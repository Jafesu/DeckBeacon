package steam

import (
	"time"
)

// Session is a running game session. Start is wall-clock (for display); the
// duration uses the monotonic clock, which stops during suspend on Linux, so
// suspended time is excluded (spec 06: monotonic durations).
type Session struct {
	AppID   uint32
	Name    string
	Started time.Time
	mono    time.Time
}

// Duration excludes time spent suspended.
func (s *Session) Duration(now time.Time) time.Duration { return now.Sub(s.mono) }

// Change is a session transition for the event stream.
type Change struct {
	Type     string // game_started | game_stopped
	Session  Session
	Duration time.Duration
}

// Tracker turns successive "running app" samples into start/stop changes.
type Tracker struct {
	active map[uint32]*Session
}

// NewTracker creates an empty tracker.
func NewTracker() *Tracker { return &Tracker{active: map[uint32]*Session{}} }

// Update compares the running set with the active sessions.
// A game missing from the library is named after its launched program.
func (t *Tracker) Update(running []Running, lib *Library, now time.Time) []Change {
	var out []Change
	cur := map[uint32]bool{}
	for _, r := range running {
		id := r.AppID
		cur[id] = true
		if _, ok := t.active[id]; ok {
			continue
		}
		name := ""
		if lib != nil {
			if g := lib.Find(id); g != nil {
				if g.Kind == KindTool {
					continue // runtimes/Proton are never "games being played"
				}
				name = g.Name
			}
		}
		if name == "" {
			name = r.Exe
		}
		s := &Session{AppID: id, Name: name, Started: now.Round(0).UTC(), mono: now}
		t.active[id] = s
		out = append(out, Change{Type: "game_started", Session: *s})
	}
	for id, s := range t.active {
		if !cur[id] {
			delete(t.active, id)
			out = append(out, Change{Type: "game_stopped", Session: *s, Duration: s.Duration(now)})
		}
	}
	return out
}

// Current returns the most recently started active session, if any.
func (t *Tracker) Current() *Session {
	var best *Session
	for _, s := range t.active {
		if best == nil || s.Started.After(best.Started) {
			best = s
		}
	}
	return best
}
