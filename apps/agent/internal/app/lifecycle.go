package app

import (
	"context"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/collect"
	"github.com/jafesu/deckbeacon/apps/agent/internal/lifecycle"
	"github.com/jafesu/deckbeacon/apps/agent/internal/service"
	"github.com/jafesu/deckbeacon/apps/agent/internal/store"
)

// startLifecycle connects to logind (unless DECKBEACON_LOGIND_ADDRESS=none) and
// always runs the clock-jump fallback. Clock resumes that duplicate a logind
// resume are dropped.
func startLifecycle(ctx context.Context, log *slog.Logger) (<-chan lifecycle.Event, lifecycle.Capabilities, func()) {
	caps := lifecycle.Capabilities{ResumeDetect: "heuristic"}
	var logind *lifecycle.Logind
	switch addr := os.Getenv("DECKBEACON_LOGIND_ADDRESS"); addr {
	case "none":
		caps.Reason = "logind disabled by configuration"
	default:
		l, err := lifecycle.ConnectLogind(addr)
		if err != nil {
			caps.Reason = err.Error()
			log.Warn("logind unavailable; sleep/shutdown cannot be announced, resume is detected heuristically", "err", err)
		} else {
			logind = l
			caps.SleepNotify, caps.ShutdownNotify, caps.ResumeDetect = true, true, "logind"
		}
	}
	clock := lifecycle.NewClockWatch()
	go clock.Run(ctx)
	out := make(chan lifecycle.Event, 8)
	var logindCh <-chan lifecycle.Event
	if logind != nil {
		logindCh = logind.Events()
	}
	go func() {
		var lastLogindResume time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-logindCh:
				if ev.Kind == lifecycle.Resume {
					lastLogindResume = time.Now()
				}
				out <- ev
			case ev := <-clock.Events():
				if logind != nil && time.Since(lastLogindResume) < 2*time.Minute {
					continue // already reported (confirmed) by logind
				}
				out <- ev
			}
		}
	}()
	stop := func() {
		if logind != nil {
			_ = logind.Close()
		}
	}
	return out, caps, stop
}

func lifecycleCapabilities(lc lifecycle.Capabilities, svc service.Manager) []collect.Capability {
	avail := func(ok bool) string {
		if ok {
			return "available"
		}
		return "unavailable"
	}
	resume := collect.Capability{ID: "lifecycle.resume_detect", Status: "available", Tier: "system", Source: "logind PrepareForSleep", Privilege: "none"}
	if lc.ResumeDetect != "logind" {
		resume.Source, resume.Reason = "wall/monotonic clock divergence", "heuristic: logind unavailable"
	}
	boot := collect.Capability{ID: "service.start_at_boot", Status: "disabled", Tier: "system", Source: "systemd user lingering", Privilege: "user", Reason: "opt-in: deckbeacon service boot on"}
	if svc.Lingering() {
		boot.Status, boot.Reason = "available", ""
	}
	return []collect.Capability{
		{ID: "lifecycle.sleep_notify", Status: avail(lc.SleepNotify), Tier: "system", Source: "logind delay inhibitor", Privilege: "none", Reason: lc.Reason},
		{ID: "lifecycle.shutdown_notify", Status: avail(lc.ShutdownNotify), Tier: "system", Source: "logind delay inhibitor", Privilege: "none", Reason: lc.Reason},
		resume, boot,
	}
}

// detectBoot queues device_booted once per new kernel boot_id.
func detectBoot(st *store.Store, bootID string, bootTime time.Time, now time.Time) (bool, error) {
	prev, err := st.Meta("boot_id")
	if err != nil || prev == bootID {
		return false, err
	}
	data := `{"boot_time":"` + bootTime.UTC().Format(time.RFC3339) + `"`
	if prev != "" {
		data += `,"previous_boot_id":"` + prev + `"`
	}
	data += "}"
	if _, err := st.AppendEvent(store.Event{EventID: ulidNew(), EventType: "device_booted", OccurredAt: now.UTC(), Live: true, Data: []byte(data)}); err != nil {
		return false, err
	}
	return true, st.SetMeta("boot_id", bootID)
}

// serviceManager builds the service manager for this user and binary.
func serviceManager(e Env) (service.Manager, error) {
	exe, err := os.Executable()
	if err != nil {
		return service.Manager{}, err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	u, err := user.Current()
	if err != nil {
		return service.Manager{}, err
	}
	cfgHome := os.Getenv("XDG_CONFIG_HOME")
	if cfgHome == "" {
		cfgHome = filepath.Join(u.HomeDir, ".config")
	}
	return service.Manager{Root: e.Host.Root, UnitDir: filepath.Join(cfgHome, "systemd", "user"), StateDir: e.Dirs.State, Exe: exe, User: u.Username, Run: e.Host.Run}, nil
}
