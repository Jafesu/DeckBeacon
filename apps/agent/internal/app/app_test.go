package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/lifecycle"
	"github.com/jafesu/deckbeacon/apps/agent/internal/service"
	"github.com/jafesu/deckbeacon/apps/agent/internal/store"
)

func TestDetectBootOncePerBootID(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"), 10)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bt := time.Date(2026, 11, 16, 14, 40, 0, 0, time.UTC)
	now := bt.Add(time.Minute)
	for i, tc := range []struct {
		boot string
		want bool
	}{{"aaaa", true}, {"aaaa", false}, {"aaaa", false}, {"bbbb", true}} {
		got, err := detectBoot(st, tc.boot, bt, now)
		if err != nil || got != tc.want {
			t.Fatalf("step %d: got %v (%v), want %v", i, got, err, tc.want)
		}
	}
	pend, _ := st.PendingEvents(0, 10)
	if len(pend) != 2 || pend[0].EventType != "device_booted" || string(pend[1].Data) != `{"boot_time":"2026-11-16T14:40:00Z","previous_boot_id":"aaaa"}` {
		t.Fatalf("events %+v / %s", pend, pend[1].Data)
	}
}

func TestLifecycleCapabilities(t *testing.T) {
	root := t.TempDir()
	svc := service.Manager{Root: root, User: "deck"}
	caps := map[string]string{}
	for _, c := range lifecycleCapabilities(lifecycle.Capabilities{ResumeDetect: "heuristic", Reason: "no system bus"}, svc) {
		caps[c.ID] = c.Status + "|" + c.Reason
	}
	if caps["lifecycle.sleep_notify"] != "unavailable|no system bus" || caps["service.start_at_boot"] != "disabled|opt-in: deckbeacon service boot on" ||
		caps["lifecycle.resume_detect"] != "available|heuristic: logind unavailable" {
		t.Fatalf("caps %v", caps)
	}
	caps = map[string]string{}
	for _, c := range lifecycleCapabilities(lifecycle.Capabilities{SleepNotify: true, ShutdownNotify: true, ResumeDetect: "logind"}, svc) {
		caps[c.ID] = c.Status
	}
	if caps["lifecycle.sleep_notify"] != "available" || caps["lifecycle.resume_detect"] != "available" {
		t.Fatalf("caps %v", caps)
	}
}

func TestMergeLifecycleIgnoresClockWhenLogindPresent(t *testing.T) {
	for _, haveLogind := range []bool{true, false} {
		ctx, cancel := context.WithCancel(context.Background())
		logindCh, clockCh, out := make(chan lifecycle.Event, 2), make(chan lifecycle.Event, 2), make(chan lifecycle.Event, 4)
		go mergeLifecycle(ctx, logindCh, clockCh, haveLogind, out)
		clockCh <- lifecycle.NewEvent(lifecycle.Resume, "clock", "heuristic", nil) // arrives first, as on the G15
		logindCh <- lifecycle.NewEvent(lifecycle.Resume, "logind", "confirmed", nil)
		time.Sleep(50 * time.Millisecond)
		cancel()
		var got []string
		for len(out) > 0 {
			got = append(got, (<-out).Source)
		}
		want := "logind"
		if !haveLogind {
			want = "clock,logind" // without logind connected only the clock would fire in practice
		}
		if strings.Join(got, ",") != want && !(haveLogind == false && strings.Join(got, ",") == "logind,clock") {
			t.Fatalf("haveLogind=%v: forwarded %v, want %s", haveLogind, got, want)
		}
	}
}
