package app

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/lifecycle"
	"github.com/jafesu/deckbeacon/apps/agent/internal/paths"
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

func TestPinnedBootTimeStableAcrossRestarts(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"), 10)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	first := time.Date(2026, 11, 17, 0, 40, 42, 0, time.UTC)
	if got := pinnedBootTime(st, "b1", first); !got.Equal(first) {
		t.Fatal(got)
	}
	if got := pinnedBootTime(st, "b1", first.Add(time.Second)); !got.Equal(first) { // btime moved by 1 s
		t.Fatalf("boot time moved to %v", got)
	}
	if got := pinnedBootTime(st, "b2", first.Add(time.Hour)); !got.Equal(first.Add(time.Hour)) {
		t.Fatal("new boot must take the new time")
	}
}

func TestActivityToggle(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	e := Env{Dirs: paths.Dirs{Config: filepath.Join(dir, "c"), State: filepath.Join(dir, "s"), Runtime: filepath.Join(dir, "r")}, Stdout: &out, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if loadActivityConfig(e).DiscordProxy || startActivity(e) != nil {
		t.Fatal("the proxy must be off by default")
	}
	if err := Activity(e, "on"); err != nil {
		t.Fatal(err)
	}
	if !loadActivityConfig(e).DiscordProxy {
		t.Fatal("on not persisted")
	}
	if fi, _ := os.Stat(activityPath(e)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	_ = Activity(e, "off")
	if loadActivityConfig(e).DiscordProxy {
		t.Fatal("off not persisted")
	}
	if Activity(e, "maybe") == nil {
		t.Fatal("bad action accepted")
	}
}
