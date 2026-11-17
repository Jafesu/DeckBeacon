// Package activity implements ADR-0007 tier P1: an opt-in Discord IPC proxy
// that reports a game's Rich Presence text (details, state, party, elapsed)
// while forwarding every frame unchanged to the user's real Discord client.
//
// Rules (ADR-0007): local IPC only, never talks to Discord's servers; forward-
// only (no Discord client running means the game sees no Discord, exactly as
// without DeckBeacon); payload text is untrusted, sanitized and length-capped;
// images, URLs, buttons and secrets in the payload are ignored; nothing is
// persisted beyond the current connection.
package activity

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

// Discord IPC opcodes.
const (
	opHandshake = 0
	opFrame     = 1
	opClose     = 2
)

const (
	maxFrame      = 1 << 20  // larger frames close the connection
	maxParsed     = 64 << 10 // only frames up to this size are inspected
	maxText       = 128      // Discord's own limit for details/state
	maxSlots      = 10
	dialTimeout   = 500 * time.Millisecond
	socketPattern = "discord-ipc-%d"
)

// Activity is the sanitized presence of one connected game.
type Activity struct {
	Details   string
	State     string
	PartySize int
	PartyMax  int
	Started   time.Time
	PID       int
	Updated   time.Time
}

// Status describes the proxy for capabilities.
type Status struct {
	Listening string // socket path ("" when not listening)
	Slot      int
	Reason    string // why not listening, or a reduced-capability note
}

// Config wires the proxy.
type Config struct {
	RuntimeDir string // $XDG_RUNTIME_DIR
	Log        *slog.Logger
	Now        func() time.Time
}

// Proxy is safe for concurrent use.
type Proxy struct {
	cfg    Config
	mu     sync.Mutex
	ln     net.Listener
	status Status
	conns  map[uint64]*Activity
	open   map[net.Conn]struct{}
	nextID uint64
	wg     sync.WaitGroup
}

// New creates a proxy (not yet listening).
func New(cfg Config) *Proxy {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Proxy{cfg: cfg, conns: map[uint64]*Activity{}, open: map[net.Conn]struct{}{}, status: Status{Slot: -1, Reason: "not started"}}
}

// Start listens on the first free discord-ipc-N in the runtime dir. A stale
// socket (connection refused) left by a crashed process is replaced; a live
// one belongs to Discord (or another proxy) and is never touched.
func (p *Proxy) Start() error {
	if p.cfg.RuntimeDir == "" {
		p.setStatus(Status{Slot: -1, Reason: "XDG_RUNTIME_DIR not set"})
		return errors.New("activity: no runtime dir")
	}
	var note string
	for n := 0; n < maxSlots; n++ {
		path := filepath.Join(p.cfg.RuntimeDir, fmt.Sprintf(socketPattern, n))
		if _, err := os.Lstat(path); err == nil {
			if !stale(path) {
				if n == 0 {
					note = "another Discord IPC server holds slot 0; games that only try slot 0 bypass DeckBeacon"
				}
				continue
			}
			_ = os.Remove(path)
		}
		ln, err := net.Listen("unix", path)
		if err != nil {
			continue
		}
		_ = os.Chmod(path, 0o600)
		p.mu.Lock()
		p.ln = ln
		p.status = Status{Listening: path, Slot: n, Reason: note}
		p.mu.Unlock()
		p.wg.Add(1)
		go p.accept(ln)
		p.cfg.Log.Info("discord activity proxy listening", "slot", n)
		return nil
	}
	p.setStatus(Status{Slot: -1, Reason: "no free discord-ipc slot"})
	return errors.New("activity: no free slot")
}

func (p *Proxy) setStatus(s Status) {
	p.mu.Lock()
	p.status = s
	p.mu.Unlock()
}

// stale reports whether path is a socket nobody listens on.
func stale(path string) bool {
	st, err := os.Lstat(path)
	if err != nil || st.Mode()&os.ModeSocket == 0 {
		return false // not a socket: leave it alone, take another slot
	}
	c, err := net.DialTimeout("unix", path, dialTimeout)
	if err == nil {
		_ = c.Close()
		return false
	}
	return errors.Is(err, syscall.ECONNREFUSED)
}

// Close stops listening, removes our socket and ends all connections.
func (p *Proxy) Close() {
	p.mu.Lock()
	ln := p.ln
	p.ln = nil
	path := p.status.Listening
	p.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
		_ = os.Remove(path)
	}
	p.mu.Lock()
	for c := range p.open {
		_ = c.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
}

// Status returns the current listener state.
func (p *Proxy) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// Current returns the most recently updated non-empty activity, if any.
func (p *Proxy) Current() *Activity {
	p.mu.Lock()
	defer p.mu.Unlock()
	var best *Activity
	for _, a := range p.conns {
		if a.Details == "" && a.State == "" {
			continue
		}
		if best == nil || a.Updated.After(best.Updated) {
			best = a
		}
	}
	if best == nil {
		return nil
	}
	c := *best
	return &c
}

func (p *Proxy) accept(ln net.Listener) {
	defer p.wg.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.serve(c)
		}()
	}
}

// upstreamCandidates lists where a real Discord client may listen: the
// standard path (other slots), Flatpak and Snap sandboxes (G15: Flatpak only).
func (p *Proxy) upstreamCandidates() []string {
	own := p.Status().Listening
	var out []string
	dirs := []string{
		p.cfg.RuntimeDir,
		filepath.Join(p.cfg.RuntimeDir, "app", "com.discordapp.Discord"),
		filepath.Join(p.cfg.RuntimeDir, "app", "com.discordapp.DiscordCanary"),
		filepath.Join(p.cfg.RuntimeDir, "app", "dev.vencord.Vesktop"),
		filepath.Join(p.cfg.RuntimeDir, "snap.discord"),
	}
	for _, d := range dirs {
		for n := 0; n < maxSlots; n++ {
			path := filepath.Join(d, fmt.Sprintf(socketPattern, n))
			if path != own {
				out = append(out, path)
			}
		}
	}
	return out
}

func (p *Proxy) dialUpstream() (net.Conn, error) {
	for _, path := range p.upstreamCandidates() {
		if _, err := os.Lstat(path); err != nil {
			continue
		}
		if c, err := net.DialTimeout("unix", path, dialTimeout); err == nil {
			return c, nil
		}
	}
	return nil, errors.New("no Discord client running")
}

func (p *Proxy) serve(client net.Conn) {
	defer client.Close()
	up, err := p.dialUpstream()
	if err != nil {
		return // forward-only: behave exactly as if no Discord were running
	}
	defer up.Close()
	p.mu.Lock()
	if p.ln == nil { // closing
		p.mu.Unlock()
		return
	}
	p.nextID++
	id := p.nextID
	p.conns[id] = &Activity{}
	p.open[client], p.open[up] = struct{}{}, struct{}{}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.conns, id)
		delete(p.open, client)
		delete(p.open, up)
		p.mu.Unlock()
	}()
	done := make(chan struct{}, 2)
	go func() { // Discord -> game: unchanged
		_, _ = io.Copy(client, up)
		done <- struct{}{}
	}()
	go func() { // game -> Discord: unchanged, inspected on the way
		p.pump(id, client, up)
		done <- struct{}{}
	}()
	<-done
}

// pump forwards frames from the game to Discord byte-for-byte.
func (p *Proxy) pump(id uint64, from io.Reader, to io.Writer) {
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(from, hdr); err != nil {
			return
		}
		op := binary.LittleEndian.Uint32(hdr[0:4])
		n := binary.LittleEndian.Uint32(hdr[4:8])
		if n > maxFrame {
			return
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(from, body); err != nil {
			return
		}
		if _, err := to.Write(append(hdr[:8:8], body...)); err != nil {
			return
		}
		if op == opFrame && n <= maxParsed {
			p.inspect(id, body)
		}
		if op == opClose {
			return
		}
	}
}

type rpcFrame struct {
	Cmd  string `json:"cmd"`
	Args struct {
		PID      int             `json:"pid"`
		Activity json.RawMessage `json:"activity"`
	} `json:"args"`
}

type rpcActivity struct {
	Details    string `json:"details"`
	State      string `json:"state"`
	Timestamps struct {
		Start json.Number `json:"start"`
	} `json:"timestamps"`
	Party struct {
		Size []json.Number `json:"size"`
	} `json:"party"`
}

func (p *Proxy) inspect(id uint64, body []byte) {
	var f rpcFrame
	if json.Unmarshal(body, &f) != nil || f.Cmd != "SET_ACTIVITY" {
		return
	}
	a := Activity{PID: f.Args.PID, Updated: p.cfg.Now()}
	if len(f.Args.Activity) > 0 && string(f.Args.Activity) != "null" {
		var ra rpcActivity
		if json.Unmarshal(f.Args.Activity, &ra) != nil {
			return
		}
		a.Details, a.State = Sanitize(ra.Details), Sanitize(ra.State)
		a.Started = epoch(ra.Timestamps.Start)
		if len(ra.Party.Size) == 2 {
			a.PartySize, a.PartyMax = smallInt(ra.Party.Size[0]), smallInt(ra.Party.Size[1])
			if a.PartyMax < a.PartySize {
				a.PartySize, a.PartyMax = 0, 0
			}
		}
	} // activity null = clear
	p.mu.Lock()
	if _, ok := p.conns[id]; ok {
		p.conns[id] = &a
	}
	p.mu.Unlock()
}

// Sanitize makes untrusted presence text safe to display: valid UTF-8, no
// control or format characters, collapsed whitespace, at most maxText runes.
func Sanitize(s string) string {
	s = strings.ToValidUTF8(s, "")
	var b strings.Builder
	space := false
	n := 0
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError {
			continue
		}
		if space {
			if n+1 >= maxText {
				break
			}
			b.WriteByte(' ')
			n++
			space = false
		}
		if n >= maxText {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// epoch accepts seconds or milliseconds (games send either).
func epoch(n json.Number) time.Time {
	v, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil || v <= 0 {
		return time.Time{}
	}
	if v > 1e12 {
		v /= 1000
	}
	if v > 1e11 { // nonsense: beyond year 5000
		return time.Time{}
	}
	return time.Unix(v, 0).UTC()
}

func smallInt(n json.Number) int {
	v, err := strconv.ParseInt(string(n), 10, 32)
	if err != nil || v < 0 || v > 10000 {
		return 0
	}
	return int(v)
}
