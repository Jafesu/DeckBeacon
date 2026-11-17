// Package channel carries schema-validated protocol envelopes over a Noise session
// inside a WebSocket (ADR-0001/0002). One Noise message = one binary frame.
package channel

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jafesu/deckbeacon/apps/agent/internal/ulid"
	"github.com/jafesu/deckbeacon/packages/protocol"
	"github.com/jafesu/deckbeacon/packages/protocol/noise"
)

// Endpoint paths on the HA HTTP server.
const (
	PathPair    = "/api/deckbeacon/v1/pair"
	PathSession = "/api/deckbeacon/v1/ws"
)

// Message is a decoded envelope.
type Message struct {
	V    int             `json:"v"`
	Type string          `json:"type"`
	ID   string          `json:"id"`
	TS   string          `json:"ts"`
	Seq  uint64          `json:"seq"`
	Body json.RawMessage `json:"body"`
}

// ErrNotFound is returned when the endpoint answers 404 (e.g. no pairing window).
var ErrNotFound = errors.New("endpoint not found (404)")

// WSURL converts an HA base URL (http/https/ws/wss) plus path into a ws(s) URL.
func WSURL(base, path string) (string, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("URL has no host")
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// Dial opens the WebSocket. 404 maps to ErrNotFound.
func Dial(ctx context.Context, base, path string) (*websocket.Conn, error) {
	target, err := WSURL(base, path)
	if err != nil {
		return nil, err
	}
	c, resp, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: &http.Client{Timeout: 15 * time.Second}})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	c.SetReadLimit(noise.MaxMessage + 1024)
	return c, nil
}

// Conn is an established encrypted channel.
type Conn struct {
	ws      *websocket.Conn
	sess    *noise.Session
	wmu     sync.Mutex
	seq     uint64
	now     func() time.Time
	Timeout time.Duration
}

// Handshake runs the Noise handshake as initiator over ws.
func Handshake(ctx context.Context, ws *websocket.Conn, cfg noise.Config) (*Conn, error) {
	cfg.Initiator = true
	if cfg.Random == nil {
		cfg.Random = rand.Reader
	}
	hs, err := noise.NewHandshake(cfg)
	if err != nil {
		return nil, err
	}
	for !hs.Done() {
		if hs.MyTurn() {
			msg, err := hs.WriteMessage(nil)
			if err != nil {
				return nil, err
			}
			if err := ws.Write(ctx, websocket.MessageBinary, msg); err != nil {
				return nil, fmt.Errorf("handshake write: %w", err)
			}
			continue
		}
		typ, msg, err := ws.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("handshake read: %w", err)
		}
		if typ != websocket.MessageBinary {
			return nil, errors.New("handshake: unexpected text frame")
		}
		if _, err := hs.ReadMessage(msg); err != nil {
			return nil, err
		}
	}
	sess, err := hs.Session()
	if err != nil {
		return nil, err
	}
	return &Conn{ws: ws, sess: sess, now: time.Now, Timeout: 10 * time.Second}, nil
}

// Session exposes the Noise session (handshake hash, peer static).
func (c *Conn) Session() *noise.Session { return c.sess }

// Send builds, validates, encrypts and writes one envelope.
func (c *Conn) Send(ctx context.Context, typ string, body any) (string, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	rawBody, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	c.seq++
	m := Message{V: protocol.Version, Type: typ, ID: ulid.New(), TS: c.now().UTC().Format(time.RFC3339Nano), Seq: c.seq, Body: rawBody}
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	if err := protocol.ValidateMessage(raw); err != nil {
		return "", fmt.Errorf("refusing to send invalid %s: %w", typ, err)
	}
	ct, err := c.sess.Seal(raw)
	if err != nil {
		return "", err
	}
	wctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	if err := c.ws.Write(wctx, websocket.MessageBinary, ct); err != nil {
		return "", err
	}
	return m.ID, nil
}

// Recv reads, decrypts and validates one envelope. A decryption or validation
// failure is fatal for the connection (the caller must close it).
func (c *Conn) Recv(ctx context.Context) (*Message, error) {
	typ, ct, err := c.ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageBinary {
		return nil, errors.New("unexpected text frame")
	}
	raw, err := c.sess.Open(ct)
	if err != nil {
		return nil, err
	}
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("invalid envelope JSON: %w", err)
	}
	if err := protocol.ValidateInboundMessage(raw); err != nil { // lenient receiver: unknown fields are ignored
		return &m, &InvalidMessageError{Type: m.Type, ID: m.ID, Err: err}
	}
	return &m, nil
}

// InvalidMessageError reports a schema violation in an inbound message. Under the
// lenient-receiver rule (COMPATIBILITY.md) callers may answer with an error and
// continue for unknown types; other violations close the session.
type InvalidMessageError struct {
	Type, ID string
	Err      error
}

func (e *InvalidMessageError) Error() string {
	return fmt.Sprintf("invalid inbound %q: %v", e.Type, e.Err)
}

// Close closes the WebSocket with a status code.
func (c *Conn) Close(code websocket.StatusCode, reason string) error {
	return c.ws.Close(code, reason)
}

// CloseNow aborts the connection.
func (c *Conn) CloseNow() error { return c.ws.CloseNow() }
