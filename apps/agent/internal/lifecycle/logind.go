package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	logindDest  = "org.freedesktop.login1"
	logindPath  = dbus.ObjectPath("/org/freedesktop/login1")
	logindIface = "org.freedesktop.login1.Manager"
)

// Logind watches PrepareForSleep/PrepareForShutdown and holds a delay inhibitor.
type Logind struct {
	conn     *dbus.Conn
	ch       chan Event
	maxDelay atomic.Int64 // release the inhibitor even if the consumer is slow (ns)

	mu  sync.Mutex
	inh *os.File
	sig chan *dbus.Signal
}

// ConnectLogind connects to the system bus (addr == "") or to addr (tests).
func ConnectLogind(addr string) (*Logind, error) {
	var conn *dbus.Conn
	var err error
	if addr == "" {
		conn, err = dbus.ConnectSystemBus()
	} else {
		conn, err = dbus.Connect(addr)
	}
	if err != nil {
		return nil, fmt.Errorf("D-Bus: %w", err)
	}
	l := &Logind{conn: conn, ch: make(chan Event, 4), sig: make(chan *dbus.Signal, 8)}
	l.SetMaxDelay(4 * time.Second) // below logind's default InhibitDelayMaxSec (5 s)
	for _, member := range []string{"PrepareForSleep", "PrepareForShutdown", "PrepareForShutdownWithMetadata"} {
		if err := conn.AddMatchSignal(dbus.WithMatchObjectPath(logindPath), dbus.WithMatchInterface(logindIface), dbus.WithMatchMember(member)); err != nil {
			conn.Close()
			return nil, fmt.Errorf("logind match %s: %w", member, err)
		}
	}
	if err := l.takeInhibitor(); err != nil {
		conn.Close()
		return nil, err
	}
	conn.Signal(l.sig)
	go l.loop()
	return l, nil
}

// SetMaxDelay bounds how long a sleep/shutdown can be delayed by the agent.
func (l *Logind) SetMaxDelay(d time.Duration) { l.maxDelay.Store(int64(d)) }

// Events returns the event channel.
func (l *Logind) Events() <-chan Event { return l.ch }

// Close releases everything.
func (l *Logind) Close() error {
	l.releaseInhibitor()
	return l.conn.Close()
}

func (l *Logind) takeInhibitor() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inh != nil {
		return nil
	}
	var fd dbus.UnixFD
	err := l.conn.Object(logindDest, logindPath).Call(logindIface+".Inhibit", 0,
		"sleep:shutdown", "DeckBeacon", "Notify Home Assistant before sleep or shutdown", "delay").Store(&fd)
	if err != nil {
		return fmt.Errorf("logind Inhibit: %w", err)
	}
	l.inh = os.NewFile(uintptr(fd), "logind-inhibitor")
	return nil
}

func (l *Logind) releaseInhibitor() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inh != nil {
		_ = l.inh.Close()
		l.inh = nil
	}
}

// Holding reports whether a delay inhibitor is currently held (tests, status).
func (l *Logind) Holding() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inh != nil
}

func (l *Logind) loop() {
	// systemd >= 255 emits PrepareForShutdownWithMetadata (carrying "type":
	// reboot, power-off, ...) right after the plain PrepareForShutdown. Hold a
	// plain shutdown briefly so the metadata variant can supersede it.
	var pendingShutdown *time.Timer
	var mu sync.Mutex
	emitShutdown := func(kind string) {
		mu.Lock()
		if pendingShutdown != nil {
			pendingShutdown.Stop()
			pendingShutdown = nil
		}
		mu.Unlock()
		l.emit(Event{Kind: Shutdown, At: time.Now().Round(0), Source: "logind", Confidence: "confirmed", ShutdownType: kind})
	}
	for s := range l.sig {
		if s.Path != logindPath || len(s.Body) < 1 {
			continue
		}
		starting, ok := s.Body[0].(bool)
		if !ok {
			continue
		}
		switch s.Name {
		case logindIface + ".PrepareForSleep":
			if starting {
				l.emit(Event{Kind: Sleep, At: time.Now().Round(0), Source: "logind", Confidence: "confirmed"})
			} else {
				_ = l.takeInhibitor() // re-arm for the next sleep
				l.emit(Event{Kind: Resume, At: time.Now().Round(0), Source: "logind", Confidence: "confirmed"})
			}
		case logindIface + ".PrepareForShutdown":
			if !starting { // shutdown cancelled: hold the inhibitor again
				_ = l.takeInhibitor()
				continue
			}
			mu.Lock()
			if pendingShutdown == nil {
				pendingShutdown = time.AfterFunc(150*time.Millisecond, func() { emitShutdown("") })
			}
			mu.Unlock()
		case logindIface + ".PrepareForShutdownWithMetadata":
			if !starting {
				continue
			}
			kind := ""
			if len(s.Body) > 1 {
				if md, ok := s.Body[1].(map[string]dbus.Variant); ok {
					if v, ok := md["type"]; ok {
						kind, _ = v.Value().(string)
					}
				}
			}
			emitShutdown(kind)
		}
	}
}

// emit delivers an event; Sleep/Shutdown get a bounded inhibitor release.
func (l *Logind) emit(ev Event) {
	if ev.Kind != Resume {
		once := &sync.Once{}
		ev.once, ev.ready = once, l.releaseInhibitor
		time.AfterFunc(time.Duration(l.maxDelay.Load()), func() { once.Do(l.releaseInhibitor) })
	}
	select {
	case l.ch <- ev:
	default:
		ev.Ready() // nobody listening: never block the system
	}
}

// ErrUnavailable wraps a reason logind cannot be used.
var ErrUnavailable = errors.New("logind unavailable")
