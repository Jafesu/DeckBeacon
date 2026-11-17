package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/dispatch"
	"github.com/jafesu/deckbeacon/apps/agent/internal/release"
)

// fakeBinary is a tiny executable that answers `version --json` like the agent.
func fakeBinary(version string) []byte {
	return []byte(fmt.Sprintf("#!/bin/sh\necho '{\"version\":\"%s\"}'\n", version))
}

type relSpec struct {
	version        string
	minIntegration string
	tamperSig      bool
	tamperBinary   bool
}

// releaseServer serves a GitHub-style release list plus assets over TLS.
type releaseServer struct {
	*httptest.Server
	mu     sync.Mutex
	files  map[string][]byte
	list   []ghRelease
	served map[string]int
}

func newReleaseServer(t *testing.T, priv ed25519.PrivateKey, specs ...relSpec) *releaseServer {
	t.Helper()
	rs := &releaseServer{files: map[string][]byte{}, served: map[string]int{}}
	rs.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		defer rs.mu.Unlock()
		rs.served[r.URL.Path]++
		if r.URL.Path == "/releases" {
			_ = json.NewEncoder(w).Encode(rs.list)
			return
		}
		b, ok := rs.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(rs.Close)
	dir := t.TempDir()
	for _, s := range specs {
		bin := filepath.Join(dir, s.version)
		_ = os.WriteFile(bin, fakeBinary(s.version), 0o755)
		minInt := s.minIntegration
		if minInt == "" {
			minInt = "0.2.0"
		}
		man, err := release.BuildManifest(s.version, 1, minInt, "https://example.invalid/notes/"+s.version, map[string]string{release.AssetName("amd64"): bin}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		sig := release.Sign(man, priv)
		if s.tamperSig {
			man = []byte(strings.Replace(string(man), `"min_protocol": 1`, `"min_protocol": 1 `, 1))
		}
		binary := fakeBinary(s.version)
		if s.tamperBinary {
			binary = append(binary, []byte("# evil\n")...)
		}
		base := "/dl/" + s.version + "/"
		rs.files[base+"release.json"] = man
		rs.files[base+"release.json.sig"] = sig
		rs.files[base+release.AssetName("amd64")] = binary
		gr := ghRelease{TagName: "agent/v" + s.version, Prerelease: strings.Contains(s.version, "-")}
		for _, n := range []string{"release.json", "release.json.sig", release.AssetName("amd64")} {
			gr.Assets = append(gr.Assets, struct {
				Name string `json:"name"`
				URL  string `json:"browser_download_url"`
				Size int64  `json:"size"`
			}{n, rs.URL + base + n, int64(len(rs.files[base+n]))})
		}
		rs.list = append(rs.list, gr)
	}
	return rs
}

type rig struct {
	c        *Client
	layout   Layout
	state    string
	started  [][]string
	busy     bool
	busyWhy  string
	conn     bool
	integ    string
	events   []Event
	clock    time.Time
	srv      *releaseServer
	pub      ed25519.PublicKey
	priv     ed25519.PrivateKey
	agentBin string
}

func newRig(t *testing.T, specs ...relSpec) *rig {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	r := &rig{pub: pub, priv: priv, integ: "0.2.0", clock: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	r.srv = newReleaseServer(t, priv, specs...)
	root := t.TempDir()
	r.layout = Layout{Dir: filepath.Join(root, "share")}
	r.state = filepath.Join(root, "state")
	_ = os.MkdirAll(r.state, 0o700)
	r.agentBin = filepath.Join(root, "src-build", "deckbeacon") // an unmanaged from-source install
	_ = os.MkdirAll(filepath.Dir(r.agentBin), 0o755)
	_ = os.WriteFile(r.agentBin, fakeBinary("0.0.0-dev"), 0o755)
	r.c = New(Config{
		Layout: r.layout, StateDir: r.state, ConfigDir: filepath.Join(root, "config"), Current: "0.0.0-dev", CurrentExe: r.agentBin, GOARCH: "amd64",
		ReleasesURL: r.srv.URL + "/releases", HTTP: r.srv.Client(), Keys: []ed25519.PublicKey{pub},
		Integration: func() string { return r.integ }, Busy: func() (bool, string) { return r.busy, r.busyWhy }, Connected: func() bool { return r.conn },
		StartUpdater: func(ctx context.Context, args []string) error { r.started = append(r.started, args); return nil },
		Now:          func() time.Time { return r.clock },
	})
	return r
}

func (r *rig) drain() []Event {
	for {
		select {
		case e := <-r.c.Events():
			r.events = append(r.events, e)
		default:
			return r.events
		}
	}
}

func (r *rig) metric(key string) any {
	for _, m := range r.c.Metrics() {
		if m.Key == key {
			return m.Value
		}
	}
	return "missing"
}

func TestCheckChannelsAndAnnounceOnce(t *testing.T) {
	r := newRig(t, relSpec{version: "0.3.0"}, relSpec{version: "0.4.0-beta.1"}, relSpec{version: "0.4.1-dev.0"})
	ctx := context.Background()
	if err := r.c.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if r.metric("agent.update.state") != StateAvailable || r.metric("agent.update.latest") != "0.3.0" || r.metric("agent.update.notes_url") != "https://example.invalid/notes/0.3.0" {
		t.Fatalf("stable: %v %v", r.metric("agent.update.state"), r.metric("agent.update.latest"))
	}
	_ = r.c.Check(ctx)
	if ev := r.drain(); len(ev) != 1 || ev[0].Type != "agent_update_available" || ev[0].Data["version"] != "0.3.0" {
		t.Fatalf("announce once: %+v", ev)
	}
	r.c.SetChannel("beta")
	_ = r.c.Check(ctx)
	if r.metric("agent.update.latest") != "0.4.0-beta.1" {
		t.Fatalf("beta: %v", r.metric("agent.update.latest"))
	}
	r.c.SetChannel("dev")
	_ = r.c.Check(ctx)
	if r.metric("agent.update.latest") != "0.4.1-dev.0" {
		t.Fatalf("dev: %v", r.metric("agent.update.latest"))
	}
	// The channel survives a restart.
	if New(Config{StateDir: r.state}).channel != "dev" {
		t.Fatal("channel not persisted")
	}
}

func TestCheckRefusesBadReleases(t *testing.T) {
	// Newest is tampered, next needs a newer integration: falls back to 0.3.0.
	r := newRig(t, relSpec{version: "0.5.0", tamperSig: true}, relSpec{version: "0.4.0", minIntegration: "9.0.0"}, relSpec{version: "0.3.0"})
	if err := r.c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.metric("agent.update.latest") != "0.3.0" {
		t.Fatalf("latest %v (tampered and incompatible releases must be skipped)", r.metric("agent.update.latest"))
	}
	// Only a bad release: check_failed with the reason, nothing offered.
	r2 := newRig(t, relSpec{version: "0.5.0", tamperSig: true})
	_ = r2.c.Check(context.Background())
	if st, why := r2.c.State(); st != StateCheckFailed || !strings.Contains(why, "signature") {
		t.Fatalf("%s %s", st, why)
	}
	if r2.metric("agent.update.latest") != nil {
		t.Fatal("unverified release offered")
	}
	// No keys in the build: unavailable, nothing fetched.
	r3 := newRig(t, relSpec{version: "0.3.0"})
	r3.c.cfg.Keys = nil
	_ = r3.c.Check(context.Background())
	if st, _ := r3.c.State(); st != StateUnavailable || r3.srv.served["/releases"] != 0 {
		t.Fatalf("%s fetched=%d", st, r3.srv.served["/releases"])
	}
	// Turned off: no network.
	r4 := newRig(t, relSpec{version: "0.3.0"})
	_ = SetEnabled(r4.c.cfg.ConfigDir, false)
	_ = r4.c.Check(context.Background())
	if st, _ := r4.c.State(); st != StateDisabled || r4.srv.served["/releases"] != 0 {
		t.Fatalf("%s", st)
	}
}

func cmd(args string, confirmed bool) dispatch.Command {
	return dispatch.Command{CommandID: "C1", Name: "approve_agent_update", Args: json.RawMessage(args), ConfirmedByUser: confirmed}
}

func TestApprovalRules(t *testing.T) {
	r := newRig(t, relSpec{version: "0.3.0"})
	h := r.c.ApproveHandler()
	_ = r.c.Check(context.Background())
	for args, code := range map[string]string{`{}`: "invalid_args", `{"version":"0.2.9"}`: "unavailable"} {
		if res, _ := h(context.Background(), cmd(args, true)); res.State != "rejected" || res.Error.Code != code {
			t.Errorf("%s: %+v", args, res)
		}
	}
	if res, _ := h(context.Background(), cmd(`{"version":"0.3.0"}`, false)); res.Error == nil || res.Error.Code != "confirmation_required" {
		t.Fatalf("unconfirmed approval accepted: %+v", res)
	}
	if res, _ := h(context.Background(), cmd(`{"version":"0.3.0"}`, true)); res.State != "confirmed" {
		t.Fatalf("%+v", res)
	}
	// Survives a restart, expires after 7 days.
	if a, ok := New(Config{StateDir: r.state, Now: func() time.Time { return r.clock }}).approval(); !ok || a.Version != "0.3.0" {
		t.Fatal("approval not persisted")
	}
	later := New(Config{StateDir: r.state, Now: func() time.Time { return r.clock.Add(8 * 24 * time.Hour) }})
	if _, ok := later.approval(); ok {
		t.Fatal("approval must expire")
	}
}

func TestStepWaitsForSafeWindowThenStagesAndHandsOver(t *testing.T) {
	r := newRig(t, relSpec{version: "0.3.0"})
	ctx := context.Background()
	_ = r.c.Check(ctx)
	if err := r.c.Approve("0.3.0"); err != nil {
		t.Fatal(err)
	}
	r.busy, r.busyWhy = true, "a game is running"
	r.c.step(ctx)
	if st, why := r.c.State(); st != StateWaiting || why != "a game is running" || len(r.started) != 0 {
		t.Fatalf("must wait: %s %s %v", st, why, r.started)
	}
	r.busy = false
	r.conn = true
	r.c.step(ctx)
	if len(r.started) != 1 {
		t.Fatalf("updater not started: %s", func() string { s, w := r.c.State(); return s + " " + w }())
	}
	args := strings.Join(r.started[0], " ")
	if !strings.Contains(args, "update apply --to 0.3.0 --from 0.0.0-dev --require-connected --adopt "+r.agentBin) {
		t.Fatalf("updater args %q", args)
	}
	if !r.layout.Has("0.3.0") {
		t.Fatal("verified binary not staged")
	}
	ev := r.drain()
	if ev[len(ev)-1].Type != "agent_update_started" {
		t.Fatalf("events %+v", ev)
	}
}

func TestStepRefusesTamperedBinary(t *testing.T) {
	r := newRig(t, relSpec{version: "0.3.0", tamperBinary: true})
	ctx := context.Background()
	_ = r.c.Check(ctx)
	_ = r.c.Approve("0.3.0")
	r.c.step(ctx)
	if st, why := r.c.State(); st != StateFailed || !strings.Contains(why, "checksum") && !strings.Contains(why, "bytes") {
		t.Fatalf("%s %s", st, why)
	}
	if len(r.started) != 0 || r.layout.Has("0.3.0") {
		t.Fatal("tampered binary must never be staged or applied")
	}
	if _, ok := r.c.approval(); ok {
		t.Fatal("approval must be cleared after a failure (no automatic retry)")
	}
	ev := r.drain()
	if last := ev[len(ev)-1]; last.Type != "agent_update_failed" || last.Data["rolled_back"] != false {
		t.Fatalf("%+v", ev)
	}
}

// ---- the independent updater ----

type applyRig struct {
	cfg      ApplyConfig
	status   string
	restarts int
	unitExe  string
	pid      int
	behave   func(version string) // what the "new process" does after a restart
}

func newApplyRig(t *testing.T) *applyRig {
	root := t.TempDir()
	a := &applyRig{status: filepath.Join(root, "status.json"), pid: 100}
	l := Layout{Dir: filepath.Join(root, "share")}
	writeStatus := func(version, state string) {
		a.pid++
		b, _ := json.Marshal(AgentStatus{State: state, Version: version, PID: a.pid})
		_ = os.WriteFile(a.status, b, 0o600)
	}
	a.behave = func(v string) { writeStatus(v, "connected_local") }
	restart := func(ctx context.Context) error {
		a.restarts++
		a.behave(l.Active())
		return nil
	}
	a.cfg = ApplyConfig{
		Layout: l, StatusPath: a.status, StateDir: root, Restart: restart, HealthTimeout: 300 * time.Millisecond, Poll: 10 * time.Millisecond,
		ReinstallUnit: func(ctx context.Context, exe string) error { a.unitExe = exe; return restart(ctx) },
	}
	writeStatus("0.0.0-dev", "connected_local")
	processAlive = func(pid int) bool { return pid > 0 }
	t.Cleanup(func() { processAlive = func(pid int) bool { return pid > 0 } })
	return a
}

func (a *applyRig) stage(t *testing.T, v string) {
	src := filepath.Join(t.TempDir(), "bin")
	_ = os.WriteFile(src, fakeBinary(v), 0o755)
	if err := a.cfg.Layout.install(src, v); err != nil {
		t.Fatal(err)
	}
}

func TestApplyAdoptsSwitchesAndKeepsRollbackTarget(t *testing.T) {
	a := newApplyRig(t)
	cur := filepath.Join(t.TempDir(), "deckbeacon")
	_ = os.WriteFile(cur, fakeBinary("0.0.0-dev"), 0o755)
	a.stage(t, "0.3.0")
	cfg := a.cfg
	cfg.To, cfg.From, cfg.AdoptExe, cfg.RequireConnected = "0.3.0", "0.0.0-dev", cur, true
	res, err := Apply(context.Background(), cfg)
	if err != nil || res.Outcome != "completed" {
		t.Fatalf("%+v %v", res, err)
	}
	l := cfg.Layout
	if l.Active() != "0.3.0" || l.Previous() != "0.0.0-dev" || a.unitExe != l.Bin() {
		t.Fatalf("active=%s previous=%s unit=%s", l.Active(), l.Previous(), a.unitExe)
	}
	if _, err := os.Stat(cur); err != nil {
		t.Fatal("the original from-source binary must stay in place")
	}
	var written Result
	b, _ := os.ReadFile(filepath.Join(cfg.StateDir, "update-result.json"))
	_ = json.Unmarshal(b, &written)
	if written.Outcome != "completed" || written.To != "0.3.0" {
		t.Fatalf("result file %+v", written)
	}
}

func TestApplyRollsBackWhenTheNewVersionCrashes(t *testing.T) {
	a := newApplyRig(t)
	a.stage(t, "0.3.0")
	a.stage(t, "0.4.0")
	l := a.cfg.Layout
	_ = l.Switch("0.3.0", "")
	a.behave = func(v string) {
		if v == "0.3.0" { // only the old version comes back up
			a.pid++
			b, _ := json.Marshal(AgentStatus{State: "connected_local", Version: v, PID: a.pid})
			_ = os.WriteFile(a.status, b, 0o600)
		}
	}
	cfg := a.cfg
	cfg.To, cfg.From = "0.4.0", "0.3.0"
	res, err := Apply(context.Background(), cfg)
	if err == nil || res.Outcome != "rolled_back" || !strings.Contains(res.Reason, "did not start") {
		t.Fatalf("%+v %v", res, err)
	}
	if l.Active() != "0.3.0" || a.restarts != 2 {
		t.Fatalf("active=%s restarts=%d", l.Active(), a.restarts)
	}
}

func TestApplyRollsBackWithoutReconnect(t *testing.T) {
	a := newApplyRig(t)
	a.stage(t, "0.3.0")
	a.stage(t, "0.4.0")
	_ = a.cfg.Layout.Switch("0.3.0", "")
	a.behave = func(v string) {
		a.pid++
		state := "connected_local"
		if v == "0.4.0" {
			state = "reconnecting" // starts, but its networking is broken
		}
		b, _ := json.Marshal(AgentStatus{State: state, Version: v, PID: a.pid})
		_ = os.WriteFile(a.status, b, 0o600)
	}
	cfg := a.cfg
	cfg.To, cfg.From, cfg.RequireConnected = "0.4.0", "0.3.0", true
	res, _ := Apply(context.Background(), cfg)
	if res.Outcome != "rolled_back" || !strings.Contains(res.Reason, "did not reconnect") || cfg.Layout.Active() != "0.3.0" {
		t.Fatalf("%+v active=%s", res, cfg.Layout.Active())
	}
	// Offline devices don't need to reconnect.
	cfg.RequireConnected = false
	if res, err := Apply(context.Background(), cfg); err != nil || res.Outcome != "completed" {
		t.Fatalf("offline: %+v %v", res, err)
	}
}

func TestReportResultEmitsOnceAndClearsApproval(t *testing.T) {
	r := newRig(t, relSpec{version: "0.3.0"})
	_ = r.c.Check(context.Background())
	_ = r.c.Approve("0.3.0")
	b, _ := json.Marshal(Result{From: "0.3.0", To: "0.4.0", Outcome: "rolled_back", Reason: "did not start"})
	_ = os.WriteFile(filepath.Join(r.state, "update-result.json"), b, 0o600)
	r.c.ReportResult()
	r.c.ReportResult()
	var failed []Event
	for _, e := range r.drain() {
		if e.Type == "agent_update_failed" {
			failed = append(failed, e)
		}
	}
	if len(failed) != 1 || failed[0].Data["rolled_back"] != true || failed[0].Data["reason"] != "did not start" {
		t.Fatalf("%+v", failed)
	}
	if _, ok := r.c.approval(); ok {
		t.Fatal("an attempted version must not be retried automatically")
	}
	if st, _ := r.c.State(); st != StateFailed {
		t.Fatal(st)
	}
}
