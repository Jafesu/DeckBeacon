package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/collect"
	"github.com/jafesu/deckbeacon/apps/agent/internal/dispatch"
	"github.com/jafesu/deckbeacon/apps/agent/internal/fsutil"
	"github.com/jafesu/deckbeacon/apps/agent/internal/release"
	"github.com/jafesu/deckbeacon/packages/protocol"

	"crypto/ed25519"
)

// DefaultReleasesURL lists the monorepo's releases (agent/vX.Y.Z tags).
const DefaultReleasesURL = "https://api.github.com/repos/Jafesu/DeckBeacon/releases?per_page=50"

// States reported as agent.update.state.
const (
	StateDisabled    = "disabled"
	StateUnavailable = "unavailable" // no trusted key in this build
	StateUnknown     = "unknown"     // not checked yet
	StateCheckFailed = "check_failed"
	StateUpToDate    = "up_to_date"
	StateAvailable   = "available"
	StateWaiting     = "waiting" // approved, waiting for a safe window
	StateDownloading = "downloading"
	StateInstalling  = "installing"
	StateFailed      = "failed"
)

// Event is an update event for the durable queue.
type Event struct {
	Type string
	Data map[string]any
}

// Config wires the client.
type Config struct {
	Layout        Layout
	StateDir      string // approval + result files
	ConfigDir     string // updates.json (on/off)
	Current       string // running version (buildinfo.Version)
	CurrentExe    string // running binary (rollback target when adopting)
	GOARCH        string
	ReleasesURL   string
	HTTP          *http.Client
	Keys          []ed25519.PublicKey
	CheckEvery    time.Duration // default 6 h
	Tick          time.Duration // approval/safe-window loop, default 30 s
	Integration   func() string // connected HA integration version ("" = unknown)
	Busy          func() (bool, string)
	Connected     func() bool
	StartUpdater  func(ctx context.Context, exe string, args []string) error // runs `<exe> update apply …` independently
	Now           func() time.Time
	Log           *slog.Logger
	ApprovalValid time.Duration // default 7 days
}

type candidate struct {
	manifest *release.Manifest
	assetURL string
}

// Client is safe for concurrent use.
type Client struct {
	cfg       Config
	mu        sync.Mutex
	state     string
	reason    string
	channel   string
	latest    *candidate
	announced string
	checked   time.Time
	events    chan Event
	wake      chan struct{}
	// handedOver is set once an updater has been started: this process is
	// about to be replaced and must never start a second one (found on the
	// G15: a second updater undid the first one's rollback).
	handedOver bool
}

type approval struct {
	Version    string    `json:"version"`
	ApprovedAt time.Time `json:"approved_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Result is written by the updater for the next agent process to report.
type Result struct {
	From    string    `json:"from"`
	To      string    `json:"to"`
	Outcome string    `json:"outcome"` // completed | rolled_back | failed
	Reason  string    `json:"reason,omitempty"`
	At      time.Time `json:"at"`
}

// New creates the client.
func New(cfg Config) *Client {
	if cfg.CheckEvery <= 0 {
		cfg.CheckEvery = 6 * time.Hour
	}
	if cfg.Tick <= 0 {
		cfg.Tick = 30 * time.Second
	}
	if cfg.ApprovalValid <= 0 {
		cfg.ApprovalValid = 7 * 24 * time.Hour
	}
	if cfg.ReleasesURL == "" {
		cfg.ReleasesURL = DefaultReleasesURL
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 60 * time.Second}
	}
	if cfg.GOARCH == "" {
		cfg.GOARCH = runtime.GOARCH
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Busy == nil {
		cfg.Busy = func() (bool, string) { return false, "" }
	}
	if cfg.Integration == nil {
		cfg.Integration = func() string { return "" }
	}
	if cfg.Connected == nil {
		cfg.Connected = func() bool { return false }
	}
	c := &Client{cfg: cfg, state: StateUnknown, channel: "stable", events: make(chan Event, 16), wake: make(chan struct{}, 1)}
	if ch := c.readChannel(); ch != "" {
		c.channel = ch
	}
	return c
}

// Events delivers update events for the durable queue.
func (c *Client) Events() <-chan Event { return c.events }

func (c *Client) emit(typ string, data map[string]any) {
	select {
	case c.events <- Event{Type: typ, Data: data}:
	default:
		c.cfg.Log.Warn("update event dropped", "type", typ)
	}
}

// State returns the current state and its reason.
func (c *Client) State() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state, c.reason
}

func (c *Client) setState(state, reason string) {
	c.mu.Lock()
	c.state, c.reason = state, reason
	c.mu.Unlock()
}

// Enabled reports whether managed updates are on (config/updates.json).
func (c *Client) Enabled() bool {
	var cfg struct {
		Enabled *bool `json:"enabled"`
	}
	if b, err := os.ReadFile(filepath.Join(c.cfg.ConfigDir, "updates.json")); err == nil && json.Unmarshal(b, &cfg) == nil && cfg.Enabled != nil {
		return *cfg.Enabled
	}
	return true
}

// SetEnabled writes config/updates.json (`deckbeacon update on|off`).
func SetEnabled(configDir string, on bool) error {
	b, _ := json.Marshal(map[string]bool{"enabled": on})
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(configDir, "updates.json"), append(b, '\n'), 0o600)
}

// SetChannel applies the channel from HA's welcome policy (persisted, so the
// next offline check uses it too).
func (c *Client) SetChannel(ch string) {
	valid := false
	for _, x := range release.Channels {
		valid = valid || x == ch
	}
	if !valid {
		return
	}
	c.mu.Lock()
	changed := ch != c.channel
	c.channel = ch
	c.mu.Unlock()
	if changed {
		_ = fsutil.WriteFileAtomic(filepath.Join(c.cfg.StateDir, "update-channel"), []byte(ch+"\n"), 0o600)
		c.Wake()
	}
}

func (c *Client) readChannel() string {
	b, err := os.ReadFile(filepath.Join(c.cfg.StateDir, "update-channel"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Wake triggers a check soon (channel change, reconnect).
func (c *Client) Wake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func currentVersion(s string) release.Version {
	v, err := release.ParseVersion(s)
	if err != nil { // development build ("0.0.0-dev"): every release is newer
		return release.Version{}
	}
	return v
}

type ghRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (c *Client) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://127.0.0.1") {
		return nil, fmt.Errorf("update: refusing non-HTTPS URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "deckbeacon-agent")
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: %s returned %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("update: response too large")
	}
	return b, nil
}

// Check discovers the newest verified, compatible release for the channel.
func (c *Client) Check(ctx context.Context) error {
	if !c.Enabled() {
		c.setState(StateDisabled, "turned off on the device (deckbeacon update on)")
		return nil
	}
	if len(c.cfg.Keys) == 0 {
		c.setState(StateUnavailable, "this build trusts no release key")
		return nil
	}
	c.mu.Lock()
	channel := c.channel
	c.mu.Unlock()
	body, err := c.get(ctx, c.cfg.ReleasesURL, 4<<20)
	if err != nil {
		c.setState(StateCheckFailed, err.Error())
		return err
	}
	var rels []ghRelease
	if err := json.Unmarshal(body, &rels); err != nil {
		c.setState(StateCheckFailed, "unreadable release list")
		return err
	}
	cur := currentVersion(c.cfg.Current)
	type opt struct {
		v   release.Version
		rel ghRelease
	}
	var opts []opt
	for _, r := range rels {
		tag, ok := strings.CutPrefix(r.TagName, "agent/v")
		if !ok || r.Draft {
			continue
		}
		v, err := release.ParseVersion(tag)
		if err != nil || !release.Accepts(channel, v) || v.Compare(cur) <= 0 {
			continue
		}
		opts = append(opts, opt{v, r})
	}
	sort.Slice(opts, func(i, j int) bool { return opts[i].v.Compare(opts[j].v) > 0 })
	var lastErr error
	for _, o := range opts { // newest first; skip releases that fail verification or compatibility
		cand, err := c.verify(ctx, o.rel)
		if err != nil {
			lastErr = err
			c.cfg.Log.Warn("skipping release", "tag", o.rel.TagName, "err", err)
			continue
		}
		c.mu.Lock()
		c.latest = cand
		c.checked = c.cfg.Now()
		newVersion := c.announced != cand.manifest.Version
		c.announced = cand.manifest.Version
		c.mu.Unlock()
		if a, ok := c.approval(); ok && a.Version == cand.manifest.Version {
			c.setState(StateWaiting, "approved")
		} else {
			c.setState(StateAvailable, "")
		}
		if newVersion {
			c.emit("agent_update_available", map[string]any{"version": cand.manifest.Version, "current": c.cfg.Current, "channel": channel, "notes_url": cand.manifest.NotesURL})
		}
		return nil
	}
	c.mu.Lock()
	c.latest = nil
	c.checked = c.cfg.Now()
	c.mu.Unlock()
	if lastErr != nil {
		c.setState(StateCheckFailed, lastErr.Error())
		return lastErr
	}
	c.setState(StateUpToDate, "")
	return nil
}

func (c *Client) verify(ctx context.Context, r ghRelease) (*candidate, error) {
	urls := map[string]string{}
	for _, a := range r.Assets {
		urls[a.Name] = a.URL
	}
	if urls["release.json"] == "" || urls["release.json.sig"] == "" {
		return nil, errors.New("release has no signed manifest")
	}
	man, err := c.get(ctx, urls["release.json"], 64<<10)
	if err != nil {
		return nil, err
	}
	sig, err := c.get(ctx, urls["release.json.sig"], 1<<10)
	if err != nil {
		return nil, err
	}
	m, err := release.Verify(man, sig, c.cfg.Keys)
	if err != nil {
		return nil, err
	}
	if "agent/v"+m.Version != r.TagName {
		return nil, fmt.Errorf("manifest version %s does not match tag %s", m.Version, r.TagName)
	}
	if m.MinProtocol > protocol.Version {
		return nil, fmt.Errorf("needs protocol %d (this agent speaks %d)", m.MinProtocol, protocol.Version)
	}
	if hav := c.cfg.Integration(); m.MinIntegration != "" && hav != "" && !atLeast(hav, m.MinIntegration) {
		return nil, fmt.Errorf("needs Home Assistant integration %s or newer (connected: %s)", m.MinIntegration, hav)
	}
	asset := release.AssetName(c.cfg.GOARCH)
	if _, ok := m.Assets[asset]; !ok || urls[asset] == "" {
		return nil, fmt.Errorf("no %s build in this release", asset)
	}
	return &candidate{manifest: m, assetURL: urls[asset]}, nil
}

// atLeast compares dotted numeric versions (integration versions are X.Y.Z).
func atLeast(have, want string) bool {
	h, errH := release.ParseVersion(have)
	w, errW := release.ParseVersion(want)
	if errH != nil || errW != nil {
		return false
	}
	return h.Compare(w) >= 0
}

func (c *Client) approval() (approval, bool) {
	var a approval
	b, err := os.ReadFile(filepath.Join(c.cfg.StateDir, "update-approval.json"))
	if err != nil || json.Unmarshal(b, &a) != nil {
		return a, false
	}
	if c.cfg.Now().After(a.ExpiresAt) {
		_ = os.Remove(filepath.Join(c.cfg.StateDir, "update-approval.json"))
		return a, false
	}
	return a, true
}

func (c *Client) clearApproval() {
	_ = os.Remove(filepath.Join(c.cfg.StateDir, "update-approval.json"))
}

// Approve records the user's approval for exactly the available version.
func (c *Client) Approve(version string) error {
	c.mu.Lock()
	cand := c.latest
	c.mu.Unlock()
	if cand == nil || cand.manifest.Version != version {
		return fmt.Errorf("version %s is not the available update", version)
	}
	now := c.cfg.Now()
	b, _ := json.Marshal(approval{Version: version, ApprovedAt: now.UTC(), ExpiresAt: now.Add(c.cfg.ApprovalValid).UTC()})
	if err := fsutil.WriteFileAtomic(filepath.Join(c.cfg.StateDir, "update-approval.json"), b, 0o600); err != nil {
		return err
	}
	c.setState(StateWaiting, "approved")
	c.Wake()
	return nil
}

// ApproveHandler implements approve_agent_update {version} (tier "updates").
func (c *Client) ApproveHandler() dispatch.Handler {
	return func(ctx context.Context, cmd dispatch.Command) (dispatch.Result, func(context.Context) dispatch.Result) {
		var args struct {
			Version string `json:"version"`
		}
		raw := cmd.Args
		if len(raw) == 0 {
			raw = json.RawMessage("{}")
		}
		if json.Unmarshal(raw, &args) != nil || args.Version == "" {
			return dispatch.Result{CommandID: cmd.CommandID, State: "rejected", Error: &dispatch.Error{Code: "invalid_args", Message: "version required"}}, nil
		}
		if !cmd.ConfirmedByUser {
			return dispatch.Result{CommandID: cmd.CommandID, State: "rejected", Error: &dispatch.Error{Code: "confirmation_required", Message: "an update needs the user's approval in Home Assistant"}}, nil
		}
		if !c.Enabled() {
			return dispatch.Result{CommandID: cmd.CommandID, State: "rejected", Error: &dispatch.Error{Code: "disabled", Message: "updates are turned off on the device"}}, nil
		}
		if err := c.Approve(args.Version); err != nil {
			return dispatch.Result{CommandID: cmd.CommandID, State: "rejected", Error: &dispatch.Error{Code: "unavailable", Message: err.Error()}}, nil
		}
		return dispatch.Result{CommandID: cmd.CommandID, State: "confirmed", Result: map[string]any{"version": args.Version, "state": StateWaiting}}, nil
	}
}

// ReportResult reads the updater's result file (left by the previous
// process) and emits the matching event once.
func (c *Client) ReportResult() {
	p := filepath.Join(c.cfg.StateDir, "update-result.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	_ = os.Remove(p)
	var r Result
	if json.Unmarshal(b, &r) != nil {
		return
	}
	c.clearApproval() // never retry an attempted version automatically
	data := map[string]any{"from": r.From, "to": r.To}
	switch r.Outcome {
	case "completed":
		c.emit("agent_update_completed", data)
	default:
		data["rolled_back"] = r.Outcome == "rolled_back"
		data["reason"] = r.Reason
		c.emit("agent_update_failed", data)
		c.setState(StateFailed, r.Reason)
	}
}

// Run checks periodically and applies an approved update in a safe window.
func (c *Client) Run(ctx context.Context) {
	c.ReportResult()
	st, _ := c.State()
	failed := st == StateFailed
	check := time.NewTimer(30 * time.Second)
	defer check.Stop()
	tick := time.NewTicker(c.cfg.Tick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-check.C:
			if err := c.Check(ctx); err != nil {
				c.cfg.Log.Info("update check failed", "err", err)
			}
			if failed { // keep showing the last failure until something new appears
				if st, reason := c.State(); st == StateUpToDate {
					c.setState(StateFailed, reason)
				}
				failed = false
			}
			check.Reset(c.cfg.CheckEvery)
		case <-c.wake:
			_ = c.Check(ctx)
			c.step(ctx)
		case <-tick.C:
			c.step(ctx)
		}
	}
}

// step applies an approved update when it is safe.
func (c *Client) step(ctx context.Context) {
	c.mu.Lock()
	handedOver := c.handedOver
	c.mu.Unlock()
	if handedOver {
		return
	}
	a, ok := c.approval()
	if !ok || !c.Enabled() {
		return
	}
	c.mu.Lock()
	cand := c.latest
	c.mu.Unlock()
	if cand == nil || cand.manifest.Version != a.Version {
		return // superseded or not verified this session yet: next check decides
	}
	if busy, why := c.cfg.Busy(); busy {
		c.setState(StateWaiting, why)
		return
	}
	if err := c.apply(ctx, cand); err != nil {
		c.cfg.Log.Warn("update failed before switching", "version", a.Version, "err", err)
		c.clearApproval()
		c.setState(StateFailed, err.Error())
		c.emit("agent_update_failed", map[string]any{"from": c.cfg.Current, "to": a.Version, "rolled_back": false, "reason": err.Error()})
	}
}

// apply stages and verifies the binary, then hands over to the updater.
func (c *Client) apply(ctx context.Context, cand *candidate) error {
	m := cand.manifest
	asset := m.Assets[release.AssetName(c.cfg.GOARCH)]
	l := c.cfg.Layout
	if err := os.MkdirAll(filepath.Join(l.Dir, "versions"), 0o755); err != nil {
		return err
	}
	if free := freeBytes(l.Dir); free >= 0 && free < 3*asset.Size {
		return fmt.Errorf("not enough disk space (%d MB free)", free>>20)
	}
	c.setState(StateDownloading, "")
	c.emit("agent_update_started", map[string]any{"from": c.cfg.Current, "to": m.Version})
	if !l.Has(m.Version) {
		tmp := filepath.Join(l.Dir, "versions", ".download-"+m.Version)
		if err := c.download(ctx, cand.assetURL, tmp, asset.Size); err != nil {
			return err
		}
		f, err := os.Open(tmp)
		if err != nil {
			return err
		}
		err = m.CheckAsset(release.AssetName(c.cfg.GOARCH), f)
		f.Close()
		if err != nil {
			_ = os.Remove(tmp)
			return err
		}
		err = l.install(tmp, m.Version)
		_ = os.Remove(tmp)
		if err != nil {
			return err
		}
	}
	// The new binary must run and report the expected version.
	out, err := exec.CommandContext(ctx, l.VersionBin(m.Version), "version", "--json").Output()
	var info struct {
		Version string `json:"version"`
	}
	if err != nil || json.Unmarshal(out, &info) != nil || info.Version != m.Version {
		return fmt.Errorf("the new binary does not run or reports the wrong version")
	}
	// The updater (and with it the rollback) always runs from the known-good
	// running version's own file, never through bin/deckbeacon, which the
	// switch repoints.
	exe := c.cfg.CurrentExe
	args := []string{"update", "apply", "--to", m.Version, "--from", c.cfg.Current}
	if c.cfg.Connected() {
		args = append(args, "--require-connected")
	}
	if l.Managed() {
		if !l.Has(c.cfg.Current) {
			return fmt.Errorf("the running version %s is not in the managed install", c.cfg.Current)
		}
		exe = l.VersionBin(c.cfg.Current)
	} else {
		args = append(args, "--adopt", c.cfg.CurrentExe)
	}
	c.setState(StateInstalling, "")
	if err := c.cfg.StartUpdater(ctx, exe, args); err != nil {
		return err
	}
	c.mu.Lock()
	c.handedOver = true
	c.mu.Unlock()
	c.clearApproval() // attempted: never retried automatically, whatever happens next
	return nil
}

func (c *Client) download(ctx context.Context, url, dst string, size int64) error {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://127.0.0.1") {
		return errors.New("update: refusing non-HTTPS download")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "deckbeacon-agent")
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", resp.Status)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != size {
		err = fmt.Errorf("download: got %d bytes, expected %d", n, size)
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return err
}

func freeBytes(dir string) int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

// Metrics reports update state (agent.update.*).
func (c *Client) Metrics() []collect.Metric {
	c.mu.Lock()
	defer c.mu.Unlock()
	at := c.cfg.Now().UTC().Truncate(time.Second)
	m := func(k string, v any, reason string) collect.Metric {
		x := collect.Metric{Key: k, Value: v, SampledAt: at, Source: "github releases (signed)", Confidence: "confirmed"}
		if v == nil {
			x.Confidence, x.Reason = "", reason
		}
		return x
	}
	var latest, notes any
	if c.latest != nil {
		latest, notes = c.latest.manifest.Version, nilIfEmpty(c.latest.manifest.NotesURL)
	} else if c.state == StateUpToDate {
		latest = c.cfg.Current
	}
	out := []collect.Metric{
		m("agent.update.state", c.state, ""),
		m("agent.update.channel", c.channel, ""),
		m("agent.update.latest", latest, "no update information yet"),
		m("agent.update.notes_url", notes, "no release notes"),
		m("agent.update.reason", nilIfEmpty(c.reason), "no detail"),
	}
	return out
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Capabilities reports managed updates.
func (c *Client) Capabilities() []collect.Capability {
	cp := collect.Capability{ID: "agent.update", Status: "available", Tier: "system", Source: "signed GitHub releases (ADR-0010)", Privilege: "none"}
	switch {
	case !c.Enabled():
		cp.Status, cp.Reason = "disabled", "turned off on the device"
	case len(c.cfg.Keys) == 0:
		cp.Status, cp.Reason = "unavailable", "this build trusts no release key"
	}
	return []collect.Capability{cp}
}
