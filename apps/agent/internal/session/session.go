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
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jafesu/deckbeacon/apps/agent/internal/channel"
	"github.com/jafesu/deckbeacon/apps/agent/internal/collect"
	"github.com/jafesu/deckbeacon/apps/agent/internal/dispatch"
	"github.com/jafesu/deckbeacon/apps/agent/internal/identity"
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
			if errors.Is(err, errPreferLocal) {
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
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.cfg.Backoff(attempt)):
		}
		attempt++
	}
}

var errPreferLocal = errors.New("local transport available again")

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
	hb := time.Duration(w.HeartbeatS) * time.Second
	sessionStart := c.cfg.Now()
	c.setStatus("connected_"+transport, transport, u.URL, "")
	c.cfg.Log.Info("connected", "transport", transport, "integration", w.IntegrationVersion)

	caps := c.cfg.Collector.Capabilities(ctx)
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
					rxErr <- err // strict for now: invalid inbound closes the session
					return
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
	flush := time.NewTicker(time.Second)
	defer flush.Stop()
	probe := time.NewTicker(c.cfg.LocalProbeEvery)
	defer probe.Stop()
	lastRx := c.cfg.Now()
	var sentThrough uint64
	localOK := 0

	sendTelemetry := func() error {
		ms := c.cfg.Collector.Collect(ctx)
		if len(ms) == 0 {
			return nil
		}
		_, err := conn.Send(ctx, "telemetry", map[string]any{"metrics": ms})
		return err
	}
	flushEvents := func() error {
		evs, err := c.cfg.Store.PendingEvents(sentThrough, 50)
		if err != nil {
			return err
		}
		for _, e := range evs {
			// An event is "live" only if it is delivered promptly within the
			// session that produced it; anything else is backfill (spec 07).
			e.Live = e.Live && e.OccurredAt.After(sessionStart.Add(-time.Second)) && c.cfg.Now().Sub(e.OccurredAt) < 30*time.Second
			if _, err := conn.Send(ctx, "event", e); err != nil {
				return err
			}
			sentThrough = e.EventSeq
		}
		return nil
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
		case <-telemetry.C:
			if err := sendTelemetry(); err != nil {
				return err
			}
		case <-flush.C:
			if err := flushEvents(); err != nil {
				return err
			}
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

func (c *Client) appendEvent(typ string, data map[string]any) {
	raw, _ := json.Marshal(data)
	if _, err := c.cfg.Store.AppendEvent(store.Event{EventID: ulid.New(), EventType: typ, OccurredAt: c.cfg.Now().UTC(), Live: true, Data: raw}); err != nil {
		c.cfg.Log.Error("event store", "err", err)
	}
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
