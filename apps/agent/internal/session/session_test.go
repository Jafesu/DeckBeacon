package session

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/collect"
	"github.com/jafesu/deckbeacon/apps/agent/internal/dispatch"
	"github.com/jafesu/deckbeacon/apps/agent/internal/identity"
	"github.com/jafesu/deckbeacon/apps/agent/internal/pairing"
	"github.com/jafesu/deckbeacon/apps/agent/internal/store"
	"github.com/jafesu/deckbeacon/apps/agent/internal/testutil/fakeha"
	"github.com/jafesu/deckbeacon/apps/agent/internal/trust"
	"github.com/jafesu/deckbeacon/apps/agent/internal/ulid"
)

type fakeCollector struct{}

func (fakeCollector) Collect(context.Context) []collect.Metric {
	return []collect.Metric{{Key: "cpu.usage", Value: 12.5, Unit: "%", SampledAt: time.Now().UTC().Truncate(time.Second), Source: "test"},
		{Key: "battery.level", Value: nil, SampledAt: time.Now().UTC().Truncate(time.Second), Source: "test", Reason: "no battery"}}
}
func (fakeCollector) Capabilities(context.Context) []collect.Capability {
	return []collect.Capability{{ID: "telemetry.cpu", Status: "available", Tier: "system", Source: "test"}}
}

type rig struct {
	srv   *fakeha.Server
	id    *identity.Identity
	store *store.Store
	cli   *Client
}

func newRig(t *testing.T) *rig {
	t.Helper()
	srv := fakeha.New()
	t.Cleanup(srv.Close)
	srv.OpenWindow()
	dir := t.TempDir()
	id, _ := identity.LoadOrCreate(dir)
	tr, err := pairing.Pair(context.Background(), srv.URL(), id, pairing.Request{AgentVersion: "0.1.0", Protocol: map[string]int{"min": 1, "max": 1}, Name: "deck",
		Device: map[string]any{"device_id": id.DeviceID, "os": map[string]any{"id": "steamos", "version_id": "3.7.0"}, "arch": "x86_64"}},
		func(context.Context, string) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	<-srv.ConfirmSAS
	st, _ := store.Open(filepath.Join(dir, "s.db"), 100)
	t.Cleanup(func() { st.Close() })
	cli, err := New(Config{Identity: id, Trust: tr, Store: st, Collector: fakeCollector{}, Dispatcher: dispatch.New(st, time.Now),
		Hello:             HelloInfo{AgentVersion: "0.1.0", Device: map[string]any{"os": map[string]any{"id": "steamos", "version_id": "3.7.0"}, "arch": "x86_64"}, BootID: "0123456789abcdef0123456789abcdef"},
		TelemetryInterval: 200 * time.Millisecond, Backoff: func(int) time.Duration { return 50 * time.Millisecond }})
	if err != nil {
		t.Fatal(err)
	}
	return &rig{srv, id, st, cli}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestSessionLifecycle(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.cli.Run(ctx) }()

	waitFor(t, "connected", func() bool { return r.cli.Status().State == "connected_local" })
	waitFor(t, "telemetry x2", func() bool { return len(r.srv.Messages("telemetry")) >= 2 })
	waitFor(t, "capabilities", func() bool { return len(r.srv.Messages("capabilities")) == 1 })
	waitFor(t, "connection event acked", func() bool { st, _ := r.store.Stats(); return st.Acked >= 1 && st.Pending == 0 })

	// Commands: unsupported, then replay → duplicate (AC7).
	cid := ulid.New()
	cmd := map[string]any{"command_id": cid, "name": "launch_game", "args": map[string]any{"app_id": 570}, "ttl_s": 30, "issued_at": time.Now().UTC().Format(time.RFC3339)}
	r.srv.SendCommand(cmd)
	r.srv.SendCommand(cmd)
	waitFor(t, "two results", func() bool { return len(r.srv.Messages("command_result")) == 2 })
	var states []string
	for _, m := range r.srv.Messages("command_result") {
		var b struct{ State string }
		_ = json.Unmarshal(m.Body, &b)
		states = append(states, b.State)
	}
	if states[0] != "rejected" || states[1] != "duplicate" {
		t.Fatalf("command states %v", states)
	}

	// Forced drop → reconnect and resume; second connection event must be delivered once.
	before := len(r.srv.Messages("hello"))
	r.srv.DropAll()
	waitFor(t, "reconnect", func() bool { return len(r.srv.Messages("hello")) > before && r.cli.Status().State == "connected_local" })
	waitFor(t, "second connection event acked", func() bool { st, _ := r.store.Stats(); return st.Acked >= 2 && st.Pending == 0 })
	seen := map[string]int{}
	for _, m := range r.srv.Messages("event") {
		var e struct {
			EventID string `json:"event_id"`
		}
		_ = json.Unmarshal(m.Body, &e)
		seen[e.EventID]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("event %s delivered %d times", id, n)
		}
	}
	var hello struct {
		Session struct {
			ResumeAfter uint64 `json:"resume_after_event_seq"`
		} `json:"session"`
	}
	hs := r.srv.Messages("hello")
	_ = json.Unmarshal(hs[len(hs)-1].Body, &hello)
	if hello.Session.ResumeAfter < 1 {
		t.Fatalf("resume point not advertised: %+v", hello)
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run returned %v", err)
	}
	if len(r.srv.Invalid) != 0 {
		t.Fatalf("agent sent schema-invalid messages: %v", r.srv.Invalid)
	}
}

func TestRevocationStopsClient(t *testing.T) {
	r := newRig(t)
	r.srv.Revoke(hex.EncodeToString(r.id.Channel.Public))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := r.cli.Run(ctx)
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("want ErrRevoked, got %v", err)
	}
	if r.cli.Status().State != "revoked" {
		t.Fatalf("status %+v", r.cli.Status())
	}
}

func TestWrongPinnedKeyNeverConnects(t *testing.T) {
	r := newRig(t)
	bad := *r.cli.cfg.Trust
	bad.ServerStatic = hex.EncodeToString(make([]byte, 32))
	bad.URLs = []trust.URL{{URL: r.srv.URL(), Kind: "manual"}}
	cli, err := New(Config{Identity: r.id, Trust: &bad, Store: r.store, Collector: fakeCollector{}, Dispatcher: dispatch.New(r.store, time.Now),
		Hello: r.cli.cfg.Hello, Backoff: func(int) time.Duration { return 20 * time.Millisecond }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	_ = cli.Run(ctx)
	if len(r.srv.Messages("hello")) != 0 {
		t.Fatal("server accepted a session for a mismatched pinned key")
	}
}

func TestBackoffBounds(t *testing.T) {
	for a := 0; a < 20; a++ {
		d := DefaultBackoff(a)
		if d < 800*time.Millisecond || d > 72*time.Second {
			t.Fatalf("attempt %d backoff %v out of bounds", a, d)
		}
	}
}

type fakeGames struct{ ch chan struct{} }

func (f fakeGames) Metrics() []collect.Metric {
	return []collect.Metric{{Key: "game.current.app_id", Value: 413150, SampledAt: time.Now().UTC().Truncate(time.Second), Source: "test"}}
}
func (fakeGames) LibraryPages() (uint64, []map[string]any) { return 0, nil }
func (fakeGames) Revision() uint64                         { return 0 }
func (f fakeGames) Changed() <-chan struct{}               { return f.ch }

func TestGameChangePushesTelemetryImmediately(t *testing.T) {
	r := newRig(t)
	g := fakeGames{ch: make(chan struct{}, 1)}
	r.cli.cfg.Games = g
	r.cli.cfg.TelemetryInterval = time.Hour // only the change may cause the second message
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.cli.Run(ctx) }()
	waitFor(t, "initial telemetry", func() bool { return len(r.srv.Messages("telemetry")) == 1 })
	g.ch <- struct{}{}
	start := time.Now()
	waitFor(t, "pushed telemetry", func() bool { return len(r.srv.Messages("telemetry")) == 2 })
	if time.Since(start) > time.Second {
		t.Fatalf("push took %v", time.Since(start))
	}
}

func TestResultAttachmentsSentBeforeResult(t *testing.T) {
	r := newRig(t)
	r.cli.cfg.Dispatcher.Register("get_artwork", "monitoring", func(ctx context.Context, cmd dispatch.Command) (dispatch.Result, func(context.Context) dispatch.Result) {
		chunk := func(seq int) dispatch.Attachment {
			return dispatch.Attachment{Type: "artwork", Body: map[string]any{"command_id": cmd.CommandID, "app_id": 1091500, "kind": "capsule", "mime": "image/jpeg",
				"width": 1, "height": 1, "seq": seq, "total": 2, "sha256": strings.Repeat("0", 64), "data": "AAAA"}}
		}
		return dispatch.Result{CommandID: cmd.CommandID, State: "confirmed", Attachments: []dispatch.Attachment{chunk(0), chunk(1)}}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.cli.Run(ctx) }()
	waitFor(t, "connected", func() bool { return r.cli.Status().State == "connected_local" })
	cid := ulid.New()
	r.srv.SendCommand(map[string]any{"command_id": cid, "name": "get_artwork", "args": map[string]any{"app_id": 1091500, "kind": "capsule"}, "ttl_s": 30, "issued_at": time.Now().UTC().Format(time.RFC3339)})
	waitFor(t, "confirmed", func() bool {
		for _, m := range r.srv.Messages("command_result") {
			if strings.Contains(string(m.Body), `"confirmed"`) {
				return true
			}
		}
		return false
	})
	var order []string
	for _, m := range r.srv.Messages("") {
		if m.Type == "artwork" || (m.Type == "command_result" && strings.Contains(string(m.Body), cid)) {
			order = append(order, m.Type)
		}
	}
	if strings.Join(order, ",") != "command_result,artwork,artwork,command_result" { // accepted, chunks, confirmed
		t.Fatalf("order %v", order)
	}
}
