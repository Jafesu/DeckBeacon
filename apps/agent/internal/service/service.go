// Package service manages the agent's systemd --user unit and the opt-in
// "start at boot" lingering (ADR-0003). It never uses sudo, pkexec or system
// units: lingering for one's own user is granted by logind's polkit policy
// (org.freedesktop.login1.set-self-linger), and SteamOS overrides are checked
// on hardware (HV-S1).
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jafesu/deckbeacon/apps/agent/internal/fsutil"
)

// UnitName is the systemd user unit.
const UnitName = "deckbeacon.service"

// Runner executes an external command (mockable).
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Manager installs and inspects the user service.
type Manager struct {
	Root     string // filesystem root for /var/lib/systemd/linger (tests)
	UnitDir  string // ~/.config/systemd/user
	StateDir string // agent state dir (linger ownership marker)
	Exe      string // absolute path of the agent binary
	User     string
	Run      Runner
}

const lingerMarker = "linger-enabled-by-deckbeacon"

// Status of the service.
type Status struct {
	UnitInstalled bool   `json:"unit_installed"`
	UnitCurrent   bool   `json:"unit_current"`
	Enabled       string `json:"enabled"` // systemctl is-enabled output
	Active        string `json:"active"`  // systemctl is-active output
	StartAtBoot   bool   `json:"start_at_boot"`
	LingerOwned   bool   `json:"linger_enabled_by_deckbeacon"`
}

// ErrBadPath is returned for executable paths systemd cannot represent safely.
var ErrBadPath = errors.New("agent path contains characters that cannot be used in a unit file")

// Render returns the unit file for exe.
func Render(exe string) (string, error) {
	if !filepath.IsAbs(exe) || strings.ContainsAny(exe, "\"\\\n\r\t") {
		return "", ErrBadPath
	}
	exec := `"` + strings.ReplaceAll(exe, "%", "%%") + `" run`
	return strings.Replace(unitTemplate, "@EXEC@", exec, 1), nil
}

const unitTemplate = `[Unit]
Description=DeckBeacon agent (Home Assistant companion for SteamOS)
Documentation=https://github.com/Jafesu/DeckBeacon
After=network-online.target
Wants=network-online.target
# Backoff instead of restart storms (spec 03): at most 5 starts per 5 minutes.
StartLimitIntervalSec=300
StartLimitBurst=5

[Service]
Type=simple
ExecStart=@EXEC@
Restart=on-failure
RestartSec=5s
# User-service hardening that does not need privileges.
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=-%h/.local/state/deckbeacon -%h/.config/deckbeacon -%t
UMask=0077

[Install]
WantedBy=default.target
`

func (m Manager) unitPath() string { return filepath.Join(m.UnitDir, UnitName) }

func (m Manager) lingerFile() string {
	return filepath.Join(m.Root, "var/lib/systemd/linger", m.User)
}

// Lingering reports whether start-at-boot (linger) is enabled for the user.
func (m Manager) Lingering() bool {
	_, err := os.Stat(m.lingerFile())
	return err == nil
}

func (m Manager) systemctl(ctx context.Context, args ...string) (string, error) {
	out, err := m.Run(ctx, "systemctl", append([]string{"--user"}, args...)...)
	return strings.TrimSpace(string(out)), err
}

// Install writes the unit (if changed), enables and starts it, and with atBoot
// enables lingering. It returns a human-readable list of changes made.
func (m Manager) Install(ctx context.Context, atBoot bool) ([]string, error) {
	var changes []string
	unit, err := Render(m.Exe)
	if err != nil {
		return nil, err
	}
	old, err := os.ReadFile(m.unitPath())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if !bytes.Equal(old, []byte(unit)) {
		if err := os.MkdirAll(m.UnitDir, 0o755); err != nil {
			return nil, err
		}
		if err := fsutil.WriteFileAtomic(m.unitPath(), []byte(unit), 0o644); err != nil {
			return nil, err
		}
		changes = append(changes, "wrote "+m.unitPath())
		if _, err := m.systemctl(ctx, "daemon-reload"); err != nil {
			return changes, fmt.Errorf("systemctl --user daemon-reload: %w", err)
		}
	}
	if st, _ := m.systemctl(ctx, "is-enabled", UnitName); st != "enabled" {
		if _, err := m.systemctl(ctx, "enable", "--now", UnitName); err != nil {
			return changes, fmt.Errorf("systemctl --user enable --now: %w", err)
		}
		changes = append(changes, "enabled and started "+UnitName)
	} else if len(changes) > 0 {
		if _, err := m.systemctl(ctx, "restart", UnitName); err != nil {
			return changes, fmt.Errorf("systemctl --user restart: %w", err)
		}
		changes = append(changes, "restarted "+UnitName)
	}
	if atBoot && !m.Lingering() {
		if _, err := m.Run(ctx, "loginctl", "enable-linger"); err != nil {
			return changes, fmt.Errorf("loginctl enable-linger failed (SteamOS may restrict self-linger; see docs/agent-lifecycle.md): %w", err)
		}
		_ = fsutil.WriteFileAtomic(filepath.Join(m.StateDir, lingerMarker), []byte("1\n"), 0o600)
		changes = append(changes, "enabled start at boot (lingering)")
	}
	return changes, nil
}

// SetStartAtBoot toggles lingering. Disabling only touches lingering that
// DeckBeacon enabled, unless force is set.
func (m Manager) SetStartAtBoot(ctx context.Context, on, force bool) ([]string, error) {
	marker := filepath.Join(m.StateDir, lingerMarker)
	if on {
		if m.Lingering() {
			return nil, nil
		}
		if _, err := m.Run(ctx, "loginctl", "enable-linger"); err != nil {
			return nil, fmt.Errorf("loginctl enable-linger: %w", err)
		}
		_ = fsutil.WriteFileAtomic(marker, []byte("1\n"), 0o600)
		return []string{"enabled start at boot (lingering)"}, nil
	}
	if !m.Lingering() {
		_ = os.Remove(marker)
		return nil, nil
	}
	if _, err := os.Stat(marker); err != nil && !force {
		return nil, errors.New("lingering was not enabled by DeckBeacon (other services may rely on it); pass --force to disable it anyway")
	}
	if _, err := m.Run(ctx, "loginctl", "disable-linger"); err != nil {
		return nil, fmt.Errorf("loginctl disable-linger: %w", err)
	}
	_ = os.Remove(marker)
	return []string{"disabled start at boot (lingering)"}, nil
}

// Uninstall stops and disables the unit, removes it, and turns off lingering
// if (and only if) DeckBeacon enabled it. Agent state is kept.
func (m Manager) Uninstall(ctx context.Context) ([]string, error) {
	var changes []string
	if _, err := os.Stat(m.unitPath()); err == nil {
		_, _ = m.systemctl(ctx, "disable", "--now", UnitName)
		if err := os.Remove(m.unitPath()); err != nil {
			return changes, err
		}
		if _, err := m.systemctl(ctx, "daemon-reload"); err != nil {
			return changes, err
		}
		changes = append(changes, "stopped, disabled and removed "+UnitName)
	}
	if _, err := os.Stat(filepath.Join(m.StateDir, lingerMarker)); err == nil {
		c, err := m.SetStartAtBoot(ctx, false, false)
		changes = append(changes, c...)
		if err != nil {
			return changes, err
		}
	}
	return changes, nil
}

// Status inspects the installation.
func (m Manager) Status(ctx context.Context) Status {
	st := Status{StartAtBoot: m.Lingering()}
	if b, err := os.ReadFile(m.unitPath()); err == nil {
		st.UnitInstalled = true
		want, _ := Render(m.Exe)
		st.UnitCurrent = string(b) == want
	}
	st.Enabled, _ = m.systemctl(ctx, "is-enabled", UnitName)
	st.Active, _ = m.systemctl(ctx, "is-active", UnitName)
	if _, err := os.Stat(filepath.Join(m.StateDir, lingerMarker)); err == nil {
		st.LingerOwned = true
	}
	return st
}
