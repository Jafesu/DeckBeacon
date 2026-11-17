package games

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/dispatch"
)

func fixture(t *testing.T) (steamRoot, procRoot string) {
	t.Helper()
	src, _ := filepath.Abs("../../testdata/steam/g15/home/deck/.local/share/Steam")
	steamRoot = t.TempDir()
	if err := os.CopyFS(steamRoot, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	lf := filepath.Join(steamRoot, "steamapps", "libraryfolders.vdf")
	b, _ := os.ReadFile(lf)
	_ = os.WriteFile(lf, []byte(strings.ReplaceAll(string(b), "/home/deck/.local/share/Steam", steamRoot)), 0o644)
	return steamRoot, t.TempDir()
}

func startGame(t *testing.T, proc string, pid string, appID string) {
	_ = os.MkdirAll(filepath.Join(proc, pid), 0o755)
	_ = os.WriteFile(filepath.Join(proc, pid, "cmdline"), []byte("/s/reaper\x00SteamLaunch\x00AppId="+appID+"\x00--\x00game\x00"), 0o644)
}

func drain(p *Provider) []Event {
	var out []Event
	for {
		select {
		case e := <-p.Events():
			out = append(out, e)
		default:
			return out
		}
	}
}

func metric(p *Provider, key string) any {
	for _, m := range p.Metrics() {
		if m.Key == key {
			return m.Value
		}
	}
	return "missing"
}

func TestSessionEventsAndMetrics(t *testing.T) {
	sr, proc := fixture(t)
	p := New(Config{SteamRoot: sr, ProcRoot: proc})
	p.scan(true)
	p.poll()
	if metric(p, "game.current.app_id") != nil || metric(p, "library.games") != 4 || metric(p, "library.updates") != 1 {
		t.Fatalf("idle metrics %v %v %v", metric(p, "game.current.app_id"), metric(p, "library.games"), metric(p, "library.updates"))
	}
	if metric(p, "downloads.status") != "downloading" || metric(p, "downloads.name") != "Starfield" || metric(p, "downloads.progress") != 25.0 {
		t.Fatalf("download metrics %v %v %v", metric(p, "downloads.status"), metric(p, "downloads.name"), metric(p, "downloads.progress"))
	}
	startGame(t, proc, "500", "204360")
	p.poll()
	ev := drain(p)
	if len(ev) != 1 || ev[0].Type != "game_started" || ev[0].Data["name"] != "Castle Crashers" {
		t.Fatalf("events %+v", ev)
	}
	if metric(p, "game.current.name") != "Castle Crashers" {
		t.Fatal("current game metric")
	}
	_ = os.RemoveAll(filepath.Join(proc, "500"))
	p.poll()
	if ev := drain(p); len(ev) != 1 || ev[0].Type != "game_stopped" {
		t.Fatalf("stop %+v", ev)
	}
}

func TestDownloadCompletedOnlyWhenInstalled(t *testing.T) {
	sr, proc := fixture(t)
	p := New(Config{SteamRoot: sr, ProcRoot: proc})
	p.scan(true)
	m := filepath.Join(sr, "steamapps", "appmanifest_1716740.acf")
	b, _ := os.ReadFile(m)
	_ = os.WriteFile(m, []byte(strings.Replace(string(b), `"StateFlags"		"1048582"`, `"StateFlags"		"2097156"`, 1)), 0o644) // staging
	p.scan(false)
	if ev := drain(p); len(ev) != 0 {
		t.Fatalf("staging must not complete: %+v", ev)
	}
	b, _ = os.ReadFile(m)
	_ = os.WriteFile(m, []byte(strings.Replace(string(b), `"StateFlags"		"2097156"`, `"StateFlags"		"4"`, 1)), 0o644)
	p.scan(false)
	ev := drain(p)
	if len(ev) != 1 || ev[0].Type != "download_completed" || ev[0].Data["app_id"] != uint32(1716740) {
		t.Fatalf("completed %+v", ev)
	}
}

func cmd(name string, args string) dispatch.Command {
	return dispatch.Command{CommandID: "C" + name, Name: name, Args: json.RawMessage(args), TTLSeconds: 30}
}

func TestLaunch(t *testing.T) {
	sr, proc := fixture(t)
	var launched []uint32
	p := New(Config{SteamRoot: sr, ProcRoot: proc, LaunchWait: 3 * time.Second, Launch: func(id uint32) error {
		launched = append(launched, id)
		startGame(t, proc, "900", "413150") // the "game" appears shortly after
		return nil
	}})
	p.scan(true)
	h := p.LaunchHandler()
	for args, code := range map[string]string{`{"app_id": 42}`: "invalid_args", `{"app_id": 1493710}`: "invalid_args", `{"app_id": 1716740}`: "unavailable", `{}`: "invalid_args"} {
		r, follow := h(context.Background(), cmd("launch_game", args))
		if r.State != "rejected" || r.Error.Code != code || follow != nil {
			t.Errorf("%s: %+v", args, r)
		}
	}
	if len(launched) != 0 {
		t.Fatal("refused launches must never reach Steam")
	}
	r, follow := h(context.Background(), cmd("launch_game", `{"app_id": 413150}`))
	if r.State != "dispatched" || follow == nil || len(launched) != 1 || launched[0] != 413150 {
		t.Fatalf("dispatch %+v %v", r, launched)
	}
	if fr := follow(context.Background()); fr.State != "confirmed" {
		t.Fatalf("follow-up %+v", fr)
	}
}

func TestLaunchTimesOut(t *testing.T) {
	sr, proc := fixture(t)
	p := New(Config{SteamRoot: sr, ProcRoot: proc, LaunchWait: 1500 * time.Millisecond, Launch: func(uint32) error { return nil }})
	p.scan(true)
	_, follow := p.LaunchHandler()(context.Background(), cmd("launch_game", `{"app_id": 413150}`))
	if fr := follow(context.Background()); fr.State != "failed" || fr.Error.Code != "unavailable" {
		t.Fatalf("timeout %+v", fr)
	}
}

func TestLibraryPagesAndNoSteam(t *testing.T) {
	sr, proc := fixture(t)
	p := New(Config{SteamRoot: sr, ProcRoot: proc})
	p.scan(true)
	rev, pages := p.LibraryPages()
	if rev != 1 || len(pages) != 1 || len(pages[0]["games"].([]map[string]any)) != 7 {
		t.Fatalf("pages rev=%d %+v", rev, pages)
	}
	raw, _ := json.Marshal(pages)
	if strings.Contains(string(raw), "76561190000000000") {
		t.Fatal("LastOwner leaked into the library snapshot")
	}
	p.scan(false)
	if p.Revision() != 1 {
		t.Fatal("unchanged library must not bump the revision")
	}
	none := New(Config{SteamRoot: "", ProcRoot: proc})
	for _, c := range none.Capabilities() {
		if c.Status != "unavailable" {
			t.Fatalf("no Steam: %+v", c)
		}
	}
}
