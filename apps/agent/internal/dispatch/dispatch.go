// Package dispatch executes HA commands with durable dedupe and relative TTL
// (COMPATIBILITY.md). M2 registers no handlers: every command is answered
// rejected/unsupported, but the dedupe/expiry machinery is real.
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
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
}

// Handler executes one command kind. It returns the terminal result.
type Handler func(ctx context.Context, cmd Command) Result

// Dispatcher routes commands.
type Dispatcher struct {
	store    *store.Store
	handlers map[string]Handler
	now      func() time.Time
}

// New creates a dispatcher.
func New(s *store.Store, now func() time.Time) *Dispatcher {
	return &Dispatcher{store: s, handlers: map[string]Handler{}, now: now}
}

// Register adds a handler for a command name.
func (d *Dispatcher) Register(name string, h Handler) { d.handlers[name] = h }

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
	h, ok := d.handlers[cmd.Name]
	if !ok {
		return finish(Result{CommandID: cmd.CommandID, State: "rejected", Error: &Error{Code: "unsupported", Message: cmd.Name + " is not supported by this agent version"}})
	}
	accepted := Result{CommandID: cmd.CommandID, State: "accepted"}
	final := h(ctx, cmd)
	_ = d.store.FinishCommand(cmd.CommandID, final.State, d.now())
	return []Result{accepted, final}
}
