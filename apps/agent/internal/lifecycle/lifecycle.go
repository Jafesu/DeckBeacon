// Package lifecycle reports sleep, resume and shutdown to the agent (spec 03):
//
//   - Logind: PrepareForSleep / PrepareForShutdown from org.freedesktop.login1,
//     with a "delay" inhibitor so the agent can tell HA *before* the device sleeps.
//   - ClockWatch: fallback resume detection from wall-clock vs monotonic
//     divergence (CLOCK_MONOTONIC stops during suspend on Linux).
package lifecycle

import (
	"sync"
	"time"
)

// Kind of lifecycle transition.
type Kind string

const (
	Sleep    Kind = "sleep"
	Resume   Kind = "resume"
	Shutdown Kind = "shutdown"
)

// Event is one transition. For Sleep and Shutdown the consumer MUST call Ready
// once it has told HA; the inhibitor is released then (or after MaxDelay).
type Event struct {
	Kind       Kind
	At         time.Time
	Source     string // "logind" | "clock"
	Confidence string // "confirmed" | "heuristic"
	Gap        time.Duration
	ready      func()
	once       *sync.Once
}

// Ready releases the delay inhibitor held for this transition (idempotent).
func (e Event) Ready() {
	if e.ready != nil && e.once != nil {
		e.once.Do(e.ready)
	}
}

// Monitor is a source of lifecycle events.
type Monitor interface {
	Events() <-chan Event
	Close() error
}

// Capabilities reported for a monitor configuration.
type Capabilities struct {
	SleepNotify    bool
	ShutdownNotify bool
	ResumeDetect   string // "logind" | "heuristic"
	Reason         string // why logind is unavailable, if it is
}

// NewEvent builds an event whose Ready runs ready once (used by monitors and tests).
func NewEvent(kind Kind, source, confidence string, ready func()) Event {
	return Event{Kind: kind, At: time.Now().Round(0), Source: source, Confidence: confidence, ready: ready, once: &sync.Once{}}
}
