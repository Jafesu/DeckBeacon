package steam

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Kind classifies library entries.
type Kind string

const (
	KindGame Kind = "game"
	KindTool Kind = "tool" // Proton, Steam Linux Runtime, redistributables
	// KindShortcut is a non-Steam game added to Steam (shortcuts.vdf, ADR-0008).
	KindShortcut Kind = "shortcut"
)

// knownTools are Valve tool/runtime app ids observed in real libraries
// (G15 discovery 2026-11-17) or documented by Valve.
var knownTools = map[uint32]bool{
	228980:  true, // Steamworks Common Redistributables
	1070560: true, // Steam Linux Runtime 1.0 (scout)
	1391110: true, // Steam Linux Runtime 2.0 (soldier)
	1628350: true, // Steam Linux Runtime 3.0 (sniper)
	4183110: true, // Steam Linux Runtime 4.0
	1493710: true, // Proton Experimental
	2805730: true, // Proton 9.0
	4628710: true, // Proton 11.0
}

var toolPrefixes = []string{"proton ", "proton-", "steam linux runtime", "steamworks common redistributables", "steamvr"}

func classify(id uint32, name string) (Kind, string) {
	if knownTools[id] {
		return KindTool, "confirmed"
	}
	lower := strings.ToLower(name)
	if lower == "proton experimental" {
		return KindTool, "confirmed"
	}
	for _, p := range toolPrefixes {
		if strings.HasPrefix(lower, p) {
			return KindTool, "heuristic"
		}
	}
	return KindGame, "heuristic"
}

// StateFlags bits (community-documented EAppState values; not an official Valve API).
const (
	flagUpdateRequired = 2
	flagFullyInstalled = 4
	flagFilesMissing   = 32
	flagUpdateRunning  = 256
	flagUpdatePaused   = 512
	flagUpdateStarted  = 1024
	flagUninstalling   = 2048
	flagValidating     = 131072
	flagDownloading    = 1048576
	flagStaging        = 2097152
	flagCommitting     = 4194304
)

// Status derives a coarse install/download state from StateFlags.
func Status(flags uint64) string {
	switch {
	case flags&flagUninstalling != 0:
		return "uninstalling"
	case flags&flagValidating != 0:
		return "verifying"
	case flags&flagUpdatePaused != 0:
		return "paused"
	case flags&flagDownloading != 0:
		return "downloading"
	case flags&(flagStaging|flagCommitting) != 0:
		return "installing"
	case flags&(flagUpdateRunning|flagUpdateStarted) != 0:
		return "updating"
	case flags&flagUpdateRequired != 0:
		return "update_required"
	case flags&flagFilesMissing != 0:
		return "files_missing"
	case flags&flagFullyInstalled != 0:
		return "installed"
	default:
		return "unknown"
	}
}

// Game is one library entry (no PII: LastOwner is deliberately not read).
type Game struct {
	AppID           uint32    `json:"app_id"`
	Name            string    `json:"name"`
	Kind            Kind      `json:"kind"`
	KindConfidence  string    `json:"-"`
	Status          string    `json:"status"`
	StateFlags      uint64    `json:"-"`
	SizeOnDisk      uint64    `json:"size_on_disk"`
	BuildID         string    `json:"build_id,omitempty"`
	LastPlayed      time.Time `json:"-"`
	LastUpdated     time.Time `json:"-"`
	Library         string    `json:"library"`
	BytesToDownload uint64    `json:"-"`
	BytesDownloaded uint64    `json:"-"`
	BytesToStage    uint64    `json:"-"`
	BytesStaged     uint64    `json:"-"`
	installDir      string
}

// LibraryFolder is one Steam library location.
type LibraryFolder struct {
	Path      string `json:"-"`
	ID        string `json:"id"` // stable, non-identifying: index in libraryfolders.vdf
	Label     string `json:"label,omitempty"`
	Removable bool   `json:"removable"`
	Present   bool   `json:"present"`
}

// Library is a full scan.
type Library struct {
	Folders []LibraryFolder
	Games   []Game
	// ShortcutsErr is set when a shortcuts.vdf could not be parsed (no path).
	ShortcutsErr error
}

// SteamRoot locates the Steam installation under home (or returns "").
func SteamRoot(home string) string {
	for _, c := range []string{".local/share/Steam", ".steam/steam", ".steam/root"} {
		p := filepath.Join(home, c)
		if st, err := os.Stat(filepath.Join(p, "steamapps")); err == nil && st.IsDir() {
			if r, err := filepath.EvalSymlinks(p); err == nil {
				return r
			}
			return p
		}
	}
	return ""
}

func u64(s string) uint64 { v, _ := strconv.ParseUint(s, 10, 64); return v }

func unix(s string) time.Time {
	v := u64(s)
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(int64(v), 0).UTC()
}

// Scan reads libraryfolders.vdf and every appmanifest (read-only).
func Scan(steamRoot string) (*Library, error) {
	lf, err := ReadVDF(filepath.Join(steamRoot, "steamapps", "libraryfolders.vdf"))
	if err != nil {
		return nil, err
	}
	lib := &Library{}
	root := lf.Get("libraryfolders")
	paths := map[string]bool{}
	if root != nil {
		for _, k := range root.Order {
			n := root.Children[k]
			if !n.IsMap() || n.Str("path") == "" {
				continue
			}
			p := n.Str("path")
			if paths[p] {
				continue
			}
			paths[p] = true
			_, statErr := os.Stat(filepath.Join(p, "steamapps"))
			lib.Folders = append(lib.Folders, LibraryFolder{
				Path: p, ID: "lib" + k, Label: n.Str("label"), Present: statErr == nil,
				Removable: strings.HasPrefix(p, "/run/media/") || strings.HasPrefix(p, "/media/"),
			})
		}
	}
	if len(lib.Folders) == 0 { // older layouts: the Steam root itself
		lib.Folders = append(lib.Folders, LibraryFolder{Path: steamRoot, ID: "lib0", Present: true})
	}
	seen := map[uint32]bool{}
	for _, f := range lib.Folders {
		if !f.Present {
			continue
		}
		manifests, _ := filepath.Glob(filepath.Join(f.Path, "steamapps", "appmanifest_*.acf"))
		sort.Strings(manifests)
		for _, m := range manifests {
			n, err := ReadVDF(m)
			if err != nil {
				continue // a half-written manifest is retried on the next scan
			}
			a := n.Get("appstate")
			if a == nil {
				continue
			}
			id64, err := strconv.ParseUint(a.Str("appid"), 10, 32)
			if err != nil || seen[uint32(id64)] {
				continue
			}
			id := uint32(id64)
			seen[id] = true
			flags := u64(a.Str("stateflags"))
			kind, conf := classify(id, a.Str("name"))
			lib.Games = append(lib.Games, Game{
				AppID: id, Name: a.Str("name"), Kind: kind, KindConfidence: conf, Status: Status(flags), StateFlags: flags,
				SizeOnDisk: u64(a.Str("sizeondisk")), BuildID: a.Str("buildid"),
				LastPlayed: unix(a.Str("lastplayed")), LastUpdated: unix(a.Str("lastupdated")), Library: f.ID,
				BytesToDownload: u64(a.Str("bytestodownload")), BytesDownloaded: u64(a.Str("bytesdownloaded")),
				BytesToStage: u64(a.Str("bytestostage")), BytesStaged: u64(a.Str("bytesstaged")),
				installDir: a.Str("installdir"),
			})
		}
	}
	shortcuts, err := ReadShortcuts(steamRoot)
	lib.ShortcutsErr = err
	for _, sc := range shortcuts {
		if seen[sc.AppID] {
			continue
		}
		seen[sc.AppID] = true
		lib.Games = append(lib.Games, Game{AppID: sc.AppID, Name: sc.Name, Kind: KindShortcut, KindConfidence: "confirmed", Status: "installed", LastPlayed: sc.LastPlayed})
	}
	sort.Slice(lib.Games, func(i, j int) bool { return lib.Games[i].AppID < lib.Games[j].AppID })
	return lib, nil
}

// Find returns the entry for id.
func (l *Library) Find(id uint32) *Game {
	for i := range l.Games {
		if l.Games[i].AppID == id {
			return &l.Games[i]
		}
	}
	return nil
}

// Counts returns installed Steam games (excluding tools and shortcuts),
// entries needing an update, and non-Steam shortcuts.
func (l *Library) Counts() (games, updates, shortcuts int) {
	for _, g := range l.Games {
		if g.Kind == KindShortcut {
			shortcuts++
		}
		if g.Kind != KindGame {
			continue
		}
		games++
		if g.Status == "update_required" || g.Status == "updating" || g.Status == "downloading" || g.Status == "paused" {
			updates++
		}
	}
	return games, updates, shortcuts
}
