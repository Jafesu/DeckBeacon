package activity

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// shortDir keeps unix socket paths under the 108-byte limit.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "dbipc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func frame(op uint32, body string) []byte {
	b := make([]byte, 8+len(body))
	binary.LittleEndian.PutUint32(b[0:], op)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(body)))
	copy(b[8:], body)
	return b
}

func readFrame(t *testing.T, r io.Reader) (uint32, string) {
	t.Helper()
	h := make([]byte, 8)
	if _, err := io.ReadFull(r, h); err != nil {
		t.Fatalf("read header: %v", err)
	}
	b := make([]byte, binary.LittleEndian.Uint32(h[4:]))
	if _, err := io.ReadFull(r, b); err != nil {
		t.Fatal(err)
	}
	return binary.LittleEndian.Uint32(h), string(b)
}

// fakeDiscord listens where the Flatpak client does on the G15 and records
// every byte it receives; it answers the handshake with READY.
type fakeDiscord struct {
	mu  sync.Mutex
	got bytes.Buffer
	ln  net.Listener
}

func startDiscord(t *testing.T, runtime string) *fakeDiscord {
	t.Helper()
	dir := filepath.Join(runtime, "app", "com.discordapp.Discord")
	_ = os.MkdirAll(dir, 0o700)
	ln, err := net.Listen("unix", filepath.Join(dir, "discord-ipc-0"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeDiscord{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				h := make([]byte, 8)
				first := true
				for {
					if _, err := io.ReadFull(c, h); err != nil {
						return
					}
					b := make([]byte, binary.LittleEndian.Uint32(h[4:]))
					if _, err := io.ReadFull(c, b); err != nil {
						return
					}
					f.mu.Lock()
					f.got.Write(h)
					f.got.Write(b)
					f.mu.Unlock()
					if first {
						_, _ = c.Write(frame(opFrame, `{"cmd":"DISPATCH","evt":"READY","data":{"v":1}}`))
						first = false
					}
				}
			}()
		}
	}()
	return f
}

func (f *fakeDiscord) bytes() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.got.Bytes()...)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const setActivity = `{"cmd":"SET_ACTIVITY","nonce":"1","args":{"pid":4242,"activity":{"details":"Night City\u0007 —  Watson","state":"Act 1","timestamps":{"start":1794881718000},"party":{"id":"p","size":[2,4]},"assets":{"large_image":"https://evil.invalid/x.png"},"buttons":[{"label":"x","url":"https://evil.invalid"}]}}}`

func TestProxyForwardsUnchangedAndReportsActivity(t *testing.T) {
	rt := shortDir(t)
	d := startDiscord(t, rt)
	p := New(Config{RuntimeDir: rt})
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if st := p.Status(); st.Slot != 0 || st.Listening != filepath.Join(rt, "discord-ipc-0") {
		t.Fatalf("status %+v", st)
	}
	if fi, _ := os.Stat(filepath.Join(rt, "discord-ipc-0")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v", fi.Mode().Perm())
	}

	game, err := net.Dial("unix", filepath.Join(rt, "discord-ipc-0"))
	if err != nil {
		t.Fatal(err)
	}
	defer game.Close()
	sent := append(frame(opHandshake, `{"v":1,"client_id":"123"}`), frame(opFrame, setActivity)...)
	if _, err := game.Write(sent); err != nil {
		t.Fatal(err)
	}
	if op, body := readFrame(t, game); op != opFrame || !strings.Contains(body, "READY") {
		t.Fatalf("Discord's reply must reach the game unchanged: %d %s", op, body)
	}
	waitFor(t, "forwarded frames", func() bool { return bytes.Equal(d.bytes(), sent) })
	waitFor(t, "activity", func() bool { return p.Current() != nil })
	a := p.Current()
	if a.Details != "Night City — Watson" || a.State != "Act 1" || a.PartySize != 2 || a.PartyMax != 4 || a.PID != 4242 || a.Started.Unix() != 1794881718 {
		t.Fatalf("activity %+v", a)
	}
	ms := map[string]any{}
	for _, m := range p.Metrics() {
		ms[m.Key] = m.Value
	}
	if ms["game.activity.details"] != "Night City — Watson" || ms["game.activity.party_max"] != 4 || ms["game.activity.started_at"] != "2026-11-17T02:15:18Z" {
		t.Fatalf("metrics %v", ms)
	}
	for _, m := range p.Metrics() {
		if s, _ := m.Value.(string); strings.Contains(s, "evil") {
			t.Fatal("payload URLs must never be reported")
		}
	}

	// activity: null clears.
	_, _ = game.Write(frame(opFrame, `{"cmd":"SET_ACTIVITY","nonce":"2","args":{"pid":4242,"activity":null}}`))
	waitFor(t, "clear", func() bool { return p.Current() == nil })
	_, _ = game.Write(frame(opFrame, setActivity))
	waitFor(t, "set again", func() bool { return p.Current() != nil })
	_ = game.Close()
	waitFor(t, "disconnect clears", func() bool { return p.Current() == nil })
	if ms := p.Metrics(); ms[0].Value != nil || ms[0].Reason == "" {
		t.Fatalf("no data must be null with a reason: %+v", ms[0])
	}
}

func TestProxyWithoutDiscordClosesConnection(t *testing.T) {
	rt := shortDir(t)
	p := New(Config{RuntimeDir: rt})
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	game, err := net.Dial("unix", filepath.Join(rt, "discord-ipc-0"))
	if err != nil {
		t.Fatal(err)
	}
	defer game.Close()
	_, _ = game.Write(frame(opHandshake, `{"v":1,"client_id":"1"}`))
	_ = game.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := game.Read(make([]byte, 1)); err != io.EOF && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("forward-only: no Discord means the game sees a closed socket, got %v", err)
	}
}

func TestProxySlotSelection(t *testing.T) {
	rt := shortDir(t)
	// Slot 0 held by a live server (Discord started first): take slot 1.
	live, err := net.Listen("unix", filepath.Join(rt, "discord-ipc-0"))
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	// Slot 1 is a stale socket left by a crash: replaced.
	stale, _ := net.Listen("unix", filepath.Join(rt, "discord-ipc-1"))
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	// Slot 2 would be a regular file: never touched.
	_ = os.WriteFile(filepath.Join(rt, "discord-ipc-2"), nil, 0o600)

	p := New(Config{RuntimeDir: rt})
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	st := p.Status()
	if st.Slot != 1 || !strings.Contains(st.Reason, "slot 0") {
		t.Fatalf("status %+v", st)
	}
	p.Close()
	if _, err := os.Lstat(filepath.Join(rt, "discord-ipc-1")); !os.IsNotExist(err) {
		t.Fatal("our socket must be removed on close")
	}
	if _, err := os.Lstat(filepath.Join(rt, "discord-ipc-0")); err != nil {
		t.Fatal("Discord's socket must be left alone")
	}
}

func TestProxyOversizedFrameCloses(t *testing.T) {
	rt := shortDir(t)
	startDiscord(t, rt)
	p := New(Config{RuntimeDir: rt})
	_ = p.Start()
	defer p.Close()
	game, _ := net.Dial("unix", filepath.Join(rt, "discord-ipc-0"))
	defer game.Close()
	h := make([]byte, 8)
	binary.LittleEndian.PutUint32(h[4:], maxFrame+1)
	_, _ = game.Write(h)
	_ = game.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadAll(game); err != nil {
		t.Fatalf("expected clean close, got %v", err)
	}
}

func TestCloseEndsActiveConnections(t *testing.T) {
	rt := shortDir(t)
	startDiscord(t, rt)
	p := New(Config{RuntimeDir: rt})
	_ = p.Start()
	game, _ := net.Dial("unix", filepath.Join(rt, "discord-ipc-0"))
	defer game.Close()
	_, _ = game.Write(frame(opHandshake, `{"v":1}`))
	readFrame(t, game)
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close must not wait for connected games")
	}
}

func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"  Level\t3\n\n– Boss  ": "Level 3 – Boss",
		"a‮b​c\x00d":             "abcd", // bidi override, zero-width space, NUL
		"\xff\xfeok":             "ok",
		strings.Repeat("é", 300): strings.Repeat("é", 128),
	} {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
