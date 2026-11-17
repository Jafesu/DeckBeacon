package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/fsutil"
)

// ApplyConfig drives the independent updater process: `deckbeacon update
// apply`, started as a transient user unit by the running agent with the
// current (known-good) binary, so it survives the agent's restart.
type ApplyConfig struct {
	Layout           Layout
	To, From         string
	AdoptExe         string // set when the running install is not yet managed
	RequireConnected bool   // the old agent was connected: the new one must reconnect
	StatusPath       string // the agent's status.json
	StateDir         string // where the result file goes
	Restart          func(ctx context.Context) error
	ReinstallUnit    func(ctx context.Context, exe string) error // point the unit at Layout.Bin()
	HealthTimeout    time.Duration                               // default 120 s
	Poll             time.Duration                               // default 1 s
	Now              func() time.Time
}

// AgentStatus is the slice of status.json the health check reads.
type AgentStatus struct {
	State   string `json:"state"`
	Version string `json:"version"`
	PID     int    `json:"pid"`
}

func readStatus(path string) AgentStatus {
	var s AgentStatus
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// ErrUpdateInProgress means another updater holds the lock.
var ErrUpdateInProgress = errors.New("update: another update is already in progress")

// lockUpdater takes an exclusive, non-blocking lock in the state directory,
// held for the updater's lifetime (released by the kernel if it dies).
func lockUpdater(stateDir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(stateDir, "update.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrUpdateInProgress
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

// Apply switches to cfg.To, restarts, health-checks and rolls back on failure.
// It writes a Result for the next agent process to report, except when another
// updater is running: then it changes nothing and returns ErrUpdateInProgress.
func Apply(ctx context.Context, cfg ApplyConfig) (res Result, err error) {
	unlock, err := lockUpdater(cfg.StateDir)
	if err != nil {
		return Result{From: cfg.From, To: cfg.To, Outcome: "failed", Reason: err.Error()}, err
	}
	defer unlock()
	if cfg.HealthTimeout <= 0 {
		cfg.HealthTimeout = 120 * time.Second
	}
	if cfg.Poll <= 0 {
		cfg.Poll = time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	l := cfg.Layout
	res = Result{From: cfg.From, To: cfg.To}
	defer func() {
		res.At = cfg.Now().UTC()
		if err != nil && res.Outcome == "" {
			res.Outcome, res.Reason = "failed", err.Error()
		}
		b, _ := json.Marshal(res)
		_ = fsutil.WriteFileAtomic(filepath.Join(cfg.StateDir, "update-result.json"), b, 0o600)
	}()
	if !validVersionName(cfg.To) || !l.Has(cfg.To) {
		return res, fmt.Errorf("version %s is not staged", cfg.To)
	}
	prev := l.Active()
	adopting := cfg.AdoptExe != ""
	if adopting {
		if err := l.Adopt(cfg.AdoptExe, cfg.From); err != nil {
			return res, fmt.Errorf("adopting the current install: %w", err)
		}
		prev = cfg.From
	}
	if prev == "" {
		return res, errors.New("no current version to fall back to")
	}
	if prev == cfg.To {
		return res, fmt.Errorf("version %s is already active; nothing to switch or roll back to", cfg.To)
	}
	oldPID := readStatus(cfg.StatusPath).PID
	if err := l.Switch(cfg.To, prev); err != nil {
		return res, err
	}
	if adopting {
		err = cfg.ReinstallUnit(ctx, l.Bin())
	} else {
		err = cfg.Restart(ctx)
	}
	if err == nil {
		err = waitHealthy(ctx, cfg, oldPID)
	}
	if err == nil {
		l.Prune(cfg.To, prev)
		res.Outcome = "completed"
		return res, nil
	}
	// Roll back: the previous version is still installed and the unit points
	// at bin/deckbeacon, so flipping the link and restarting restores it.
	reason := err.Error()
	if rerr := symlinkAtomic(l.linkTarget(prev), l.Bin()); rerr != nil {
		res.Outcome, res.Reason = "failed", reason+"; rollback failed: "+rerr.Error()
		return res, err
	}
	if rerr := cfg.Restart(ctx); rerr != nil {
		res.Outcome, res.Reason = "failed", reason+"; restart after rollback failed: "+rerr.Error()
		return res, err
	}
	res.Outcome, res.Reason = "rolled_back", reason
	return res, err
}

// waitHealthy: the new process writes status.json with the new version and a
// new pid, stays up, and (if required) connects to Home Assistant.
func waitHealthy(ctx context.Context, cfg ApplyConfig, oldPID int) error {
	deadline := cfg.Now().Add(cfg.HealthTimeout)
	var last AgentStatus
	for cfg.Now().Before(deadline) {
		s := readStatus(cfg.StatusPath)
		last = s
		started := s.Version == cfg.To && s.PID != 0 && s.PID != oldPID
		connected := strings.HasPrefix(s.State, "connected_")
		if started && (!cfg.RequireConnected || connected) && processAlive(s.PID) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(cfg.Poll):
		}
	}
	switch {
	case last.Version != cfg.To || last.PID == oldPID:
		return fmt.Errorf("version %s did not start within %s", cfg.To, cfg.HealthTimeout)
	case cfg.RequireConnected:
		return fmt.Errorf("version %s did not reconnect to Home Assistant within %s (state %q)", cfg.To, cfg.HealthTimeout, last.State)
	default:
		return fmt.Errorf("version %s is not healthy", cfg.To)
	}
}

// processAlive is a variable so tests can simulate pids.
var processAlive = func(pid int) bool {
	return pid > 0 && func() bool { _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); return err == nil }()
}
