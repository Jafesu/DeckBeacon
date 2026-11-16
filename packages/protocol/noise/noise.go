// Package noise implements the DeckBeacon device channel (ADR-0002):
//
//   - Pairing:  Noise_XX_25519_ChaChaPoly_SHA256 (no prior knowledge; SAS confirms keys)
//   - Sessions: Noise_IK_25519_ChaChaPoly_SHA256 (agent knows the HA static key)
//
// Each Noise message is carried as one binary WebSocket frame. The prologue binds
// the protocol version and the endpoint so a pairing transcript can never be
// replayed as a session (or vice versa).
package noise

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	fnoise "github.com/flynn/noise"
)

// Pattern selects the handshake.
type Pattern int

const (
	// XX is used for pairing.
	XX Pattern = iota
	// IK is used for established sessions.
	IK
)

func (p Pattern) String() string {
	if p == XX {
		return "XX"
	}
	return "IK"
}

// Prologues bind protocol version and endpoint.
var (
	ProloguePair    = []byte("deckbeacon/1 pair")
	PrologueSession = []byte("deckbeacon/1 session")
)

// MaxMessage is the Noise maximum message size; plaintext is MaxMessage-16.
const MaxMessage = 65535

// MaxPlaintext is the largest payload Seal accepts.
const MaxPlaintext = MaxMessage - 16

var suite = fnoise.NewCipherSuite(fnoise.DH25519, fnoise.CipherChaChaPoly, fnoise.HashSHA256)

// KeyPair is an X25519 static or ephemeral key pair.
type KeyPair = fnoise.DHKey

// GenerateKeyPair creates an X25519 key pair from rng (crypto/rand.Reader in production).
func GenerateKeyPair(rng io.Reader) (KeyPair, error) { return suite.GenerateKeypair(rng) }

// KeyPairFromPrivate derives the key pair for a 32-byte X25519 private key.
func KeyPairFromPrivate(priv []byte) (KeyPair, error) {
	if len(priv) != 32 {
		return KeyPair{}, errors.New("noise: private key must be 32 bytes")
	}
	return suite.GenerateKeypair(fixedReader(priv))
}

// Config describes one side of a handshake.
type Config struct {
	Pattern      Pattern
	Initiator    bool
	Prologue     []byte
	Static       KeyPair
	RemoteStatic []byte    // required for the IK initiator
	Random       io.Reader // ephemeral key source; crypto/rand in production, fixed in vectors
}

// Handshake drives a handshake one message at a time.
type Handshake struct {
	hs        *fnoise.HandshakeState
	initiator bool
	pattern   Pattern
	step      int
	done      bool
	session   *Session
}

// NewHandshake validates cfg and prepares the handshake state.
func NewHandshake(cfg Config) (*Handshake, error) {
	if cfg.Random == nil {
		return nil, errors.New("noise: Random is required")
	}
	hp := fnoise.HandshakeXX
	if cfg.Pattern == IK {
		hp = fnoise.HandshakeIK
		if cfg.Initiator && len(cfg.RemoteStatic) != 32 {
			return nil, errors.New("noise: IK initiator needs the responder static key")
		}
	}
	hs, err := fnoise.NewHandshakeState(fnoise.Config{
		CipherSuite:   suite,
		Random:        cfg.Random,
		Pattern:       hp,
		Initiator:     cfg.Initiator,
		Prologue:      cfg.Prologue,
		StaticKeypair: cfg.Static,
		PeerStatic:    cfg.RemoteStatic,
	})
	if err != nil {
		return nil, fmt.Errorf("noise: %w", err)
	}
	return &Handshake{hs: hs, initiator: cfg.Initiator, pattern: cfg.Pattern}, nil
}

// messages per pattern
func (h *Handshake) total() int {
	if h.pattern == XX {
		return 3
	}
	return 2
}

// MyTurn reports whether the next handshake action is WriteMessage.
func (h *Handshake) MyTurn() bool { return (h.step%2 == 0) == h.initiator }

// Done reports handshake completion.
func (h *Handshake) Done() bool { return h.done }

// WriteMessage produces the next handshake message carrying payload.
func (h *Handshake) WriteMessage(payload []byte) ([]byte, error) {
	if h.done || !h.MyTurn() {
		return nil, errors.New("noise: not our turn to write")
	}
	out, cs1, cs2, err := h.hs.WriteMessage(nil, payload)
	if err != nil {
		return nil, fmt.Errorf("noise: write: %w", err)
	}
	h.advance(cs1, cs2)
	return out, nil
}

// ReadMessage consumes the next handshake message and returns its payload.
func (h *Handshake) ReadMessage(msg []byte) ([]byte, error) {
	if h.done || h.MyTurn() {
		return nil, errors.New("noise: not our turn to read")
	}
	if len(msg) > MaxMessage {
		return nil, errors.New("noise: handshake message too large")
	}
	payload, cs1, cs2, err := h.hs.ReadMessage(nil, msg)
	if err != nil {
		return nil, fmt.Errorf("noise: read: %w", err)
	}
	h.advance(cs1, cs2)
	return payload, nil
}

func (h *Handshake) advance(cs1, cs2 *fnoise.CipherState) {
	h.step++
	if h.step < h.total() {
		return
	}
	h.done = true
	send, recv := cs1, cs2
	if !h.initiator {
		send, recv = cs2, cs1
	}
	h.session = &Session{
		send:         send,
		recv:         recv,
		hash:         append([]byte(nil), h.hs.ChannelBinding()...),
		remoteStatic: append([]byte(nil), h.hs.PeerStatic()...),
	}
}

// Session returns the transport session after completion.
func (h *Handshake) Session() (*Session, error) {
	if !h.done {
		return nil, errors.New("noise: handshake incomplete")
	}
	return h.session, nil
}

// Session is an established Noise transport. It is not safe for concurrent Seal
// (or concurrent Open) calls; callers serialise writes and reads separately.
type Session struct {
	send, recv   *fnoise.CipherState
	hash         []byte
	remoteStatic []byte
}

// Seal encrypts one transport message.
func (s *Session) Seal(plaintext []byte) ([]byte, error) {
	if len(plaintext) > MaxPlaintext {
		return nil, errors.New("noise: plaintext too large")
	}
	return s.send.Encrypt(nil, nil, plaintext)
}

// Open decrypts one transport message. Any failure is fatal for the session.
func (s *Session) Open(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) > MaxMessage {
		return nil, errors.New("noise: message too large")
	}
	pt, err := s.recv.Decrypt(nil, nil, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("noise: open: %w", err)
	}
	return pt, nil
}

// HandshakeHash is the final handshake hash h (channel binding).
func (s *Session) HandshakeHash() []byte { return append([]byte(nil), s.hash...) }

// RemoteStatic is the authenticated peer static public key.
func (s *Session) RemoteStatic() []byte { return append([]byte(nil), s.remoteStatic...) }

// SAS derives the 6-digit short authentication string shown on both sides during
// pairing: SHA-256("deckbeacon-sas-v1" || h), first 4 bytes big-endian, mod 10^6.
// A MITM yields different handshake hashes on each side, hence different codes.
func SAS(handshakeHash []byte) string {
	d := sha256.Sum256(append([]byte("deckbeacon-sas-v1"), handshakeHash...))
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(d[:4])%1_000_000)
}

type fixed struct{ b []byte }

func (f *fixed) Read(p []byte) (int, error) {
	if len(f.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, f.b)
	f.b = f.b[n:]
	return n, nil
}

func fixedReader(b []byte) io.Reader { return &fixed{b: append([]byte(nil), b...)} }

// FixedRandom returns a reader yielding exactly the given bytes, for test vectors.
func FixedRandom(b ...[]byte) io.Reader {
	var all []byte
	for _, x := range b {
		all = append(all, x...)
	}
	return fixedReader(all)
}
