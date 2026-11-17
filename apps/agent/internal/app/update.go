package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/buildinfo"
	"github.com/jafesu/deckbeacon/apps/agent/internal/release"
	"github.com/jafesu/deckbeacon/apps/agent/internal/service"
	"github.com/jafesu/deckbeacon/apps/agent/internal/update"
)

// installLayout is the managed install tree (~/.local/share/deckbeacon).
func installLayout() update.Layout {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		if u, err := user.Current(); err == nil {
			base = filepath.Join(u.HomeDir, ".local", "share")
		}
	}
	return update.Layout{Dir: filepath.Join(base, "deckbeacon")}
}

// runningExe is the real file of the running binary (kept as the updater and
// rollback target even after bin/deckbeacon is flipped).
func runningExe() string {
	exe, _ := os.Executable()
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return exe
}

// startUpdater runs `<current exe> update apply …` as a transient user unit,
// independent of deckbeacon.service so it survives the agent's restart.
func startUpdater(e Env) func(ctx context.Context, args []string) error {
	return func(ctx context.Context, args []string) error {
		unit := "deckbeacon-updater-" + strconv.FormatInt(time.Now().Unix(), 10)
		cmd := append([]string{"--user", "--unit=" + unit, "--collect", "--quiet", "--", runningExe()}, args...)
		out, err := e.Host.Run(ctx, "systemd-run", cmd...)
		if err != nil {
			return fmt.Errorf("systemd-run: %v: %s", err, strings.TrimSpace(string(out)))
		}
		e.Log.Info("updater started", "unit", unit, "args", strings.Join(args, " "))
		return nil
	}
}

func newUpdateClient(e Env, busy func() (bool, string), connected func() bool, integration func() string) *update.Client {
	return update.New(update.Config{
		Layout:       installLayout(),
		StateDir:     e.Dirs.State,
		ConfigDir:    e.Dirs.Config,
		Current:      buildinfo.Version,
		CurrentExe:   runningExe(),
		Keys:         release.TrustedKeys(),
		Integration:  integration,
		Busy:         busy,
		Connected:    connected,
		StartUpdater: startUpdater(e),
		Log:          e.Log,
	})
}

// Update implements `deckbeacon update status|check|on|off|rollback|apply`.
func Update(ctx context.Context, e Env, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: deckbeacon update status|check|on|off|rollback")
	}
	l := installLayout()
	switch args[0] {
	case "on", "off":
		if err := update.SetEnabled(e.Dirs.Config, args[0] == "on"); err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "Managed updates: %s. Restart the agent to apply: systemctl --user restart deckbeacon.service\n", args[0])
		return nil
	case "status":
		c := newUpdateClient(e, nil, nil, nil)
		fmt.Fprintf(e.Stdout, "Running:   %s (%s)\n", buildinfo.Version, runningExe())
		fmt.Fprintf(e.Stdout, "Enabled:   %v\n", c.Enabled())
		if l.Managed() {
			fmt.Fprintf(e.Stdout, "Installed: active %s, previous %s (%s)\n", l.Active(), orNone(l.Previous()), l.Dir)
		} else {
			fmt.Fprintf(e.Stdout, "Installed: not managed yet (adopted on the first update)\n")
		}
		return nil
	case "check":
		c := newUpdateClient(e, nil, nil, nil)
		if len(args) > 2 && args[1] == "--channel" {
			c.SetChannel(args[2])
		}
		_ = c.Check(ctx)
		for _, m := range c.Metrics() {
			if m.Value != nil {
				fmt.Fprintf(e.Stdout, "%s: %v\n", m.Key, m.Value)
			}
		}
		return nil
	case "rollback":
		prev := l.Previous()
		if prev == "" {
			return update.ErrNotManaged
		}
		if err := l.Switch(prev, l.Active()); err != nil {
			return err
		}
		if _, err := e.Host.Run(ctx, "systemctl", "--user", "restart", service.UnitName); err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "Rolled back to %s and restarted the agent.\n", prev)
		return nil
	case "apply":
		return applyCmd(ctx, e, l, args[1:])
	}
	return fmt.Errorf("unknown update command %q", args[0])
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// applyCmd is the independent updater process (started via systemd-run).
func applyCmd(ctx context.Context, e Env, l update.Layout, args []string) error {
	fs := flag.NewFlagSet("update apply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	to := fs.String("to", "", "")
	from := fs.String("from", "", "")
	adopt := fs.String("adopt", "", "")
	needConn := fs.Bool("require-connected", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, err := serviceManager(e)
	if err != nil {
		return err
	}
	res, err := update.Apply(ctx, update.ApplyConfig{
		Layout: l, To: *to, From: *from, AdoptExe: *adopt, RequireConnected: *needConn,
		StatusPath: filepath.Join(e.Dirs.Runtime, "status.json"), StateDir: e.Dirs.State,
		Restart: func(ctx context.Context) error {
			_, err := e.Host.Run(ctx, "systemctl", "--user", "restart", service.UnitName)
			return err
		},
		ReinstallUnit: func(ctx context.Context, exe string) error {
			svc.Exe = exe
			_, err := svc.Install(ctx, false) // rewrites ExecStart, daemon-reload, restart; lingering untouched
			return err
		},
	})
	e.Log.Info("update apply finished", "outcome", res.Outcome, "to", res.To, "reason", res.Reason)
	return err
}
