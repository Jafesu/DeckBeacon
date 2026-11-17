package dispatch

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/store"
)

func newD(t *testing.T, now *time.Time) *Dispatcher {
	s, err := store.Open(filepath.Join(t.TempDir(), "s.db"), 10)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s, func() time.Time { return *now })
}

func TestUnsupportedDuplicateExpired(t *testing.T) {
	now := time.Unix(1000, 0)
	d := newD(t, &now)
	ctx := context.Background()
	r := d.Handle(ctx, Command{CommandID: "A", Name: "launch_game", TTLSeconds: 60}, now)
	if len(r) != 1 || r[0].State != "rejected" || r[0].Error.Code != "unsupported" {
		t.Fatalf("got %+v", r)
	}
	r = d.Handle(ctx, Command{CommandID: "A", Name: "launch_game", TTLSeconds: 60}, now)
	if r[0].State != "duplicate" {
		t.Fatalf("replay got %+v", r)
	}
	received := now
	now = now.Add(61 * time.Second)
	r = d.Handle(ctx, Command{CommandID: "B", Name: "launch_game", TTLSeconds: 60}, received)
	if r[0].State != "expired" || r[0].Error.Code != "expired" {
		t.Fatalf("expiry got %+v", r)
	}
}

func TestRegisteredHandler(t *testing.T) {
	now := time.Unix(1000, 0)
	d := newD(t, &now)
	calls := 0
	d.Register("refresh_library", "monitoring", func(ctx context.Context, c Command) (Result, func(context.Context) Result) {
		calls++
		return Result{CommandID: c.CommandID, State: "confirmed"}, nil
	})
	r := d.Handle(context.Background(), Command{CommandID: "C", Name: "refresh_library", TTLSeconds: 5}, now)
	if len(r) != 2 || r[0].State != "accepted" || r[1].State != "confirmed" {
		t.Fatalf("got %+v", r)
	}
	d.Handle(context.Background(), Command{CommandID: "C", Name: "refresh_library", TTLSeconds: 5}, now)
	if calls != 1 {
		t.Fatalf("handler executed %d times; replay must not re-execute", calls)
	}
}

func TestTierEnforcementAndFollowUp(t *testing.T) {
	now := time.Unix(1000, 0)
	d := newD(t, &now)
	d.Register("launch_game", "game_management", func(ctx context.Context, c Command) (Result, func(context.Context) Result) {
		return Result{CommandID: c.CommandID, State: "dispatched"}, func(context.Context) Result {
			return Result{CommandID: c.CommandID, State: "confirmed"}
		}
	})
	r := d.Handle(context.Background(), Command{CommandID: "L1", Name: "launch_game", TTLSeconds: 30}, now)
	if r[0].State != "rejected" || r[0].Error.Code != "permission_denied" {
		t.Fatalf("not granted: %+v", r)
	}
	d.SetGranted([]string{"monitoring", "game_management"})
	r = d.Handle(context.Background(), Command{CommandID: "L2", Name: "launch_game", TTLSeconds: 30}, now)
	if len(r) != 2 || r[1].State != "dispatched" {
		t.Fatalf("granted: %+v", r)
	}
	select {
	case late := <-d.Late():
		if late.CommandID != "L2" || late.State != "confirmed" {
			t.Fatalf("late %+v", late)
		}
	case <-time.After(time.Second):
		t.Fatal("no follow-up result")
	}
}
