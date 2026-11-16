package session

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/lifecycle"
	"github.com/jafesu/deckbeacon/apps/agent/internal/ulid"
)

func eventTypes(r *rig) []string {
	var out []string
	for _, m := range r.srv.Messages("event") {
		var e struct {
			EventType string `json:"event_type"`
		}
		_ = json.Unmarshal(m.Body, &e)
		out = append(out, e.EventType)
	}
	return out
}

func has(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestSleepAnnouncedBeforeInhibitorRelease(t *testing.T) {
	r := newRig(t)
	lc := make(chan lifecycle.Event, 1)
	r.cli.cfg.Lifecycle = lc
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.cli.Run(ctx)
	waitFor(t, "connected", func() bool { return r.cli.Status().State == "connected_local" })

	var releasedWithAnnounce atomic.Bool
	released := make(chan struct{})
	lc <- lifecycle.NewEvent(lifecycle.Sleep, "logind", "confirmed", func() {
		st, _ := r.store.Stats()
		releasedWithAnnounce.Store(has(eventTypes(r), "device_suspending") && st.Pending == 0)
		close(released)
	})
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("inhibitor never released")
	}
	if !releasedWithAnnounce.Load() {
		t.Fatal("inhibitor released before HA received and acked device_suspending (AC3)")
	}
}

func TestResumeReconnectsWithoutBackoff(t *testing.T) {
	r := newRig(t)
	lc := make(chan lifecycle.Event, 1)
	r.cli.cfg.Lifecycle = lc
	r.cli.cfg.Backoff = func(int) time.Duration { return time.Hour } // any backoff would stall the test
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.cli.Run(ctx)
	waitFor(t, "connected", func() bool { return r.cli.Status().State == "connected_local" })
	hellos := len(r.srv.Messages("hello"))
	lc <- lifecycle.NewEvent(lifecycle.Resume, "logind", "confirmed", nil)
	waitFor(t, "reconnect after resume", func() bool { return len(r.srv.Messages("hello")) > hellos })
	waitFor(t, "device_resumed delivered", func() bool { return has(eventTypes(r), "device_resumed") })
}

func TestSleepWhileOfflineDoesNotDelaySystem(t *testing.T) {
	r := newRig(t)
	lc := make(chan lifecycle.Event, 1)
	r.cli.cfg.Lifecycle = lc
	r.srv.Close() // HA unreachable
	r.cli.cfg.Backoff = func(int) time.Duration { return time.Hour }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.cli.Run(ctx)
	waitFor(t, "reconnecting", func() bool { return r.cli.Status().State == "reconnecting" })
	done := make(chan struct{})
	lc <- lifecycle.NewEvent(lifecycle.Shutdown, "logind", "confirmed", func() { close(done) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("offline shutdown was delayed")
	}
	pend, _ := r.store.PendingEvents(0, 10)
	if len(pend) == 0 || pend[len(pend)-1].EventType != "device_shutting_down" {
		t.Fatalf("shutdown not queued for later delivery: %+v", pend)
	}
}

func TestUnknownCommandFromNewerHAIsRejectedNotFatal(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.cli.Run(ctx)
	waitFor(t, "connected", func() bool { return r.cli.Status().State == "connected_local" })
	hellos := len(r.srv.Messages("hello"))
	id := ulid.New()
	r.srv.SendRawCommand(map[string]any{"command_id": id, "name": "teleport", "args": map[string]any{}, "ttl_s": 30, "issued_at": time.Now().UTC().Format(time.RFC3339)})
	waitFor(t, "rejection", func() bool { return len(r.srv.Messages("command_result")) == 1 })
	var res struct {
		CommandID string                `json:"command_id"`
		State     string                `json:"state"`
		Error     struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(r.srv.Messages("command_result")[0].Body, &res)
	if res.CommandID != id || res.State != "rejected" || res.Error.Code != "unsupported" {
		t.Fatalf("result %+v", res)
	}
	time.Sleep(300 * time.Millisecond)
	if len(r.srv.Messages("hello")) != hellos {
		t.Fatal("session was dropped for an unknown command (lenient receiver violated)")
	}
}
