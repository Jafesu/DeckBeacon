package steam

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// RunningApps returns app ids of games launched by Steam, from the process table:
// Steam starts each game under `reaper SteamLaunch AppId=<id> -- ...`
// (ADR-0004 Tier A; confidence "high").
func RunningApps(procRoot string) []uint32 {
	dirs, _ := filepath.Glob(filepath.Join(procRoot, "[0-9]*"))
	set := map[uint32]bool{}
	for _, d := range dirs {
		raw, err := os.ReadFile(filepath.Join(d, "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if len(args) < 3 || filepath.Base(args[0]) != "reaper" {
			continue
		}
		launch := false
		for _, a := range args[1:] {
			if a == "--" {
				break
			}
			if a == "SteamLaunch" {
				launch = true
			}
			if v, ok := strings.CutPrefix(a, "AppId="); ok && launch {
				if id, err := strconv.ParseUint(v, 10, 32); err == nil && id > 0 {
					set[uint32(id)] = true
				}
			}
		}
	}
	out := make([]uint32, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
