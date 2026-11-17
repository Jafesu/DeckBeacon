package steam

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadShortcutsAllowlist(t *testing.T) {
	root, _ := filepath.Abs("../../testdata/steam/g15/home/deck/.local/share/Steam")
	list, err := ReadShortcuts(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[uint32]string{4244106195: "Hollow Acre", 2961728726: "Discord", 3248090148: "Discord", 2994840599: "The Tux InVader"}
	if len(list) != len(want) {
		t.Fatalf("got %d shortcuts (hidden must be excluded): %+v", len(list), list)
	}
	for _, s := range list {
		if want[s.AppID] != s.Name {
			t.Errorf("%d: name %q, want %q", s.AppID, s.Name, want[s.AppID])
		}
		if dump := fmt.Sprintf("%#v", s); strings.Contains(dump, "CANARY") {
			t.Fatalf("non-allowlisted field leaked: %s", dump)
		}
	}
	for _, s := range list {
		if s.AppID == 4244106195 && !s.LastPlayed.Equal(time.Unix(1794881977, 0)) {
			t.Errorf("last played %v", s.LastPlayed)
		}
	}
}

func TestParseShortcutsRejectsMalformed(t *testing.T) {
	root, _ := filepath.Abs("../../testdata/steam/g15/home/deck/.local/share/Steam")
	good, err := os.ReadFile(filepath.Join(root, "userdata/10000001/config/shortcuts.vdf"))
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < len(good)-1; n += 7 { // every truncation fails cleanly, never panics
		if _, err := ParseShortcuts(good[:n]); err == nil {
			t.Fatalf("truncated at %d accepted", n)
		}
	}
	bad := append([]byte{0x00, 'x', 0}, good[11:]...)
	if _, err := ParseShortcuts(bad); err == nil {
		t.Fatal("wrong root name accepted")
	}
	if _, err := ParseShortcuts(make([]byte, maxShortcutsBytes+1)); err == nil {
		t.Fatal("oversized accepted")
	}
	// Deep nesting is bounded.
	deep := []byte("\x00shortcuts\x00\x000\x00")
	for i := 0; i < 20; i++ {
		deep = append(deep, []byte("\x00t\x00")...)
	}
	if _, err := ParseShortcuts(deep); err == nil {
		t.Fatal("deep nesting accepted")
	}
	if err != nil || strings.Contains(fmt.Sprint(errShortcuts), "/") {
		t.Fatal("errors must not carry paths")
	}
}

// All three recorded forms of every G15 shortcut id (discovery §11).
func TestNormalizeIDRecordedForms(t *testing.T) {
	rows := [][3]string{
		{"-1333238570", "2961728726", "12720528017827299328"},
		{"-50861101", "4244106195", "18228297308309553152"},
		{"-372116352", "3922850944", "16848516511596281856"},
		{"-242862154", "4052105142", "17403659064876990464"},
		{"-2102558816", "2192408480", "9416322721106624512"},
		{"-1300126697", "2994840599", "12862742429471604736"},
		{"-1046877148", "3248090148", "13950440960153354240"},
		{"-2095740962", "2199226334", "9445605181065527296"},
	}
	for _, r := range rows {
		var want uint32
		fmt.Sscan(r[1], &want)
		for _, form := range r {
			if got, ok := ParseID(form); !ok || got != want {
				t.Errorf("%s -> %d,%v want %d", form, got, ok, want)
			}
		}
		if g := ShortcutGameID(want); g != r[2] {
			t.Errorf("GameID(%d) = %s want %s", want, g, r[2])
		}
	}
	// Steam games: plain appid, and as a GameID (type 0).
	for _, s := range []string{"1091500"} {
		if got, ok := ParseID(s); !ok || got != 1091500 {
			t.Errorf("%s -> %d", s, got)
		}
	}
	for _, s := range []string{"", "0", "-0", "abc", "-2147483649", "18446744073709551616", "4294967296", fmt.Sprint(uint64(3) << 24 << 32)} {
		if got, ok := ParseID(s); ok {
			t.Errorf("%q accepted as %d", s, got)
		}
	}
}

func TestUIModeWatcher(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	_ = os.MkdirAll(logs, 0o755)
	path := filepath.Join(logs, "gameprocess_log.txt")
	w := NewUIModeWatcher(dir)
	w.Poll()
	if w.Mode() != "" {
		t.Fatal("no file: no mode")
	}
	src, _ := os.ReadFile("../../testdata/steam/g15/home/deck/.local/share/Steam/logs/gameprocess_log.txt")
	_ = os.WriteFile(path, src, 0o644)
	w.Poll()
	if w.Mode() != "gamepad" {
		t.Fatalf("mode %q", w.Mode())
	}
	if strings.Contains(fmt.Sprintf("%#v", *w), "CANARY") {
		t.Fatal("non-UI-mode lines must not be retained")
	}
	appendLine := func(s string) {
		f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		_, _ = f.WriteString(s)
		_ = f.Close()
	}
	appendLine("[2026-11-16 20:24:39] SSGL: UI mode (4->")
	w.Poll()
	if w.Mode() != "gamepad" {
		t.Fatal("partial line must wait for its newline")
	}
	appendLine("7)\n")
	w.Poll()
	if w.Mode() != "desktop" {
		t.Fatalf("after close BPM %q", w.Mode())
	}
	// Rotation: Steam moves the file away and starts a new, shorter one.
	_ = os.Rename(path, filepath.Join(logs, "gameprocess_log.previous.txt"))
	_ = os.WriteFile(path, []byte("[x] SSGL: UI mode (0->9)\n"), 0o644)
	w.Poll()
	if w.Mode() != "unknown:9" {
		t.Fatalf("after rotation %q", w.Mode())
	}
	// Truncation in place.
	_ = os.WriteFile(path, []byte("SSGL: UI mode (0->4)\n"), 0o644)
	w.Poll()
	if w.Mode() != "gamepad" {
		t.Fatalf("after truncation %q", w.Mode())
	}
}

func TestScanProcsShortcutAndSession(t *testing.T) {
	proc := t.TempDir()
	write := func(pid, comm string, argv ...string) {
		_ = os.MkdirAll(filepath.Join(proc, pid), 0o755)
		_ = os.WriteFile(filepath.Join(proc, pid, "comm"), []byte(comm+"\n"), 0o644)
		if argv != nil {
			_ = os.WriteFile(filepath.Join(proc, pid, "cmdline"), []byte(strings.Join(argv, "\x00")+"\x00"), 0o644)
		}
	}
	// G15 §7/§9 shapes: launch client and supervisor embed the reaper argv.
	write("11604", "steam-runtime-l", "steam-runtime-launch-client", "--", "/s/reaper", "SteamLaunch", "AppId=4244106195", "--", "/home/u/Downloads/hollow_acre.x86_64")
	write("11610", "reaper", "/s/steamrt64/reaper", "SteamLaunch", "AppId=4244106195", "--", "/home/u/Downloads/hollow_acre.x86_64")
	write("10166", "reaper", "/s/reaper", "SteamLaunch", "AppId=1091500", "--", "/s/SteamLinuxRuntime_4/_v2-entry-point", "--verb=waitforexitandrun", "--", "/s/Proton - Experimental/proton", "waitforexitandrun", "/s/common/Cyberpunk 2077/REDprelauncher.exe")
	write("1", "gamescope-wl")
	p := ScanProcs(proc)
	if len(p.Running) != 2 || p.Running[0].AppID != 1091500 || p.Running[1].AppID != 4244106195 {
		t.Fatalf("running %+v", p.Running)
	}
	if p.Running[1].Exe != "hollow_acre" || p.Running[0].Exe != "REDprelauncher" {
		t.Fatalf("exe fallback %+v", p.Running)
	}
	if p.Session != "gaming" {
		t.Fatalf("session %q", p.Session)
	}
	write("2", "plasmashell") // Plasma wins (nested gamescope in Desktop Mode)
	if s := ScanProcs(proc).Session; s != "desktop" {
		t.Fatalf("session %q", s)
	}

	lib, _ := Scan(fixture(t))
	ch := NewTracker().Update(ScanProcs(proc).Running, lib, time.Now())
	names := map[uint32]string{}
	for _, c := range ch {
		names[c.Session.AppID] = c.Session.Name
	}
	if names[4244106195] != "Hollow Acre" || names[1091500] != "Cyberpunk 2077" {
		t.Fatalf("names %v", names)
	}
}
