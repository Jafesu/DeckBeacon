package lifecycle

import (
	"context"
	"time"
)

// ClockWatch detects resumes from suspend by comparing elapsed wall time with
// elapsed monotonic time between ticks. NTP slews and small steps stay below
// Threshold; a suspend of Threshold or longer is reported as a heuristic Resume.
type ClockWatch struct {
	Interval  time.Duration
	Threshold time.Duration
	// Now returns the current time with a monotonic reading (time.Now). Tests
	// inject synthetic readings through Sample instead.
	Now func() time.Time
	ch  chan Event
}

// NewClockWatch creates a watcher with sane defaults (5 s ticks, 30 s threshold).
func NewClockWatch() *ClockWatch {
	return &ClockWatch{Interval: 5 * time.Second, Threshold: 30 * time.Second, Now: time.Now, ch: make(chan Event, 4)}
}

// Events returns the event channel.
func (c *ClockWatch) Events() <-chan Event { return c.ch }

// Close is a no-op (Run stops with its context).
func (c *ClockWatch) Close() error { return nil }

// Gap returns how much more wall time than monotonic time passed between two
// time.Now readings: ~0 normally, the suspend duration after a resume.
func Gap(prev, cur time.Time) time.Duration {
	wall := cur.Round(0).Sub(prev.Round(0)) // Round(0) strips the monotonic reading
	mono := cur.Sub(prev)                   // uses monotonic readings when both have them
	return wall - mono
}

// Check evaluates one pair of readings and emits a Resume if warranted.
func (c *ClockWatch) Check(prev, cur time.Time) bool { return c.CheckGap(Gap(prev, cur), cur) }

// CheckGap emits a Resume when gap reaches the threshold.
func (c *ClockWatch) CheckGap(gap time.Duration, cur time.Time) bool {
	if gap < c.Threshold {
		return false
	}
	select {
	case c.ch <- Event{Kind: Resume, At: cur.Round(0), Source: "clock", Confidence: "heuristic", Gap: gap}:
	default:
	}
	return true
}

// Run ticks until ctx is done.
func (c *ClockWatch) Run(ctx context.Context) {
	t := time.NewTicker(c.Interval)
	defer t.Stop()
	prev := c.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cur := c.Now()
			c.Check(prev, cur)
			prev = cur
		}
	}
}
