package steam

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Running is a game launched by Steam. Exe is a display fallback only: the
// basename of the launched program without extension (never the path).
type Running struct {
	AppID uint32
	Exe   string
}

// Procs is one pass over the process table.
type Procs struct {
	Running []Running
	// Session is "gaming" (gamescope, no Plasma), "desktop" (Plasma) or "unknown".
	Session string
}

// RunningApps returns only the app ids (see ScanProcs).
func RunningApps(procRoot string) []uint32 {
	var out []uint32
	for _, r := range ScanProcs(procRoot).Running {
		out = append(out, r.AppID)
	}
	return out
}

// launchWrappers are argv entries between "--" and the real program.
var launchWrappers = map[string]bool{"_v2-entry-point": true, "proton": true, "waitforexitandrun": true, "run": true}

// ScanProcs reads the process table once. Games: the real `reaper` process
// (argv[0] basename "reaper", argv `SteamLaunch AppId=<n>`); the launch client
// and supervisor carry the same text inside their own argv and are skipped by
// the argv[0] rule (G15 discovery §7). Steam starts each game under exactly one
// reaper (ADR-0004 Tier A, ADR-0008 D1).
func ScanProcs(procRoot string) Procs {
	dirs, _ := filepath.Glob(filepath.Join(procRoot, "[0-9]*"))
	byID := map[uint32]Running{}
	gamescope, plasma := false, false
	for _, d := range dirs {
		if comm, err := os.ReadFile(filepath.Join(d, "comm")); err == nil {
			c := strings.TrimSpace(string(comm))
			switch {
			case strings.HasPrefix(c, "gamescope"):
				gamescope = true
			case c == "plasmashell" || c == "kwin_wayland" || c == "kwin_x11":
				plasma = true
			}
		}
		raw, err := os.ReadFile(filepath.Join(d, "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if len(args) < 3 || filepath.Base(args[0]) != "reaper" {
			continue
		}
		launch, sep := false, -1
		var id uint32
		for i, a := range args[1:] {
			if a == "--" {
				sep = i + 1
				break
			}
			if a == "SteamLaunch" {
				launch = true
			}
			if v, ok := strings.CutPrefix(a, "AppId="); ok && launch {
				if n, ok := ParseID(v); ok {
					id = n
				}
			}
		}
		if id == 0 {
			continue
		}
		byID[id] = Running{AppID: id, Exe: exeName(args, sep)}
	}
	out := Procs{Session: "unknown"}
	switch {
	case plasma:
		out.Session = "desktop"
	case gamescope:
		out.Session = "gaming"
	}
	for _, r := range byID {
		out.Running = append(out.Running, r)
	}
	sort.Slice(out.Running, func(i, j int) bool { return out.Running[i].AppID < out.Running[j].AppID })
	return out
}

// exeName picks the launched program after "--", skipping runtime/Proton
// wrappers and options, and returns its basename without extension.
func exeName(args []string, sep int) string {
	if sep < 0 {
		return ""
	}
	for _, a := range args[sep+1:] {
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		base := filepath.Base(strings.ReplaceAll(a, `\`, "/"))
		if launchWrappers[base] {
			continue
		}
		if ext := filepath.Ext(base); ext != "" && len(ext) <= 10 {
			if _, numeric := strconv.Atoi(ext[1:]); numeric != nil { // keep "Game 2.0"-style dots
				base = strings.TrimSuffix(base, ext)
			}
		}
		return capText(base, 64)
	}
	return ""
}
