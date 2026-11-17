// Package dispatch executes HA commands with durable dedupe and relative TTL
// (COMPATIBILITY.md). M2 registers no handlers: every command is answered
// rejected/unsupported, but the dedupe/expiry machinery is real.
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/store"
)

// Command is a protocol command body.
type Command struct {
	CommandID       string          `json:"command_id"`
	Name            string          `json:"name"`
	Args            json.RawMessage `json:"args"`
	TTLSeconds      int             `json:"ttl_s"`
	IssuedAt        string          `json:"issued_at"`
	ConfirmedByUser bool            `json:"confirmed_by_user,omitempty"`
}

// Error is the protocol error object.
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

// Result is a protocol command_result body.
type Result struct {
	CommandID string         `json:"command_id"`
	State     string         `json:"state"`
	Error     *Error         `json:"error,omitempty"`
	Result    map[string]any `json:"result,omitempty"`
	// Attachments are messages the session sends before this result (e.g.
	// artwork chunks, protocol 1.4). Never persisted or deduplicated.
	Attachments []Attachment `json:"-"`
}

// Attachment is one extra protocol message belonging to a result.
type Attachment struct {
	Type string
	Body any
}

// Handler executes one command kind. It returns the immediate result and,
// optionally, a follow-up that runs asynchronously and yields the final state
// (e.g. launch: dispatched now, confirmed once the game process appears).
type Handler func(ctx context.Context, cmd Command) (Result, func(ctx context.Context) Result)

type entry struct {
	tier string
	h    Handler
}

// Dispatcher routes commands and enforces permission tiers on the agent
// (spec 10: "enforce on agent, not just HA UI").
type Dispatcher struct {
	store    *store.Store
	handlers map[string]entry
	now      func() time.Time
	mu       sync.Mutex
	granted  map[string]bool
	late     chan Result
}

// New creates a dispatcher.
func New(s *store.Store, now func() time.Time) *Dispatcher {
	return &Dispatcher{store: s, handlers: map[string]entry{}, now: now, granted: map[string]bool{"monitoring": true}, late: make(chan Result, 16)}
}

// Register adds a handler for a command name requiring a permission tier.
func (d *Dispatcher) Register(name, tier string, h Handler) { d.handlers[name] = entry{tier, h} }

// SetGranted replaces the tiers granted for the current session (from welcome),
// intersected by the caller with the agent's local policy.
func (d *Dispatcher) SetGranted(tiers []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.granted = map[string]bool{}
	for _, t := range tiers {
		d.granted[t] = true
	}
}

func (d *Dispatcher) allowed(tier string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.granted[tier]
}

// Late delivers results of asynchronous follow-ups.
func (d *Dispatcher) Late() <-chan Result { return d.late }

// Handle processes a command received at receivedAt and returns the results to
// send, in order. A replayed command id is never executed again.
func (d *Dispatcher) Handle(ctx context.Context, cmd Command, receivedAt time.Time) []Result {
	now := d.now()
	if _, err := d.store.BeginCommand(cmd.CommandID, now); errors.Is(err, store.ErrDuplicate) {
		return []Result{{CommandID: cmd.CommandID, State: "duplicate"}}
	} else if err != nil {
		return []Result{{CommandID: cmd.CommandID, State: "failed", Error: &Error{Code: "internal", Message: "store error", Retryable: true}}}
	}
	finish := func(r Result) []Result {
		_ = d.store.FinishCommand(cmd.CommandID, r.State, d.now())
		return []Result{r}
	}
	if now.Sub(receivedAt) > time.Duration(cmd.TTLSeconds)*time.Second {
		return finish(Result{CommandID: cmd.CommandID, State: "expired", Error: &Error{Code: "expired", Message: "command TTL elapsed before execution"}})
	}
	e, ok := d.handlers[cmd.Name]
	if !ok {
		return finish(Result{CommandID: cmd.CommandID, State: "rejected", Error: &Error{Code: "unsupported", Message: cmd.Name + " is not supported by this agent version"}})
	}
	if !d.allowed(e.tier) {
		return finish(Result{CommandID: cmd.CommandID, State: "rejected", Error: &Error{Code: "permission_denied", Message: "permission tier " + e.tier + " is not granted for this connection"}})
	}
	accepted := Result{CommandID: cmd.CommandID, State: "accepted"}
	r, follow := e.h(ctx, cmd)
	_ = d.store.FinishCommand(cmd.CommandID, r.State, d.now())
	if follow != nil {
		go func() {
			fr := follow(context.WithoutCancel(ctx))
			_ = d.store.FinishCommand(cmd.CommandID, fr.State, d.now())
			d.late <- fr
		}()
	}
	return []Result{accepted, r}
}
