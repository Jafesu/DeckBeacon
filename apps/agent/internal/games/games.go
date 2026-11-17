// Package games turns the read-only Steam library and process table into
// telemetry, durable events, library snapshots and the launch command (M6).
package games

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/collect"
	"github.com/jafesu/deckbeacon/apps/agent/internal/dispatch"
	"github.com/jafesu/deckbeacon/apps/agent/internal/steam"
)

// Event is a game-related protocol event to queue.
type Event struct {
	Type string
	Data map[string]any
}

// Launcher starts a Steam URL (production: `steam steam://rungameid/<id>`, detached).
type Launcher func(appID uint32) error

// Config wires the provider.
type Config struct {
	SteamRoot  string
	ProcRoot   string
	Interval   time.Duration // process poll (default 5 s)
	ScanEvery  time.Duration // library rescan (default 20 s)
	LaunchWait time.Duration // how long a launch may take to show a process (default 90 s)
	Launch     Launcher
	Now        func() time.Time
	Log        *slog.Logger
}

// Provider is safe for concurrent use.
type Provider struct {
	cfg      Config
	mu       sync.Mutex
	lib      *steam.Library
	revision uint64
	tracker  *steam.Tracker
	rate     steam.DownloadRate
	events   chan Event
	prevStat map[uint32]string
	scanErr  error
}

// New creates a provider. An empty SteamRoot means Steam is not installed.
func New(cfg Config) *Provider {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}
	if cfg.ScanEvery <= 0 {
		cfg.ScanEvery = 20 * time.Second
	}
	if cfg.LaunchWait <= 0 {
		cfg.LaunchWait = 90 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Launch == nil {
		cfg.Launch = SteamURLLauncher
	}
	return &Provider{cfg: cfg, tracker: steam.NewTracker(), events: make(chan Event, 64), prevStat: map[uint32]string{}}
}

// SteamURLLauncher hands steam://rungameid/<id> to the running Steam client.
// Fixed argv, no shell; the id is a validated uint32 (threat model T11).
func SteamURLLauncher(appID uint32) error {
	cmd := exec.Command("steam", "steam://rungameid/"+strconv.FormatUint(uint64(appID), 10))
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // Steam may forward and exit, or stay running
	return nil
}

// Events delivers game events for the durable queue.
func (p *Provider) Events() <-chan Event { return p.events }

func (p *Provider) emit(typ string, data map[string]any) {
	select {
	case p.events <- Event{Type: typ, Data: data}:
	default:
		p.cfg.Log.Warn("game event dropped (queue full)", "type", typ)
	}
}

// Run polls until ctx is done.
func (p *Provider) Run(ctx context.Context) {
	p.scan(true)
	p.poll()
	tick := time.NewTicker(p.cfg.Interval)
	defer tick.Stop()
	lastScan := p.cfg.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if p.cfg.Now().Sub(lastScan) >= p.cfg.ScanEvery {
				p.scan(false)
				lastScan = p.cfg.Now()
			}
			p.poll()
		}
	}
}

// Rescan forces a library scan (refresh_library).
func (p *Provider) Rescan() { p.scan(false) }

func (p *Provider) scan(initial bool) {
	if p.cfg.SteamRoot == "" {
		return
	}
	lib, err := steam.Scan(p.cfg.SteamRoot)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scanErr = err
	if err != nil {
		return
	}
	changed := p.lib == nil || !sameLibrary(p.lib, lib)
	for _, g := range lib.Games {
		if g.Kind != steam.KindGame {
			continue
		}
		prev, known := p.prevStat[g.AppID]
		p.prevStat[g.AppID] = g.Status
		if initial || !known || prev == g.Status {
			continue
		}
		data := map[string]any{"app_id": g.AppID, "name": g.Name}
		switch {
		case inProgress(g.Status) && !inProgress(prev):
			p.emit("download_started", data)
		case g.Status == "installed" && inProgress(prev):
			// Only once Steam reports the app fully installed (spec 06).
			p.emit("download_completed", data)
		case g.Status == "update_required" && prev == "installed":
			p.emit("update_available", data)
		}
	}
	if changed {
		p.lib = lib
		p.revision++
	}
}

func inProgress(s string) bool {
	return s == "downloading" || s == "installing" || s == "updating" || s == "paused" || s == "verifying"
}

func sameLibrary(a, b *steam.Library) bool {
	if len(a.Games) != len(b.Games) {
		return false
	}
	for i := range a.Games {
		x, y := a.Games[i], b.Games[i]
		if x.AppID != y.AppID || x.Status != y.Status || x.SizeOnDisk != y.SizeOnDisk || x.BuildID != y.BuildID || !x.LastPlayed.Equal(y.LastPlayed) || x.Name != y.Name {
			return false
		}
	}
	return true
}

func (p *Provider) poll() {
	running := steam.RunningApps(p.cfg.ProcRoot)
	p.mu.Lock()
	changes := p.tracker.Update(running, p.lib, p.cfg.Now())
	p.mu.Unlock()
	for _, c := range changes {
		data := map[string]any{"app_id": c.Session.AppID, "name": c.Session.Name, "started_at": c.Session.Started.Format(time.RFC3339)}
		if c.Type == "game_stopped" {
			data["duration_s"] = int(c.Duration.Seconds())
		}
		p.emit(c.Type, data)
	}
}

// Metrics reports game, library and download telemetry.
func (p *Provider) Metrics() []collect.Metric {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.cfg.Now()
	at := now.UTC().Truncate(time.Second)
	m := func(key string, v any, unit, reason string) collect.Metric {
		x := collect.Metric{Key: key, Value: v, Unit: unit, SampledAt: at, Source: "steam (local files)", Confidence: "high"}
		if v == nil {
			x.Reason, x.Confidence = reason, ""
		}
		return x
	}
	var out []collect.Metric
	if s := p.tracker.Current(); s != nil {
		out = append(out,
			m("game.current.app_id", s.AppID, "", ""),
			m("game.current.name", nonEmpty(s.Name), "", "name not in library"),
			m("game.session.started_at", s.Started.Format(time.RFC3339), "", ""),
			m("game.session.duration_s", int(s.Duration(now).Seconds()), "s", ""),
		)
	} else {
		for _, k := range []string{"game.current.app_id", "game.current.name", "game.session.started_at", "game.session.duration_s"} {
			out = append(out, m(k, nil, "", "no game running"))
		}
	}
	if p.lib == nil {
		return out
	}
	games, updates := p.lib.Counts()
	out = append(out, m("library.games", games, "", ""), m("library.updates", updates, "", ""))
	var dl *steam.Game
	for i := range p.lib.Games {
		g := &p.lib.Games[i]
		if inProgress(g.Status) && (dl == nil || g.Status == "downloading") {
			dl = g
		}
	}
	if dl == nil {
		out = append(out, m("downloads.status", "idle", "", ""))
		for _, k := range []string{"downloads.app_id", "downloads.name", "downloads.progress", "downloads.rate_bps", "downloads.eta_s"} {
			out = append(out, m(k, nil, "", "no download in progress"))
		}
		p.rate = steam.DownloadRate{}
		return out
	}
	out = append(out, m("downloads.status", dl.Status, "", ""), m("downloads.app_id", dl.AppID, "", ""), m("downloads.name", dl.Name, "", ""))
	if dl.BytesToDownload > 0 {
		pct := math.Round(1000*float64(dl.BytesDownloaded)/float64(dl.BytesToDownload)) / 10
		out = append(out, m("downloads.progress", pct, "%", ""))
	} else {
		out = append(out, m("downloads.progress", nil, "%", "size not known yet"))
	}
	rate := p.rate.Sample(dl.AppID, dl.BytesDownloaded, now)
	if rate > 0 {
		out = append(out, m("downloads.rate_bps", int64(rate), "B/s", ""))
		remaining := float64(dl.BytesToDownload - min(dl.BytesDownloaded, dl.BytesToDownload))
		out = append(out, m("downloads.eta_s", int64(remaining/rate), "s", ""))
	} else {
		out = append(out, m("downloads.rate_bps", nil, "B/s", "measuring"), m("downloads.eta_s", nil, "s", "measuring"))
	}
	return out
}

func nonEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// LibraryPages returns the current snapshot as protocol pages (≤ 100 games).
func (p *Provider) LibraryPages() (revision uint64, pages []map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lib == nil {
		return 0, nil
	}
	var games []map[string]any
	for _, g := range p.lib.Games {
		e := map[string]any{"app_id": g.AppID, "name": g.Name, "kind": string(g.Kind), "status": g.Status, "size_on_disk": g.SizeOnDisk, "library": g.Library}
		if g.BuildID != "" {
			e["build_id"] = g.BuildID
		}
		if !g.LastPlayed.IsZero() {
			e["last_played"] = g.LastPlayed.Format(time.RFC3339)
		}
		games = append(games, e)
	}
	n := max(1, (len(games)+99)/100)
	for i := 0; i < n; i++ {
		chunk := games[i*100 : min(len(games), (i+1)*100)]
		if chunk == nil {
			chunk = []map[string]any{}
		}
		pages = append(pages, map[string]any{"revision": p.revision, "page": i + 1, "pages": n, "games": chunk})
	}
	return p.revision, pages
}

// Revision is the current library revision (0 = not scanned).
func (p *Provider) Revision() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.revision
}

// Capabilities reports what the game provider can do here.
func (p *Provider) Capabilities() []collect.Capability {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cfg.SteamRoot == "" {
		reason := "Steam installation not found"
		return []collect.Capability{
			{ID: "game.library", Status: "unavailable", Tier: "files", Source: "appmanifest", Reason: reason},
			{ID: "game.current", Status: "unavailable", Tier: "files", Source: "process table", Reason: reason},
			{ID: "downloads.status", Status: "unavailable", Tier: "files", Source: "appmanifest", Reason: reason},
			{ID: "game.launch", Status: "unavailable", Tier: "steam_url", Source: "steam://rungameid", Reason: reason},
		}
	}
	lib := collect.Capability{ID: "game.library", Status: "available", Tier: "files", Source: "libraryfolders.vdf + appmanifest", Privilege: "none"}
	if p.scanErr != nil {
		lib.Status, lib.Reason = "unavailable", p.scanErr.Error()
	}
	return []collect.Capability{
		lib,
		{ID: "game.current", Status: "available", Tier: "files", Source: "reaper SteamLaunch process", Privilege: "none"},
		{ID: "downloads.status", Status: "available", Tier: "files", Source: "appmanifest StateFlags/Bytes*", Privilege: "none"},
		{ID: "game.launch", Status: "available", Tier: "steam_url", Source: "steam://rungameid", Privilege: "user"},
	}
}

// ErrNotLaunchable explains why a game cannot be launched.
var ErrNotLaunchable = errors.New("not launchable")

// LaunchHandler implements launch_game: accepted -> dispatched -> confirmed
// once the game's process appears (spec 06), or failed after LaunchWait.
func (p *Provider) LaunchHandler() dispatch.Handler {
	return func(ctx context.Context, cmd dispatch.Command) (dispatch.Result, func(context.Context) dispatch.Result) {
		var args struct {
			AppID uint32 `json:"app_id"`
		}
		if err := jsonUnmarshal(cmd.Args, &args); err != nil || args.AppID == 0 {
			return reject(cmd, "invalid_args", "app_id required"), nil
		}
		p.mu.Lock()
		var g *steam.Game
		if p.lib != nil {
			g = p.lib.Find(args.AppID)
		}
		p.mu.Unlock()
		switch {
		case g == nil:
			return reject(cmd, "invalid_args", fmt.Sprintf("app %d is not installed", args.AppID)), nil
		case g.Kind != steam.KindGame:
			return reject(cmd, "invalid_args", g.Name+" is a tool, not a game"), nil
		case g.Status != "installed":
			return reject(cmd, "unavailable", g.Name+" is not ready ("+g.Status+")"), nil
		}
		if s := p.current(); s != nil && s.AppID == args.AppID {
			return dispatch.Result{CommandID: cmd.CommandID, State: "confirmed", Result: map[string]any{"app_id": args.AppID, "already_running": true}}, nil
		}
		if err := p.cfg.Launch(args.AppID); err != nil {
			return dispatch.Result{CommandID: cmd.CommandID, State: "failed", Error: &dispatch.Error{Code: "internal", Message: "could not hand the launch to Steam: " + err.Error()}}, nil
		}
		dispatched := dispatch.Result{CommandID: cmd.CommandID, State: "dispatched", Result: map[string]any{"app_id": args.AppID}}
		return dispatched, func(ctx context.Context) dispatch.Result {
			deadline := p.cfg.Now().Add(p.cfg.LaunchWait)
			for p.cfg.Now().Before(deadline) {
				p.poll()
				if s := p.current(); s != nil && s.AppID == args.AppID {
					return dispatch.Result{CommandID: cmd.CommandID, State: "confirmed", Result: map[string]any{"app_id": args.AppID}}
				}
				select {
				case <-ctx.Done():
					return dispatch.Result{CommandID: cmd.CommandID, State: "failed", Error: &dispatch.Error{Code: "internal", Message: "agent stopping"}}
				case <-time.After(time.Second):
				}
			}
			return dispatch.Result{CommandID: cmd.CommandID, State: "failed", Error: &dispatch.Error{Code: "unavailable", Message: "the game did not start within " + p.cfg.LaunchWait.String(), Retryable: true}}
		}
	}
}

// RefreshHandler implements refresh_library.
func (p *Provider) RefreshHandler() dispatch.Handler {
	return func(ctx context.Context, cmd dispatch.Command) (dispatch.Result, func(context.Context) dispatch.Result) {
		p.Rescan()
		return dispatch.Result{CommandID: cmd.CommandID, State: "confirmed", Result: map[string]any{"revision": p.Revision()}}, nil
	}
}

func (p *Provider) current() *steam.Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tracker.Current()
}

func reject(cmd dispatch.Command, code, msg string) dispatch.Result {
	return dispatch.Result{CommandID: cmd.CommandID, State: "rejected", Error: &dispatch.Error{Code: code, Message: msg}}
}
