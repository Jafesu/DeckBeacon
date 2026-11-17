package lifecycle

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestGapIsZeroWithoutSuspend(t *testing.T) {
	a := time.Now()
	b := a.Add(5 * time.Second)
	if g := Gap(a, b); g != 0 {
		t.Fatalf("gap %v", g)
	}
}

func TestClockWatchThreshold(t *testing.T) {
	c := NewClockWatch()
	now := time.Now()
	if c.CheckGap(2*time.Second, now) || c.CheckGap(29*time.Second, now) {
		t.Fatal("NTP-sized steps must not be reported")
	}
	if !c.CheckGap(90*time.Second, now) {
		t.Fatal("a 90 s suspend gap must be reported")
	}
	ev := <-c.Events()
	if ev.Kind != Resume || ev.Source != "clock" || ev.Confidence != "heuristic" || ev.Gap != 90*time.Second {
		t.Fatalf("event %+v", ev)
	}
}

// fakeLogind exports a minimal org.freedesktop.login1.Manager on a private bus.
type fakeLogind struct {
	conn     *dbus.Conn
	inhibits chan string
	held     chan *os.File // our end of the socketpair per Inhibit call
	peers    chan int      // the end handed to the client (we close our copy once it holds it)
}

func (f *fakeLogind) Inhibit(what, who, why, mode string) (dbus.UnixFD, *dbus.Error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC|syscall.SOCK_NONBLOCK, 0) // non-blocking: os.File deadlines need a pollable fd
	if err != nil {
		return 0, dbus.MakeFailedError(err)
	}
	f.inhibits <- what + "|" + who + "|" + mode
	f.held <- os.NewFile(uintptr(fds[0]), "logind-side")
	f.peers <- fds[1]
	return dbus.UnixFD(fds[1]), nil
}

func privateBus(t *testing.T) string {
	t.Helper()
	skip := t.Skip
	if os.Getenv("DECKBEACON_REQUIRE_DBUS") != "" { // CI: the logind evidence must not silently skip
		skip = t.Fatal
	}
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		skip("dbus-daemon not installed")
	}
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--nopidfile", "--print-address=1")
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		skip(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	addr, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(addr)
}

func startFake(t *testing.T, addr string) *fakeLogind {
	t.Helper()
	conn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	f := &fakeLogind{conn: conn, inhibits: make(chan string, 8), held: make(chan *os.File, 8), peers: make(chan int, 8)}
	if err := conn.Export(f, logindPath, logindIface); err != nil {
		t.Fatal(err)
	}
	if reply, err := conn.RequestName(logindDest, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("request name: %v %v", reply, err)
	}
	return f
}

// handOver closes the fake's copy of the client end once the client holds its
// own duplicate, so EOF on our end means "the agent released the inhibitor".
func (f *fakeLogind) handOver(t *testing.T, l *Logind) {
	t.Helper()
	peer := <-f.peers
	deadline := time.Now().Add(2 * time.Second)
	for !l.Holding() {
		if time.Now().After(deadline) {
			t.Fatal("client never stored the inhibitor")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := syscall.Close(peer); err != nil {
		t.Fatal(err)
	}
}

// released waits until the agent closed its inhibitor fd (EOF on our end).
func released(t *testing.T, f *os.File, within time.Duration) bool {
	t.Helper()
	_ = f.SetReadDeadline(time.Now().Add(within))
	buf := make([]byte, 1)
	_, err := f.Read(buf)
	return err != nil && !os.IsTimeout(err)
}

func (f *fakeLogind) emit(t *testing.T, member string, v bool) {
	t.Helper()
	if err := f.conn.Emit(logindPath, logindIface+"."+member, v); err != nil {
		t.Fatal(err)
	}
}

func TestLogindSleepResumeShutdown(t *testing.T) {
	addr := privateBus(t)
	fake := startFake(t, addr)
	l, err := ConnectLogind(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if got := <-fake.inhibits; got != "sleep:shutdown|DeckBeacon|delay" {
		t.Fatalf("inhibit %q", got)
	}
	ours := <-fake.held
	fake.handOver(t, l)

	// Sleep: the inhibitor must stay held until the consumer calls Ready.
	fake.emit(t, "PrepareForSleep", true)
	ev := <-l.Events()
	if ev.Kind != Sleep || ev.Confidence != "confirmed" {
		t.Fatalf("event %+v", ev)
	}
	if released(t, ours, 300*time.Millisecond) {
		t.Fatal("inhibitor released before the agent was ready")
	}
	ev.Ready()
	if !released(t, ours, 2*time.Second) {
		t.Fatal("inhibitor not released after Ready")
	}

	// Resume: event, and a fresh inhibitor is taken for the next sleep.
	fake.emit(t, "PrepareForSleep", false)
	if ev := <-l.Events(); ev.Kind != Resume {
		t.Fatalf("event %+v", ev)
	}
	<-fake.inhibits
	ours = <-fake.held
	fake.handOver(t, l)

	// Shutdown: a slow consumer still cannot hold the system past MaxDelay.
	l.SetMaxDelay(300 * time.Millisecond)
	fake.emit(t, "PrepareForShutdown", true)
	if ev := <-l.Events(); ev.Kind != Shutdown {
		t.Fatalf("event %+v", ev)
	}
	if !released(t, ours, 2*time.Second) {
		t.Fatal("inhibitor not released after MaxDelay")
	}
}

func TestLogindUnavailable(t *testing.T) {
	addr := privateBus(t) // a bus with no logind on it
	if _, err := ConnectLogind(addr); err == nil {
		t.Fatal("expected an error without logind")
	}
}

func TestClockWatchRunStops(t *testing.T) {
	c := NewClockWatch()
	c.Interval = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	c.Run(ctx) // must return
}

func (f *fakeLogind) emitMeta(t *testing.T, kind string) {
	t.Helper()
	if err := f.conn.Emit(logindPath, logindIface+".PrepareForShutdownWithMetadata", true, map[string]dbus.Variant{"type": dbus.MakeVariant(kind)}); err != nil {
		t.Fatal(err)
	}
}

func TestLogindShutdownTypeFromMetadata(t *testing.T) {
	addr := privateBus(t)
	fake := startFake(t, addr)
	l, err := ConnectLogind(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	<-fake.inhibits
	<-fake.held
	fake.handOver(t, l)
	// systemd 261 (G15) emits both signals back to back: exactly one event, with the type.
	fake.emit(t, "PrepareForShutdown", true)
	fake.emitMeta(t, "reboot")
	ev := <-l.Events()
	if ev.Kind != Shutdown || ev.ShutdownType != "reboot" {
		t.Fatalf("event %+v", ev)
	}
	ev.Ready()
	select {
	case extra := <-l.Events():
		t.Fatalf("duplicate shutdown event %+v", extra)
	case <-time.After(400 * time.Millisecond):
	}
}

func TestLogindPlainShutdownWithoutMetadata(t *testing.T) {
	addr := privateBus(t)
	fake := startFake(t, addr)
	l, err := ConnectLogind(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	<-fake.inhibits
	<-fake.held
	fake.handOver(t, l)
	fake.emit(t, "PrepareForShutdown", true) // older systemd: no metadata variant
	ev := <-l.Events()
	if ev.Kind != Shutdown || ev.ShutdownType != "" {
		t.Fatalf("event %+v", ev)
	}
	ev.Ready()
}
