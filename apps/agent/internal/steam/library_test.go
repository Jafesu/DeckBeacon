package steam

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) string {
	t.Helper()
	// The fixture's libraryfolders.vdf points at /home/deck/...: rewrite it to the
	// temp copy so Scan works anywhere.
	src, _ := filepath.Abs("../../testdata/steam/g15/home/deck/.local/share/Steam")
	dst := t.TempDir()
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	lf := filepath.Join(dst, "steamapps", "libraryfolders.vdf")
	b, _ := os.ReadFile(lf)
	_ = os.WriteFile(lf, []byte(strings.ReplaceAll(string(b), "/home/deck/.local/share/Steam", dst)), 0o644)
	return dst
}

func TestScanRecordedG15Library(t *testing.T) {
	root := fixture(t)
	lib, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Folders) != 2 || !lib.Folders[0].Present || lib.Folders[1].Present || !lib.Folders[1].Removable {
		t.Fatalf("folders %+v (SD card listed but not mounted)", lib.Folders)
	}
	if len(lib.Games) != 11 { // 7 manifests (the truncated one skipped) + 4 visible shortcuts
		t.Fatalf("got %d entries", len(lib.Games))
	}
	games, updates, shortcuts := lib.Counts()
	if games != 4 || updates != 1 || shortcuts != 4 {
		t.Fatalf("games=%d updates=%d", games, updates)
	}
	for id, want := range map[uint32]Kind{228980: KindTool, 1493710: KindTool, 1628350: KindTool, 1091500: KindGame, 204360: KindGame, 4244106195: KindShortcut} {
		if g := lib.Find(id); g == nil || g.Kind != want {
			t.Errorf("%d kind %+v", id, g)
		}
	}
	cp := lib.Find(1091500)
	if cp.Name != "Cyberpunk 2077" || cp.Status != "installed" || cp.SizeOnDisk != 66818683782 || cp.LastPlayed.Unix() != 1794775004 {
		t.Fatalf("cyberpunk %+v", cp)
	}
	sf := lib.Find(1716740)
	if sf.Status != "downloading" || sf.BytesDownloaded != 28114436288 {
		t.Fatalf("starfield %+v", sf)
	}
}

func TestScanNeverOpensCredentialFiles(t *testing.T) {
	// The hard guarantee is ReadVDF refusing credential files (vdf_test.go). Scan
	// only opens libraryfolders.vdf and appmanifest_*.acf, so a canary in
	// config/config.vdf can never surface in the result.
	for _, g := range mustScan(t, fixture(t)).Games {
		if strings.Contains(g.Name, "CANARY") || strings.Contains(g.BuildID, "CANARY") {
			t.Fatal("credential content leaked into the library")
		}
	}
}

func mustScan(t *testing.T, root string) *Library {
	lib, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	return lib
}

func TestStatusFlags(t *testing.T) {
	for flags, want := range map[uint64]string{4: "installed", 6: "update_required", 1048582: "downloading", 516: "paused", 131076: "verifying", 2097156: "installing", 0: "unknown", 2052: "uninstalling"} {
		if got := Status(flags); got != want {
			t.Errorf("Status(%d) = %s, want %s", flags, got, want)
		}
	}
}

func TestRunningAppsAndTracker(t *testing.T) {
	proc := t.TempDir()
	write := func(pid, cmd string) {
		_ = os.MkdirAll(filepath.Join(proc, pid), 0o755)
		_ = os.WriteFile(filepath.Join(proc, pid, "cmdline"), []byte(strings.ReplaceAll(cmd, " ", "\x00")+"\x00"), 0o644)
	}
	write("100", "/home/deck/.local/share/Steam/ubuntu12_32/reaper SteamLaunch AppId=204360 -- /proton waitforexitandrun castle.exe")
	write("101", "/usr/bin/bash -c echo AppId=999")                // not reaper
	write("102", "/x/reaper SteamLaunch AppId=1628350 -- runtime") // a tool
	write("103", "/x/reaper -- SteamLaunch AppId=777")             // after "--": ignored
	got := ScanProcs(proc).Running
	if len(got) != 2 || got[0].AppID != 204360 || got[1].AppID != 1628350 {
		t.Fatalf("running %v", got)
	}

	lib, _ := Scan(fixture(t))
	tr := NewTracker()
	t0 := time.Now()
	ch := tr.Update(got, lib, t0)
	if len(ch) != 1 || ch[0].Type != "game_started" || ch[0].Session.Name != "Castle Crashers" {
		t.Fatalf("start %+v (tools must not start sessions)", ch)
	}
	if ch := tr.Update(got, lib, t0.Add(time.Second)); len(ch) != 0 {
		t.Fatalf("duplicate start %+v", ch)
	}
	ch = tr.Update(nil, lib, t0.Add(90*time.Second))
	if len(ch) != 1 || ch[0].Type != "game_stopped" || ch[0].Duration != 90*time.Second {
		t.Fatalf("stop %+v", ch)
	}
	if tr.Current() != nil {
		t.Fatal("no current session expected")
	}
}
