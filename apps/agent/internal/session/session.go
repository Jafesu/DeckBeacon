// Package session runs the agent's connection lifecycle (spec 03 state machine):
//
//	unpaired → connecting → connected_local | connected_remote → reconnecting → … ; revoked
//
// It dials HA (local URLs first, then remote), runs Noise IK, exchanges
// hello/welcome, then streams capabilities, telemetry and durable events, answers
// commands, keeps a heartbeat, and reconnects with jittered exponential backoff.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jafesu/deckbeacon/apps/agent/internal/channel"
	"github.com/jafesu/deckbeacon/apps/agent/internal/collect"
	"github.com/jafesu/deckbeacon/apps/agent/internal/dispatch"
	"github.com/jafesu/deckbeacon/apps/agent/internal/identity"
	"github.com/jafesu/deckbeacon/apps/agent/internal/lifecycle"
	"github.com/jafesu/deckbeacon/apps/agent/internal/store"
	"github.com/jafesu/deckbeacon/apps/agent/internal/trust"
	"github.com/jafesu/deckbeacon/apps/agent/internal/ulid"
	"github.com/jafesu/deckbeacon/packages/protocol/noise"
)

// Collector provides telemetry and capabilities.
type Collector interface {
	Collect(ctx context.Context) []collect.Metric
	Capabilities(ctx context.Context) []collect.Capability
}

// HelloInfo is static identity sent in hello.
type HelloInfo struct {
	AgentVersion string
	Device       map[string]any // protocol hello.device (device_id filled in by the client)
	BootID       string
	Mode         func() string
}

// Status is the externally visible state.
type Status struct {
	State     string    `json:"state"`
	Transport string    `json:"transport,omitempty"`
	URL       string    `json:"url,omitempty"`
	Since     time.Time `json:"since"`
	LastError string    `json:"last_error,omitempty"`
}

// Config wires the client.
type Config struct {
	Identity          *identity.Identity
	Trust             *trust.Trust
	Store             *store.Store
	Collector         Collector
	Dispatcher        *dispatch.Dispatcher
	Hello             HelloInfo
	TelemetryInterval time.Duration
	LocalProbeEvery   time.Duration // while remote, how often to probe local URLs
	Log               *slog.Logger
	Now               func() time.Time
	Backoff           func(attempt int) time.Duration
	OnStatus          func(Status)
	// Lifecycle delivers sleep/resume/shutdown transitions (nil: none).
	Lifecycle <-chan lifecycle.Event
	// ExtraCapabilities are appended to the collector's capabilities.
	ExtraCapabilities func() []collect.Capability
	// AckWait bounds how long a sleep/shutdown is held for HA's ack (default 3 s).
	AckWait time.Duration
	// Games provides game telemetry and library snapshots (nil: none).
	Games GameSource
	// LocalPolicy filters the tiers HA grants (agent-side enforcement, spec 10).
	LocalPolicy func(granted []string) []string
}

// GameSource is implemented by games.Provider.
type GameSource interface {
	Metrics() []collect.Metric
	LibraryPages() (revision uint64, pages []map[string]any)
	Revision() uint64
}

// ErrRevoked means HA no longer recognises this device.
var ErrRevoked = errors.New("this device has been removed from Home Assistant; pair again with `deckbeacon pair`")

// Client is one agent connection manager.
type Client struct {
	cfg       Config
	serverKey []byte
	mu        sync.Mutex
	status    Status
	lastCaps  []collect.Capability
}

// New validates the configuration.
func New(cfg Config) (*Client, error) {
	key, err := cfg.Trust.ServerKey()
	if err != nil {
		return nil, err
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.TelemetryInterval <= 0 {
		cfg.TelemetryInterval = 30 * time.Second
	}
	if cfg.LocalProbeEvery <= 0 {
		cfg.LocalProbeEvery = 5 * time.Minute
	}
	if cfg.Backoff == nil {
		cfg.Backoff = DefaultBackoff
	}
	if cfg.AckWait <= 0 {
		cfg.AckWait = 3 * time.Second
	}
	return &Client{cfg: cfg, serverKey: key}, nil
}

// DefaultBackoff is exponential 1s..60s with ±20% jitter (no restart storms).
func DefaultBackoff(attempt int) time.Duration {
	d := time.Second << min(attempt, 6)
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	j := 0.8 + 0.4*rand.Float64()
	return time.Duration(float64(d) * j)
}

// Status returns the current status.
func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Client) setStatus(state, transport, url, lastErr string) {
	c.mu.Lock()
	c.status = Status{State: state, Transport: transport, URL: url, Since: c.cfg.Now().UTC(), LastError: lastErr}
	st := c.status
	c.mu.Unlock()
	if c.cfg.OnStatus != nil {
		c.cfg.OnStatus(st)
	}
}

func (c *Client) orderedURLs() []trust.URL {
	var local, remote []trust.URL
	for _, u := range c.cfg.Trust.URLs {
		if u.Remote() {
			remote = append(remote, u)
		} else {
			local = append(local, u)
		}
	}
	return append(local, remote...)
}

// Run connects and reconnects until ctx is cancelled or the device is revoked.
func (c *Client) Run(ctx context.Context) error {
	attempt := 0
	c.setStatus("connecting", "", "", "")
	for {
		var lastErr error
		for _, u := range c.orderedURLs() {
			started := c.cfg.Now()
			err := c.connectOnce(ctx, u)
			if errors.Is(err, ErrRevoked) {
				c.setStatus("revoked", "", "", err.Error())
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, errPreferLocal) || errors.Is(err, errResumed) {
				attempt, lastErr = 0, nil
				break
			}
			if errors.Is(err, errSuspending) {
				c.setStatus("suspended", "", "", "")
				if err := c.awaitResume(ctx, 2*time.Minute); err != nil {
					return err
				}
				attempt, lastErr = 0, nil
				break
			}
			if err != nil {
				lastErr = err
				c.cfg.Log.Warn("connection ended", "url_kind", u.Kind, "err", err)
			}
			if c.cfg.Now().Sub(started) > 60*time.Second {
				attempt = 0 // a stable session resets the backoff
				break
			}
		}
		msg := ""
		if lastErr != nil {
			msg = lastErr.Error()
		}
		c.setStatus("reconnecting", "", "", msg)
		if lastErr == nil && attempt == 0 {
			continue // resumed or switching to local: reconnect now, no backoff
		}
		wait := time.After(c.cfg.Backoff(attempt))
	waitLoop:
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case ev := <-c.cfg.Lifecycle:
				// Offline: record the transition for later delivery and never delay the system.
				c.recordLifecycle(ev)
				ev.Ready()
				if ev.Kind == lifecycle.Resume {
					attempt = 0
					break waitLoop
				}
			case <-wait:
				attempt++
				break waitLoop
			}
		}
	}
}

var (
	errPreferLocal = errors.New("local transport available again")
	errResumed     = errors.New("device resumed from sleep")
	errSuspending  = errors.New("device is going to sleep or shutting down")
)

// awaitResume waits for the resume after an announced sleep (no reconnect
// attempts while the system goes down). If the sleep never happens (cancelled,
// or a shutdown was aborted), the fallback timer reconnects anyway.
func (c *Client) awaitResume(ctx context.Context, fallback time.Duration) error {
	c.cfg.Log.Info("waiting for resume before reconnecting")
	t := time.NewTimer(fallback)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		case ev := <-c.cfg.Lifecycle:
			c.recordLifecycle(ev)
			ev.Ready()
			if ev.Kind == lifecycle.Resume {
				return nil
			}
		}
	}
}

// recordLifecycle queues the protocol event for a transition and returns its seq.
func (c *Client) recordLifecycle(ev lifecycle.Event) uint64 {
	data := map[string]any{"source": ev.Source, "confidence": ev.Confidence}
	typ := map[lifecycle.Kind]string{lifecycle.Sleep: "device_suspending", lifecycle.Shutdown: "device_shutting_down", lifecycle.Resume: "device_resumed"}[ev.Kind]
	if ev.Gap > 0 {
		data["suspended_s"] = int(ev.Gap.Seconds())
	}
	if ev.ShutdownType != "" {
		data["kind"] = ev.ShutdownType
	}
	c.cfg.Log.Info("lifecycle", "event", typ, "source", ev.Source, "kind", ev.ShutdownType)
	return c.appendEvent(typ, data)
}

type helloBody struct {
	AgentVersion string         `json:"agent_version"`
	Protocol     map[string]int `json:"protocol"`
	Device       map[string]any `json:"device"`
	BootID       string         `json:"boot_id"`
	Session      map[string]any `json:"session"`
}

type welcomeBody struct {
	Protocol             int    `json:"protocol"`
	HeartbeatS           int    `json:"heartbeat_s"`
	LastReceivedEventSeq uint64 `json:"last_received_event_seq"`
	IntegrationVersion   string `json:"integration_version"`
	Policy               struct {
		Granted []string `json:"granted"`
	} `json:"policy"`
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Fatal bool `json:"fatal"`
}

func (c *Client) connectOnce(ctx context.Context, u trust.URL) error {
	transport := "local"
	if u.Remote() {
		transport = "remote"
	}
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	ws, err := channel.Dial(dctx, u.URL, channel.PathSession)
	if err != nil {
		cancel()
		return err
	}
	defer ws.CloseNow()
	conn, err := channel.Handshake(dctx, ws, noise.Config{Pattern: noise.IK, Prologue: noise.PrologueSession, Static: c.cfg.Identity.Channel, RemoteStatic: c.serverKey})
	if err != nil {
		cancel()
		return err
	}
	st, _ := c.cfg.Store.Stats()
	dev := map[string]any{}
	for k, v := range c.cfg.Hello.Device {
		dev[k] = v
	}
	dev["device_id"] = c.cfg.Identity.DeviceID
	mode := "unknown"
	if c.cfg.Hello.Mode != nil {
		mode = c.cfg.Hello.Mode()
	}
	if _, err := conn.Send(dctx, "hello", helloBody{
		AgentVersion: c.cfg.Hello.AgentVersion, Protocol: map[string]int{"min": 1, "max": 1}, Device: dev,
		BootID: c.cfg.Hello.BootID, Session: map[string]any{"mode": mode, "transport": transport, "resume_after_event_seq": st.Acked},
	}); err != nil {
		cancel()
		return err
	}
	m, err := conn.Recv(dctx)
	cancel()
	if err != nil {
		return fmt.Errorf("waiting for welcome: %w", err)
	}
	switch m.Type {
	case "error":
		var eb errorBody
		_ = json.Unmarshal(m.Body, &eb)
		if eb.Error.Code == "permission_denied" {
			return ErrRevoked
		}
		return fmt.Errorf("server error %s: %s", eb.Error.Code, eb.Error.Message)
	case "welcome":
	default:
		return fmt.Errorf("expected welcome, got %s", m.Type)
	}
	var w welcomeBody
	if err := json.Unmarshal(m.Body, &w); err != nil {
		return err
	}
	if w.LastReceivedEventSeq > 0 {
		_ = c.cfg.Store.AckThrough(w.LastReceivedEventSeq)
	}
	granted := w.Policy.Granted
	if c.cfg.LocalPolicy != nil {
		granted = c.cfg.LocalPolicy(granted)
	}
	c.cfg.Dispatcher.SetGranted(granted)
	hb := time.Duration(w.HeartbeatS) * time.Second
	c.setStatus("connected_"+transport, transport, u.URL, "")
	c.cfg.Log.Info("connected", "transport", transport, "integration", w.IntegrationVersion)

	caps := c.cfg.Collector.Capabilities(ctx)
	if c.cfg.ExtraCapabilities != nil {
		caps = append(caps, c.cfg.ExtraCapabilities()...)
	}
	if _, err := conn.Send(ctx, "capabilities", map[string]any{"capabilities": caps}); err != nil {
		return err
	}
	if c.lastCaps != nil && !reflect.DeepEqual(c.lastCaps, caps) {
		c.appendEvent("capability_changed", map[string]any{})
	}
	c.lastCaps = caps
	c.appendEvent("connection_changed", map[string]any{"transport": transport})

	rx := make(chan *channel.Message, 16)
	rxErr := make(chan error, 1)
	go func() {
		for {
			m, err := conn.Recv(ctx)
			if err != nil {
				var inv *channel.InvalidMessageError
				if errors.As(err, &inv) && m != nil {
					// Lenient receiver (COMPATIBILITY.md): answer and continue.
					if rerr := c.rejectInvalid(ctx, conn, m); rerr == nil {
						continue
					}
				}
				rxErr <- err
				return
			}
			rx <- m
		}
	}()

	telemetry := time.NewTicker(c.cfg.TelemetryInterval)
	defer telemetry.Stop()
	heartbeat := time.NewTicker(hb)
	defer heartbeat.Stop()
	flush := time.NewTicker(250 * time.Millisecond)
	defer flush.Stop()
	probe := time.NewTicker(c.cfg.LocalProbeEvery)
	defer probe.Stop()
	lastRx := c.cfg.Now()
	var sentThrough uint64
	localOK := 0
	type held struct {
		seq      uint64
		ev       lifecycle.Event
		deadline time.Time
	}
	var holding []held
	// releaseAcked releases held sleep/shutdown transitions once HA has acked
	// their announcement (or the deadline passed). It returns true if one was
	// released: the device is about to sleep, so the session is closed cleanly
	// first and HA sees "offline" at once instead of after a heartbeat timeout.
	releaseAcked := func(force bool) bool {
		st, _ := c.cfg.Store.Stats()
		kept := holding[:0]
		var ready []held
		for _, h := range holding {
			if force || st.Acked >= h.seq || c.cfg.Now().After(h.deadline) {
				ready = append(ready, h)
				continue
			}
			kept = append(kept, h)
		}
		holding = kept
		if len(ready) > 0 && !force {
			// Best effort only: NetworkManager tears Wi-Fi down in parallel
			// (observed on the G15), so the close may never arrive. HA expects the
			// drop after the announcement; never hold the system for the close.
			c.cfg.Log.Info("closing session before sleep/shutdown", "acked", st.Acked >= ready[0].seq)
			go conn.Close(websocket.StatusGoingAway, "suspending")
		}
		for _, h := range ready {
			h.ev.Ready()
		}
		return len(ready) > 0
	}
	defer releaseAcked(true) // never leave the system inhibited

	var sentRevision uint64
	sendLibrary := func() error {
		if c.cfg.Games == nil {
			return nil
		}
		rev, pages := c.cfg.Games.LibraryPages()
		if rev == 0 || rev == sentRevision {
			return nil
		}
		for _, pg := range pages {
			if _, err := conn.Send(ctx, "library", pg); err != nil {
				return err
			}
		}
		sentRevision = rev
		return nil
	}
	sendTelemetry := func() error {
		ms := c.cfg.Collector.Collect(ctx)
		if c.cfg.Games != nil {
			ms = append(ms, c.cfg.Games.Metrics()...)
		}
		if len(ms) == 0 {
			return nil
		}
		for len(ms) > 0 { // telemetry messages carry at most 256 metrics
			n := min(len(ms), 200)
			if _, err := conn.Send(ctx, "telemetry", map[string]any{"metrics": ms[:n]}); err != nil {
				return err
			}
			ms = ms[n:]
		}
		return sendLibrary()
	}
	flushEvents := func() error {
		evs, err := c.cfg.Store.PendingEvents(sentThrough, 50)
		if err != nil {
			return err
		}
		for _, e := range evs {
			// An event is "live" only if it is delivered promptly; anything older is
			// backfill and must not trigger time-sensitive automations (spec 07).
			e.Live = e.Live && c.cfg.Now().Sub(e.OccurredAt) < 30*time.Second
			if _, err := conn.Send(ctx, "event", e); err != nil {
				return err
			}
			sentThrough = e.EventSeq
		}
		return nil
	}
	// Game metrics are pushed as soon as they change (the full telemetry tick
	// is 30 s; owner on the G15: 10–15 s lag was too slow). HA merges by key.
	var gameChanged <-chan struct{}
	if ch, ok := c.cfg.Games.(interface{ Changed() <-chan struct{} }); ok {
		gameChanged = ch.Changed()
	}
	sendGameMetrics := func() error {
		if ms := c.cfg.Games.Metrics(); len(ms) > 0 {
			if _, err := conn.Send(ctx, "telemetry", map[string]any{"metrics": ms[:min(len(ms), 200)]}); err != nil {
				return err
			}
		}
		return sendLibrary()
	}
	if err := sendTelemetry(); err != nil {
		return err
	}
	if err := flushEvents(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusGoingAway, "agent stopping")
			return ctx.Err()
		case err := <-rxErr:
			return err
		case m := <-rx:
			lastRx = c.cfg.Now()
			if err := c.handle(ctx, conn, m); err != nil {
				return err
			}
			if m.Type == "ack" && releaseAcked(false) {
				return errSuspending
			}
		case <-telemetry.C:
			if err := sendTelemetry(); err != nil {
				return err
			}
		case <-gameChanged:
			if err := sendGameMetrics(); err != nil {
				return err
			}
		case r := <-c.cfg.Dispatcher.Late():
			if _, err := conn.Send(ctx, "command_result", r); err != nil {
				return err
			}
		case <-flush.C:
			if err := flushEvents(); err != nil {
				return err
			}
			if releaseAcked(false) {
				return errSuspending
			}
		case ev := <-c.cfg.Lifecycle:
			seq := c.recordLifecycle(ev)
			if ev.Kind == lifecycle.Resume {
				// The old connection is stale after a suspend: reconnect now.
				_ = conn.Close(websocket.StatusGoingAway, "resumed")
				return errResumed
			}
			if err := flushEvents(); err != nil {
				ev.Ready()
				return err
			}
			holding = append(holding, held{seq: seq, ev: ev, deadline: c.cfg.Now().Add(c.cfg.AckWait)})
		case <-heartbeat.C:
			if c.cfg.Now().Sub(lastRx) > 3*hb {
				return errors.New("heartbeat timeout")
			}
			if _, err := conn.Send(ctx, "ping", map[string]any{}); err != nil {
				return err
			}
		case <-probe.C:
			if transport != "remote" {
				continue
			}
			if c.localReachable(ctx) {
				localOK++
			} else {
				localOK = 0
			}
			if localOK >= 2 { // stability threshold before switching back
				_ = conn.Close(websocket.StatusNormalClosure, "switching to local")
				return errPreferLocal
			}
		}
	}
}

func (c *Client) handle(ctx context.Context, conn *channel.Conn, m *channel.Message) error {
	switch m.Type {
	case "ack":
		var a struct {
			EventSeq uint64 `json:"event_seq"`
		}
		_ = json.Unmarshal(m.Body, &a)
		if a.EventSeq > 0 {
			return c.cfg.Store.AckThrough(a.EventSeq)
		}
	case "ping":
		_, err := conn.Send(ctx, "pong", map[string]any{"ref_id": m.ID})
		return err
	case "pong":
	case "command":
		var cmd dispatch.Command
		if err := json.Unmarshal(m.Body, &cmd); err != nil {
			return err
		}
		for _, r := range c.cfg.Dispatcher.Handle(ctx, cmd, c.cfg.Now()) {
			if _, err := conn.Send(ctx, "command_result", r); err != nil {
				return err
			}
		}
	case "error":
		var eb errorBody
		_ = json.Unmarshal(m.Body, &eb)
		if eb.Error.Code == "permission_denied" {
			return ErrRevoked
		}
		if eb.Fatal {
			return fmt.Errorf("fatal server error %s: %s", eb.Error.Code, eb.Error.Message)
		}
		c.cfg.Log.Warn("server reported error", "code", eb.Error.Code, "message", eb.Error.Message)
	default:
		_, err := conn.Send(ctx, "error", map[string]any{"ref_id": m.ID, "error": map[string]string{"code": "unsupported_type", "message": "unexpected message type " + m.Type}, "fatal": false})
		return err
	}
	return nil
}

// AppendEvent queues a durable event from another component (e.g. games).
// Safe for concurrent use; delivered by the session when connected.
func (c *Client) AppendEvent(typ string, data map[string]any) uint64 { return c.appendEvent(typ, data) }

func (c *Client) appendEvent(typ string, data map[string]any) uint64 {
	raw, _ := json.Marshal(data)
	e, err := c.cfg.Store.AppendEvent(store.Event{EventID: ulid.New(), EventType: typ, OccurredAt: c.cfg.Now().UTC(), Live: true, Data: raw})
	if err != nil {
		c.cfg.Log.Error("event store", "err", err)
	}
	return e.EventSeq
}

// rejectInvalid answers a schema-invalid but decryptable message without
// dropping the session: unknown commands are rejected as unsupported, anything
// else gets a non-fatal unsupported_type error.
func (c *Client) rejectInvalid(ctx context.Context, conn *channel.Conn, m *channel.Message) error {
	if m.Type == "command" {
		var cmd struct {
			CommandID string `json:"command_id"`
		}
		if json.Unmarshal(m.Body, &cmd) == nil && validULID(cmd.CommandID) {
			_, err := conn.Send(ctx, "command_result", dispatch.Result{CommandID: cmd.CommandID, State: "rejected",
				Error: &dispatch.Error{Code: "unsupported", Message: "command not understood by this agent version"}})
			return err
		}
	}
	if !validULID(m.ID) {
		return errors.New("invalid message without usable id")
	}
	_, err := conn.Send(ctx, "error", map[string]any{"ref_id": m.ID, "error": map[string]string{"code": "unsupported_type", "message": "message not understood by this agent version"}, "fatal": false})
	return err
}

func validULID(s string) bool {
	if len(s) != 26 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", r) {
			return false
		}
	}
	return true
}

// localReachable probes the first local URL: any HTTP answer from HA's /api/
// (401 is expected without a token) means the LAN path is up again.
func (c *Client) localReachable(ctx context.Context) bool {
	for _, u := range c.orderedURLs() {
		if u.Remote() {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		req, _ := http.NewRequestWithContext(pctx, http.MethodGet, u.URL+"/api/", nil)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			return true
		}
	}
	return false
}
