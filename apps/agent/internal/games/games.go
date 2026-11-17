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
	"strings"
	"sync"
	"syscall"
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

// Launcher starts a Steam URL (production: `steam steam://rungameid/<gameID>`,
// detached). gameID is a decimal string: the appid for a Steam game, the
// 64-bit GameID for a shortcut (ADR-0008 D3).
type Launcher func(gameID string) error

// Config wires the provider.
type Config struct {
	SteamRoot  string
	ProcRoot   string
	Interval   time.Duration // process poll (default 2 s)
	ScanEvery  time.Duration // library rescan (default 20 s)
	LaunchWait time.Duration // how long a launch may take to show a process (default 90 s)
	Launch     Launcher
	// Signal delivers a signal to a process (production: syscall.Kill). The
	// agent runs as Steam's user, so it can signal the games Steam started.
	Signal    func(pid int, sig syscall.Signal) error
	CloseWait time.Duration // graceful close timeout (default 20 s)
	Now       func() time.Time
	Log       *slog.Logger
	// Activity is the optional rich-presence source (ADR-0007, opt-in).
	Activity ActivitySource
}

// ActivitySource reports game activity metrics (Discord IPC proxy).
type ActivitySource interface {
	Metrics() []collect.Metric
	Capabilities() []collect.Capability
}

// Provider is safe for concurrent use.
type Provider struct {
	cfg      Config
	mu       sync.Mutex
	lib      *steam.Library
	revision uint64
	tracker  *steam.Tracker
	events   chan Event
	prevStat map[uint32]string
	scanErr  error
	uiMode   *steam.UIModeWatcher
	dl       *steam.DownloadTracker
	session  string
	changed  chan struct{}
	lastFP   string
}

// New creates a provider. An empty SteamRoot means Steam is not installed.
func New(cfg Config) *Provider {
	if cfg.Interval <= 0 {
		cfg.Interval = 2 * time.Second
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
	if cfg.Signal == nil {
		cfg.Signal = syscall.Kill
	}
	if cfg.CloseWait <= 0 {
		cfg.CloseWait = 20 * time.Second
	}
	p := &Provider{cfg: cfg, tracker: steam.NewTracker(), events: make(chan Event, 64), prevStat: map[uint32]string{}, session: "unknown", changed: make(chan struct{}, 1)}
	if cfg.SteamRoot != "" {
		p.uiMode = steam.NewUIModeWatcher(cfg.SteamRoot)
		p.dl = steam.NewDownloadTracker(cfg.SteamRoot, cfg.Now)
	}
	return p
}

// SteamURLLauncher hands steam://rungameid/<id> to the running Steam client.
// Fixed argv, no shell; the id must be all digits (threat model T11).
func SteamURLLauncher(gameID string) error {
	if _, err := strconv.ParseUint(gameID, 10, 64); err != nil {
		return fmt.Errorf("invalid game id")
	}
	cmd := exec.Command("steam", "steam://rungameid/"+gameID)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // Steam may forward and exit, or stay running
	return nil
}

// Changed signals (coalesced) that game metrics changed and should be sent now
// rather than at the next telemetry tick: game start/stop, UI mode, session
// type, download state, activity text.
func (p *Provider) Changed() <-chan struct{} { return p.changed }

// checkChanged compares a fingerprint of the user-visible game state.
func (p *Provider) checkChanged() {
	p.mu.Lock()
	var b strings.Builder
	if s := p.tracker.Current(); s != nil {
		fmt.Fprintf(&b, "g%d|", s.AppID)
	}
	b.WriteString(p.session + "|")
	if p.uiMode != nil {
		b.WriteString(p.uiMode.Mode() + "|")
	}
	if p.lib != nil {
		for _, g := range p.lib.Games {
			if inProgress(g.Status) || g.Status == "update_required" {
				fmt.Fprintf(&b, "d%d:%s:%d|", g.AppID, g.Status, g.BytesDownloaded)
			}
		}
	}
	if g := p.downloadGame(); g != nil && p.dl != nil {
		// Coarse buckets: push live progress without a message on every poll.
		d := p.dl.Live(g.AppID, snapshot(g))
		fmt.Fprintf(&b, "L%d:%s:%d:%d|", d.AppID, d.Phase, d.Downloaded*1000/max(d.Total, 1), int64(d.RateBps)/(1<<20))
	}
	p.mu.Unlock()
	if p.cfg.Activity != nil {
		for _, m := range p.cfg.Activity.Metrics() {
			fmt.Fprintf(&b, "%v|", m.Value)
		}
	}
	fp := b.String()
	p.mu.Lock()
	diff := fp != p.lastFP
	p.lastFP = fp
	p.mu.Unlock()
	if diff {
		select {
		case p.changed <- struct{}{}:
		default:
		}
	}
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
			p.checkChanged()
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
	procs := steam.ScanProcs(p.cfg.ProcRoot)
	p.mu.Lock()
	changes := p.tracker.Update(procs.Running, p.lib, p.cfg.Now())
	p.session = procs.Session
	if p.uiMode != nil {
		p.uiMode.Poll()
	}
	if p.dl != nil {
		p.dl.Poll(procs.SteamWrite, procs.SteamWriteOK)
	}
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
	if p.cfg.Activity != nil {
		out = append(out, p.cfg.Activity.Metrics()...)
	}
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
	if p.session == "unknown" {
		out = append(out, m("session.type", nil, "", "neither gamescope nor Plasma is running"))
	} else {
		out = append(out, m("session.type", p.session, "", ""))
	}
	if p.uiMode != nil {
		ui := m("steam.ui_mode", nonEmpty(p.uiMode.Mode()), "", "no UI mode line in Steam's log yet")
		if ui.Value != nil {
			ui.Source = "steam gameprocess_log (SSGL UI mode)"
		}
		out = append(out, ui)
	}
	if p.lib == nil {
		return out
	}
	games, updates, shortcuts := p.lib.Counts()
	out = append(out, m("library.games", games, "", ""), m("library.updates", updates, "", ""), m("library.shortcuts", shortcuts, "", ""))
	dl := p.downloadGame()
	if dl == nil {
		out = append(out, m("downloads.status", "idle", "", ""))
		for _, k := range []string{"downloads.app_id", "downloads.name", "downloads.progress", "downloads.rate_bps", "downloads.eta_s"} {
			out = append(out, m(k, nil, "", "no download in progress"))
		}
		return out
	}
	if p.dl != nil {
		live := p.dl.Live(dl.AppID, snapshot(dl))
		return append(out, liveMetrics(m, &live, dl.Name)...)
	}
	out = append(out, m("downloads.status", dl.Status, "", ""), m("downloads.app_id", dl.AppID, "", ""), m("downloads.name", dl.Name, "", ""))
	if dl.BytesToDownload > 0 {
		pct := math.Round(1000*float64(dl.BytesDownloaded)/float64(dl.BytesToDownload)) / 10
		out = append(out, m("downloads.progress", pct, "%", ""))
	} else {
		out = append(out, m("downloads.progress", nil, "%", "size not known yet"))
	}
	// The manifest is written only at events (G15 §13), so byte deltas between
	// writes are not a rate: an earlier version showed 0.02 MB/s and a 29-day
	// ETA on the G15. Without the live tracker, speed and ETA are unknown.
	reason := "no live download data (Steam log not readable)"
	out = append(out, m("downloads.rate_bps", nil, "B/s", reason), m("downloads.eta_s", nil, "s", reason))
	return out
}

// downloadGame picks the download to report: the app Steam's log says it is
// updating, else an in-progress manifest. Callers hold p.mu.
func (p *Provider) downloadGame() *steam.Game {
	if p.lib == nil {
		return nil
	}
	if p.dl != nil {
		if id := p.dl.ActiveApp(); id != 0 {
			if g := p.lib.Find(id); g != nil {
				return g
			}
		}
	}
	var dl *steam.Game
	for i := range p.lib.Games {
		g := &p.lib.Games[i]
		if inProgress(g.Status) && (dl == nil || g.Status == "downloading") {
			dl = g
		}
	}
	return dl
}

func snapshot(g *steam.Game) steam.Snapshot {
	return steam.Snapshot{Downloaded: g.BytesDownloaded, Total: g.BytesToDownload, Staged: g.BytesStaged, StageTotal: g.BytesToStage, Status: g.Status}
}

// liveMetrics reports a download tracked from Steam's log and disk writes
// (G15 discovery §13): exact at events, estimated in between.
func liveMetrics(m func(string, any, string, string) collect.Metric, d *steam.LiveDownload, name string) []collect.Metric {
	src := "steam content_log + disk writes"
	est := func(x collect.Metric) collect.Metric {
		if x.Value != nil {
			x.Source = src
			if d.Estimated {
				x.Confidence = "heuristic"
			}
		}
		return x
	}
	out := []collect.Metric{m("downloads.status", d.Phase, "", ""), m("downloads.app_id", d.AppID, "", ""), m("downloads.name", name, "", "")}
	out[0].Source = src
	pct := math.Round(1000*float64(d.Downloaded)/float64(d.Total)) / 10
	out = append(out, est(m("downloads.progress", pct, "%", "")))
	switch {
	case d.Phase == "paused":
		out = append(out, m("downloads.rate_bps", nil, "B/s", "paused"), m("downloads.eta_s", nil, "s", "paused"))
	case d.RateBps > 0:
		r := m("downloads.rate_bps", int64(d.RateBps), "B/s", "")
		r.Source, r.Confidence = d.RateSource, "heuristic"
		out = append(out, r)
		eta := m("downloads.eta_s", int64(float64(d.Total-min(d.Downloaded, d.Total))/d.RateBps), "s", "")
		eta.Source, eta.Confidence = src, "heuristic"
		out = append(out, eta)
	default:
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
		e := map[string]any{"app_id": g.AppID, "name": g.Name, "kind": string(g.Kind), "status": g.Status}
		if g.Kind != steam.KindShortcut {
			e["size_on_disk"], e["library"] = g.SizeOnDisk, g.Library
		}
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

// IsShortcut reports whether appID is a non-Steam shortcut in the library.
func (p *Provider) IsShortcut(appID uint32) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lib == nil {
		return false
	}
	g := p.lib.Find(appID)
	return g != nil && g.Kind == steam.KindShortcut
}

// Revision is the current library revision (0 = not scanned).
func (p *Provider) Revision() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.revision
}

// Capabilities reports what the game provider can do here.
func (p *Provider) Capabilities() []collect.Capability {
	var extra []collect.Capability
	if p.cfg.Activity != nil {
		extra = p.cfg.Activity.Capabilities()
	}
	return append(p.capabilities(), extra...)
}

func (p *Provider) capabilities() []collect.Capability {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cfg.SteamRoot == "" {
		reason := "Steam installation not found"
		return []collect.Capability{
			{ID: "game.library", Status: "unavailable", Tier: "files", Source: "appmanifest", Reason: reason},
			{ID: "game.current", Status: "unavailable", Tier: "files", Source: "process table", Reason: reason},
			{ID: "downloads.status", Status: "unavailable", Tier: "files", Source: "appmanifest", Reason: reason},
			{ID: "game.launch", Status: "unavailable", Tier: "steam_url", Source: "steam://rungameid", Reason: reason},
			{ID: "game.shortcuts", Status: "unavailable", Tier: "files", Source: "shortcuts.vdf", Reason: reason},
			{ID: "game.launch_shortcut", Status: "unavailable", Tier: "steam_url", Source: "steam://rungameid", Reason: reason},
			{ID: "steam.ui_mode", Status: "unavailable", Tier: "files", Source: "gameprocess_log", Reason: reason},
		}
	}
	lib := collect.Capability{ID: "game.library", Status: "available", Tier: "files", Source: "libraryfolders.vdf + appmanifest", Privilege: "none"}
	if p.scanErr != nil {
		lib.Status, lib.Reason = "unavailable", p.scanErr.Error()
	}
	sc := collect.Capability{ID: "game.shortcuts", Status: "available", Tier: "files", Source: "shortcuts.vdf (appid, AppName, LastPlayTime, IsHidden only)", Privilege: "none"}
	if p.lib != nil && p.lib.ShortcutsErr != nil {
		sc.Status, sc.Reason = "unavailable", p.lib.ShortcutsErr.Error()
	}
	return []collect.Capability{
		lib,
		{ID: "game.current", Status: "available", Tier: "files", Source: "reaper SteamLaunch process", Privilege: "none"},
		{ID: "downloads.status", Status: "available", Tier: "files", Source: "appmanifest StateFlags/Bytes*", Privilege: "none"},
		{ID: "game.launch", Status: "available", Tier: "steam_url", Source: "steam://rungameid", Privilege: "user"},
		sc,
		{ID: "game.launch_shortcut", Status: "available", Tier: "steam_url", Source: "steam://rungameid (64-bit GameID)", Privilege: "user"},
		{ID: "steam.ui_mode", Status: "available", Tier: "files", Source: "gameprocess_log (UI mode lines only)", Privilege: "none"},
		{ID: "session.type", Status: "available", Tier: "files", Source: "process table (gamescope / Plasma)", Privilege: "none"},
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
		case g.Kind == steam.KindTool:
			return reject(cmd, "invalid_args", g.Name+" is a tool, not a game"), nil
		case g.Status != "installed":
			return reject(cmd, "unavailable", g.Name+" is not ready ("+g.Status+")"), nil
		}
		if s := p.current(); s != nil && s.AppID == args.AppID {
			return dispatch.Result{CommandID: cmd.CommandID, State: "confirmed", Result: map[string]any{"app_id": args.AppID, "already_running": true}}, nil
		}
		gameID := strconv.FormatUint(uint64(args.AppID), 10)
		if g.Kind == steam.KindShortcut {
			gameID = steam.ShortcutGameID(args.AppID)
		}
		if err := p.cfg.Launch(gameID); err != nil {
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

// CloseHandler implements close_game (game_management tier). Steam has no
// documented close API, so the agent signals the game's own process tree:
// everything under the game's `reaper` (never the Steam client).
//   - graceful: SIGTERM, then wait up to CloseWait for the reaper to exit;
//   - force: SIGKILL, only with confirmed_by_user (spec 08: confirmation for
//     force actions).
func (p *Provider) CloseHandler() dispatch.Handler {
	return func(ctx context.Context, cmd dispatch.Command) (dispatch.Result, func(context.Context) dispatch.Result) {
		var args struct {
			AppID uint32 `json:"app_id"`
			Force bool   `json:"force"`
		}
		if err := jsonUnmarshal(cmd.Args, &args); err != nil || args.AppID == 0 {
			return reject(cmd, "invalid_args", "app_id required"), nil
		}
		if args.Force && !cmd.ConfirmedByUser {
			return reject(cmd, "confirmation_required", "force close needs confirmation in Home Assistant"), nil
		}
		var reaper int
		for _, r := range steam.ScanProcs(p.cfg.ProcRoot).Running {
			if r.AppID == args.AppID {
				reaper = r.PID
			}
		}
		if reaper <= 1 {
			return reject(cmd, "unavailable", "that game is not running"), nil
		}
		sig, how := syscall.SIGTERM, "close"
		if args.Force {
			sig, how = syscall.SIGKILL, "force close"
		}
		signalled := 0
		pids := steam.Descendants(p.cfg.ProcRoot, reaper)
		for i := len(pids) - 1; i >= 0; i-- { // children first
			if pids[i] > 1 && p.cfg.Signal(pids[i], sig) == nil {
				signalled++
			}
		}
		if signalled == 0 {
			return dispatch.Result{CommandID: cmd.CommandID, State: "failed", Error: &dispatch.Error{Code: "internal", Message: "could not signal the game"}}, nil
		}
		p.cfg.Log.Info("closing game", "app_id", args.AppID, "how", how, "processes", signalled)
		dispatched := dispatch.Result{CommandID: cmd.CommandID, State: "dispatched", Result: map[string]any{"app_id": args.AppID, "force": args.Force}}
		return dispatched, func(ctx context.Context) dispatch.Result {
			wait := p.cfg.CloseWait
			if args.Force {
				wait = 10 * time.Second
			}
			deadline := p.cfg.Now().Add(wait)
			for p.cfg.Now().Before(deadline) {
				p.poll()
				if !p.running(args.AppID) {
					return dispatch.Result{CommandID: cmd.CommandID, State: "confirmed", Result: map[string]any{"app_id": args.AppID, "force": args.Force}}
				}
				select {
				case <-ctx.Done():
					return dispatch.Result{CommandID: cmd.CommandID, State: "failed", Error: &dispatch.Error{Code: "internal", Message: "agent stopping"}}
				case <-time.After(500 * time.Millisecond):
				}
			}
			msg := "the game did not close within " + wait.String()
			if !args.Force {
				msg += "; use force close"
			}
			return dispatch.Result{CommandID: cmd.CommandID, State: "failed", Error: &dispatch.Error{Code: "unavailable", Message: msg, Retryable: true}}
		}
	}
}

func (p *Provider) running(appID uint32) bool {
	for _, r := range steam.ScanProcs(p.cfg.ProcRoot).Running {
		if r.AppID == appID {
			return true
		}
	}
	return false
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
