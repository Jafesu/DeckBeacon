// Package fakeha is a minimal Go stand-in for the DeckBeacon HA integration's
// device endpoints, used only by agent unit tests. The real server is the Python
// integration; interop is proven by the cross-language vectors and the E2E suite.
package fakeha

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jafesu/deckbeacon/apps/agent/internal/ulid"
	"github.com/jafesu/deckbeacon/packages/protocol"
	"github.com/jafesu/deckbeacon/packages/protocol/noise"
)

// Msg is a received envelope.
type Msg struct {
	Type string          `json:"type"`
	ID   string          `json:"id"`
	Body json.RawMessage `json:"body"`
}

// Server is the fake.
type Server struct {
	HTTP   *httptest.Server
	Static noise.KeyPair

	mu          sync.Mutex
	window      bool
	AutoConfirm bool
	ConfirmSAS  chan string // receives the SAS the server computed
	devices     map[string]bool
	Received    []Msg
	LastEvent   uint64
	commands    chan any
	conns       []*websocket.Conn
	Heartbeat   int
	Invalid     []string // schema violations seen (must stay empty)
}

// New starts the fake on a random port.
func New() *Server {
	kp, _ := noise.GenerateKeyPair(rand.Reader)
	s := &Server{Static: kp, devices: map[string]bool{}, commands: make(chan any, 8), ConfirmSAS: make(chan string, 1), Heartbeat: 5, AutoConfirm: true}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/deckbeacon/v1/pair", s.pair)
	mux.HandleFunc("/api/deckbeacon/v1/ws", s.session)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	s.HTTP = httptest.NewServer(mux)
	return s
}

// URL is the base URL.
func (s *Server) URL() string { return s.HTTP.URL }

// OpenWindow allows one pairing.
func (s *Server) OpenWindow() { s.mu.Lock(); s.window = true; s.mu.Unlock() }

// Revoke forgets a device static key.
func (s *Server) Revoke(pubHex string) { s.mu.Lock(); delete(s.devices, pubHex); s.mu.Unlock() }

// Paired reports whether a device key is enrolled.
func (s *Server) Paired(pubHex string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.devices[pubHex]
}

// SendCommand queues a command body for the next connected session.
func (s *Server) SendCommand(body any) { s.commands <- body }

// DropAll abruptly closes all session connections.
func (s *Server) DropAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		c.CloseNow()
	}
	s.conns = nil
}

// Messages returns a copy of received messages of a type.
func (s *Server) Messages(typ string) []Msg {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Msg
	for _, m := range s.Received {
		if typ == "" || m.Type == typ {
			out = append(out, m)
		}
	}
	return out
}

// Close stops the server.
func (s *Server) Close() { s.HTTP.Close() }

type peer struct {
	ws   *websocket.Conn
	sess *noise.Session
	seq  uint64
	srv  *Server
}

func (p *peer) send(ctx context.Context, typ string, body any) error {
	b, _ := json.Marshal(body)
	p.seq++
	env, _ := json.Marshal(map[string]any{"v": 1, "type": typ, "id": ulid.New(), "ts": time.Now().UTC().Format(time.RFC3339), "seq": p.seq, "body": json.RawMessage(b)})
	if err := protocol.ValidateMessage(env); err != nil {
		panic("fakeha sending invalid message: " + err.Error())
	}
	ct, err := p.sess.Seal(env)
	if err != nil {
		return err
	}
	return p.ws.Write(ctx, websocket.MessageBinary, ct)
}

func (p *peer) recv(ctx context.Context) (Msg, error) {
	_, ct, err := p.ws.Read(ctx)
	if err != nil {
		return Msg{}, err
	}
	raw, err := p.sess.Open(ct)
	if err != nil {
		return Msg{}, err
	}
	if err := protocol.ValidateMessage(raw); err != nil {
		p.srv.mu.Lock()
		p.srv.Invalid = append(p.srv.Invalid, err.Error())
		p.srv.mu.Unlock()
	}
	var m Msg
	err = json.Unmarshal(raw, &m)
	p.srv.mu.Lock()
	p.srv.Received = append(p.srv.Received, m)
	p.srv.mu.Unlock()
	return m, err
}

func (s *Server) handshake(ctx context.Context, w http.ResponseWriter, r *http.Request, pat noise.Pattern, prologue []byte) (*peer, error) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return nil, err
	}
	hs, err := noise.NewHandshake(noise.Config{Pattern: pat, Prologue: prologue, Static: s.Static, Random: rand.Reader})
	if err != nil {
		return nil, err
	}
	for !hs.Done() {
		if hs.MyTurn() {
			out, err := hs.WriteMessage(nil)
			if err != nil {
				return nil, err
			}
			if err := ws.Write(ctx, websocket.MessageBinary, out); err != nil {
				return nil, err
			}
			continue
		}
		_, in, err := ws.Read(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := hs.ReadMessage(in); err != nil {
			ws.Close(4003, "handshake failed")
			return nil, err
		}
	}
	sess, _ := hs.Session()
	return &peer{ws: ws, sess: sess, srv: s}, nil
}

func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	open := s.window
	s.window = false
	s.mu.Unlock()
	if !open {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	p, err := s.handshake(ctx, w, r, noise.XX, noise.ProloguePair)
	if err != nil {
		return
	}
	defer p.ws.CloseNow()
	if m, err := p.recv(ctx); err != nil || m.Type != "pair_request" {
		return
	}
	s.ConfirmSAS <- noise.SAS(p.sess.HandshakeHash())
	m, err := p.recv(ctx)
	if err != nil || m.Type != "pair_confirm" {
		return
	}
	var c struct{ Accepted bool }
	_ = json.Unmarshal(m.Body, &c)
	if !c.Accepted || !s.AutoConfirm {
		_ = p.send(ctx, "pair_abort", map[string]string{"reason": "sas_rejected"})
		return
	}
	s.mu.Lock()
	s.devices[hex.EncodeToString(p.sess.RemoteStatic())] = true
	s.mu.Unlock()
	_ = p.send(ctx, "pair_complete", map[string]any{"instance_name": "Fake HA", "connection_profile": map[string]any{
		"urls": []map[string]string{{"url": s.URL(), "kind": "internal"}}}})
	_, _, _ = p.ws.Read(ctx) // wait for client close
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := s.handshake(ctx, w, r, noise.IK, noise.PrologueSession)
	if err != nil {
		return
	}
	defer p.ws.CloseNow()
	s.mu.Lock()
	s.conns = append(s.conns, p.ws)
	s.mu.Unlock()
	hello, err := p.recv(ctx)
	if err != nil || hello.Type != "hello" {
		return
	}
	if !s.Paired(hex.EncodeToString(p.sess.RemoteStatic())) {
		_ = p.send(ctx, "error", map[string]any{"error": map[string]string{"code": "permission_denied", "message": "device not paired"}, "fatal": true})
		p.ws.Close(4003, "revoked")
		return
	}
	s.mu.Lock()
	last := s.LastEvent
	s.mu.Unlock()
	_ = p.send(ctx, "welcome", map[string]any{"protocol": 1, "integration_version": "0.0.0", "heartbeat_s": s.Heartbeat, "last_received_event_seq": last,
		"policy": map[string]any{"transport": "local", "granted": []string{"monitoring"}}})
	in := make(chan Msg, 16)
	go func() {
		for {
			m, err := p.recv(ctx)
			if err != nil {
				close(in)
				return
			}
			in <- m
		}
	}()
	for {
		select {
		case m, ok := <-in:
			if !ok {
				return
			}
			switch m.Type {
			case "event":
				var e struct {
					EventSeq uint64 `json:"event_seq"`
				}
				_ = json.Unmarshal(m.Body, &e)
				s.mu.Lock()
				if e.EventSeq > s.LastEvent {
					s.LastEvent = e.EventSeq
				}
				s.mu.Unlock()
				_ = p.send(ctx, "ack", map[string]any{"ref_id": m.ID, "event_seq": e.EventSeq})
			case "ping":
				_ = p.send(ctx, "pong", map[string]any{"ref_id": m.ID})
			}
		case c := <-s.commands:
			_ = p.send(ctx, "command", c)
		case <-ctx.Done():
			return
		}
	}
}
