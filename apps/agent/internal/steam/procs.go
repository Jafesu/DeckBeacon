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
	PID   int // the game's reaper process
}

// Procs is one pass over the process table.
type Procs struct {
	Running []Running
	// Session is "gaming" (gamescope, no Plasma), "desktop" (Plasma) or "unknown".
	Session string
	// SteamWrite is write_bytes of the Steam client process (the "steam" with the
	// largest wchar; several processes share the name, G15 §13). It follows a
	// download live. SteamWriteOK is false when Steam is not running.
	SteamWrite   uint64
	SteamWriteOK bool
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
	var bestWchar uint64
	out := Procs{Session: "unknown"}
	for _, d := range dirs {
		if comm, err := os.ReadFile(filepath.Join(d, "comm")); err == nil {
			c := strings.TrimSpace(string(comm))
			switch {
			case c == "steam":
				if wchar, wb, ok := readIO(filepath.Join(d, "io")); ok && (!out.SteamWriteOK || wchar > bestWchar) {
					bestWchar, out.SteamWrite, out.SteamWriteOK = wchar, wb, true
				}
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
		pid, _ := strconv.Atoi(filepath.Base(d))
		byID[id] = Running{AppID: id, Exe: exeName(args, sep), PID: pid}
	}
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

// readIO parses wchar and write_bytes from /proc/<pid>/io (readable for the
// agent's own user only, which is also Steam's user).
func readIO(path string) (wchar, writeBytes uint64, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	var gotW, gotB bool
	for _, l := range strings.Split(string(b), "\n") {
		k, v, found := strings.Cut(l, ":")
		if !found {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "wchar":
			wchar, gotW = n, true
		case "write_bytes":
			writeBytes, gotB = n, true
		}
	}
	return wchar, writeBytes, gotW && gotB
}

// Descendants returns pid and every process below it (by PPid in /proc/*/stat),
// breadth-first and bounded.
func Descendants(procRoot string, pid int) []int {
	dirs, _ := filepath.Glob(filepath.Join(procRoot, "[0-9]*"))
	children := map[int][]int{}
	for _, d := range dirs {
		raw, err := os.ReadFile(filepath.Join(d, "stat"))
		if err != nil {
			continue
		}
		// "pid (comm) state ppid ...": comm may contain spaces/parens, so
		// parse after the last ')'.
		s := string(raw)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+1:])
		if len(f) < 2 {
			continue
		}
		child, err1 := strconv.Atoi(filepath.Base(d))
		ppid, err2 := strconv.Atoi(f[1])
		if err1 == nil && err2 == nil {
			children[ppid] = append(children[ppid], child)
		}
	}
	out := []int{pid}
	seen := map[int]bool{pid: true}
	for i := 0; i < len(out) && len(out) < 4096; i++ {
		for _, c := range children[out[i]] {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}
