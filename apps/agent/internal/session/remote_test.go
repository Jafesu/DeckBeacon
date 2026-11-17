package session

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/testutil/fakeha"
	"github.com/jafesu/deckbeacon/apps/agent/internal/trust"
)

func hellos(srv *fakeha.Server) []map[string]any {
	var out []map[string]any
	for _, m := range srv.Messages("hello") {
		var b struct {
			Session map[string]any `json:"session"`
		}
		_ = json.Unmarshal(m.Body, &b)
		out = append(out, b.Session)
	}
	return out
}

func deadURL(t *testing.T) string {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	_ = l.Close() // nothing listens here any more: connection refused
	return "http://" + addr
}

func TestProfileRefreshPersistsAndNeverWipes(t *testing.T) {
	r := newRig(t)
	var mu sync.Mutex
	var saved [][]trust.URL
	r.cli.cfg.OnProfile = func(u []trust.URL) { mu.Lock(); saved = append(saved, u); mu.Unlock() }
	r.srv.Profile = []map[string]string{{"url": r.srv.URL(), "kind": "internal"}, {"url": "https://example.ui.nabu.casa", "kind": "cloud"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.cli.Run(ctx) }()
	waitFor(t, "profile saved", func() bool { mu.Lock(); defer mu.Unlock(); return len(saved) == 1 })
	mu.Lock()
	got := saved[0]
	mu.Unlock()
	if len(got) != 2 || got[1].Kind != "cloud" || !got[1].Remote() {
		t.Fatalf("profile %+v", got)
	}
	if v := hellos(r.srv)[0]["via"]; v == nil {
		t.Fatal("hello must say which URL kind it used")
	}
	// An unusable profile (only a credentialed URL is invalid per agent rules,
	// still schema-valid) must not replace the stored list.
	before := append([]trust.URL(nil), r.cli.cfg.Trust.URLs...)
	r.cli.applyProfile(welcomeBody{ConnectionProfile: &struct {
		URLs []trust.URL `json:"urls"`
	}{URLs: []trust.URL{{URL: "https://user:pw@evil.invalid", Kind: "external"}, {URL: "ftp://x", Kind: "internal"}}}})
	if !trust.SameURLs(before, r.cli.cfg.Trust.URLs) {
		t.Fatal("invalid profile wiped the stored URLs")
	}
}

func TestNetworkChangeFailsOverWithoutWaitingForHeartbeat(t *testing.T) {
	r := newRig(t)
	r.srv.Heartbeat = 300 // a heartbeat timeout would take 15 minutes
	net := make(chan struct{}, 1)
	r.cli.cfg.NetChanged = net
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.cli.Run(ctx) }()
	waitFor(t, "connected", func() bool { return strings.HasPrefix(r.cli.Status().State, "connected_") })
	r.srv.Mute.Store(true) // the old network silently drops everything
	start := time.Now()
	net <- struct{}{}
	// The agent must give up on the silent session and reconnect (a new hello)
	// within the 5 s pong deadline, not after the 15-minute heartbeat timeout.
	deadline := time.Now().Add(9 * time.Second)
	for len(hellos(r.srv)) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if len(hellos(r.srv)) < 2 {
		t.Fatal("no reconnect after the network change")
	}
	if d := time.Since(start); d > 7*time.Second {
		t.Fatalf("failover took %v", d)
	}
	r.srv.Mute.Store(false)
	waitFor(t, "reconnected", func() bool { return strings.HasPrefix(r.cli.Status().State, "connected_") })
	// A healthy network change keeps the session.
	net <- struct{}{}
	time.Sleep(5500 * time.Millisecond)
	if len(hellos(r.srv)) != 2 || !strings.HasPrefix(r.cli.Status().State, "connected_") {
		t.Fatal("a responsive session must survive a network change")
	}
}

func TestDiscoveryFindsMovedHAAndIgnoresSpoofs(t *testing.T) {
	r := newRig(t)
	spoof := fakeha.New() // a LAN peer announcing itself as HA, with its own key
	t.Cleanup(spoof.Close)
	var discovered atomic.Int32
	r.cli.cfg.Trust.URLs = []trust.URL{{URL: deadURL(t), Kind: "internal"}} // HA moved
	r.cli.cfg.Discover = func(ctx context.Context) []trust.URL {
		discovered.Add(1)
		return []trust.URL{{URL: spoof.URL(), Kind: "internal"}, {URL: r.srv.URL(), Kind: "internal"}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.cli.Run(ctx) }()
	waitFor(t, "connected via discovery", func() bool { return r.cli.Status().State == "connected_local" })
	if h := hellos(r.srv); len(h) != 1 || h[0]["via"] != "discovered" {
		t.Fatalf("hello %+v", h)
	}
	if len(spoof.Messages("hello")) != 0 {
		t.Fatal("the spoofed host must never get a session (pinned key)")
	}
	if discovered.Load() == 0 {
		t.Fatal("discovery not used")
	}
}

// switchableProxy forwards to target only while up (including WebSockets).
type switchableProxy struct {
	up    atomic.Bool
	proxy *httputil.ReverseProxy
}

func (p *switchableProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !p.up.Load() {
		if hj, ok := w.(http.Hijacker); ok {
			c, _, _ := hj.Hijack()
			_ = c.Close() // like an unreachable host: no HTTP answer
		}
		return
	}
	p.proxy.ServeHTTP(w, r)
}

func TestReturnsToLocalAfterThreeGoodProbes(t *testing.T) {
	r := newRig(t)
	target, _ := url.Parse(r.srv.URL())
	px := &switchableProxy{proxy: httputil.NewSingleHostReverseProxy(target)}
	local := &http.Server{Handler: px}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { _ = local.Serve(l) }()
	t.Cleanup(func() { _ = local.Close() })
	r.cli.cfg.Trust.URLs = []trust.URL{{URL: "http://" + l.Addr().String(), Kind: "internal"}, {URL: r.srv.URL(), Kind: "cloud"}}
	r.cli.cfg.LocalProbeEvery = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.cli.Run(ctx) }()
	waitFor(t, "remote while away", func() bool { return r.cli.Status().State == "connected_remote" })
	px.up.Store(true) // home again
	start := time.Now()
	waitFor(t, "back on local", func() bool { return r.cli.Status().State == "connected_local" })
	if d := time.Since(start); d < 250*time.Millisecond {
		t.Fatalf("switched after %v: needs 3 consecutive good probes", d)
	}
	h := hellos(r.srv)
	if h[0]["via"] != "cloud" || h[len(h)-1]["via"] != "internal" {
		t.Fatalf("hellos %+v", h)
	}
}
