// Package pairing implements the agent side of SAS pairing (ADR-0002):
// Noise XX on /api/deckbeacon/v1/pair, then pair_request → both humans confirm
// the 6-digit SAS → pair_complete carries the connection profile.
package pairing

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/coder/websocket"

	"github.com/jafesu/deckbeacon/apps/agent/internal/channel"
	"github.com/jafesu/deckbeacon/apps/agent/internal/identity"
	"github.com/jafesu/deckbeacon/apps/agent/internal/trust"
	"github.com/jafesu/deckbeacon/packages/protocol/noise"
)

// Request is the pair_request body.
type Request struct {
	AgentVersion string         `json:"agent_version"`
	Protocol     map[string]int `json:"protocol"`
	Device       map[string]any `json:"device"`
	Name         string         `json:"name"`
}

// ConfirmFunc shows the SAS to the local human and returns their decision.
type ConfirmFunc func(ctx context.Context, sas string) (bool, error)

var (
	// ErrNoWindow means HA has no open pairing window.
	ErrNoWindow = errors.New("Home Assistant is not accepting pairing requests: open DeckBeacon in Settings → Devices & services → Add integration first")
	// ErrRejected means a human rejected the code.
	ErrRejected = errors.New("pairing code rejected")
)

// AbortError carries the reason HA aborted pairing.
type AbortError struct{ Reason, Message string }

func (e *AbortError) Error() string { return "pairing aborted by Home Assistant: " + e.Reason }

// Pair runs pairing against base (an HA URL). On success the returned trust is
// NOT yet saved; the caller persists it.
func Pair(ctx context.Context, base string, id *identity.Identity, req Request, confirm ConfirmFunc) (*trust.Trust, error) {
	ctx, cancel := context.WithTimeout(ctx, 11*time.Minute) // window is 10 min
	defer cancel()
	ws, err := channel.Dial(ctx, base, channel.PathPair)
	if errors.Is(err, channel.ErrNotFound) {
		return nil, ErrNoWindow
	}
	if err != nil {
		return nil, err
	}
	defer ws.CloseNow()
	conn, err := channel.Handshake(ctx, ws, noise.Config{Pattern: noise.XX, Prologue: noise.ProloguePair, Static: id.Channel})
	if err != nil {
		return nil, err
	}
	if _, err := conn.Send(ctx, "pair_request", req); err != nil {
		return nil, err
	}
	sas := noise.SAS(conn.Session().HandshakeHash())

	type inbound struct {
		m   *channel.Message
		err error
	}
	msgs := make(chan inbound, 4)
	go func() {
		for {
			m, err := conn.Recv(ctx)
			msgs <- inbound{m, err}
			if err != nil {
				return
			}
		}
	}()
	decision := make(chan error, 1)
	var accepted bool
	go func() {
		ok, err := confirm(ctx, sas)
		accepted = ok
		decision <- err
	}()

	confirmed := false
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-decision:
			if err != nil {
				return nil, err
			}
			if _, err := conn.Send(ctx, "pair_confirm", map[string]bool{"accepted": accepted}); err != nil {
				return nil, err
			}
			if !accepted {
				_ = conn.Close(websocket.StatusNormalClosure, "sas rejected")
				return nil, ErrRejected
			}
			confirmed = true
		case in := <-msgs:
			if in.err != nil {
				return nil, fmt.Errorf("pairing connection: %w", in.err)
			}
			switch in.m.Type {
			case "pair_abort":
				var a struct{ Reason, Message string }
				_ = json.Unmarshal(in.m.Body, &a)
				return nil, &AbortError{a.Reason, a.Message}
			case "pair_complete":
				if !confirmed {
					return nil, errors.New("protocol violation: pair_complete before local confirmation")
				}
				var pc struct {
					InstanceName      string `json:"instance_name"`
					ConnectionProfile struct {
						URLs []trust.URL `json:"urls"`
					} `json:"connection_profile"`
				}
				if err := json.Unmarshal(in.m.Body, &pc); err != nil {
					return nil, err
				}
				urls := []trust.URL{{URL: base, Kind: "manual"}}
				for _, u := range pc.ConnectionProfile.URLs {
					if u.URL != base {
						urls = append(urls, u)
					}
				}
				_ = conn.Close(websocket.StatusNormalClosure, "paired")
				return &trust.Trust{
					ServerStatic: hex.EncodeToString(conn.Session().RemoteStatic()),
					InstanceName: pc.InstanceName, URLs: urls, PairedAt: time.Now().UTC(),
				}, nil
			}
		}
	}
}
