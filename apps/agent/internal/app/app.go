// Package app wires the agent's commands (run, pair, status, unpair).
package app

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/buildinfo"
	"github.com/jafesu/deckbeacon/apps/agent/internal/collect"
	"github.com/jafesu/deckbeacon/apps/agent/internal/discovery"
	"github.com/jafesu/deckbeacon/apps/agent/internal/dispatch"
	"github.com/jafesu/deckbeacon/apps/agent/internal/fsutil"
	"github.com/jafesu/deckbeacon/apps/agent/internal/identity"
	"github.com/jafesu/deckbeacon/apps/agent/internal/pairing"
	"github.com/jafesu/deckbeacon/apps/agent/internal/paths"
	"github.com/jafesu/deckbeacon/apps/agent/internal/session"
	"github.com/jafesu/deckbeacon/apps/agent/internal/store"
	"github.com/jafesu/deckbeacon/apps/agent/internal/trust"
	"github.com/jafesu/deckbeacon/apps/agent/internal/ulid"
)

// Env carries process-level dependencies.
type Env struct {
	Dirs   paths.Dirs
	Host   collect.Env
	Stdin  io.Reader
	Stdout io.Writer
	Log    *slog.Logger
}

func goarchToProtocol() string {
	if runtime.GOARCH == "arm64" {
		return "aarch64"
	}
	return "x86_64"
}

func deviceInfo(h collect.Env, osr collect.OSRelease) map[string]any {
	os := map[string]any{"id": osr["ID"], "version_id": osr["VERSION_ID"]}
	for k, v := range map[string]string{"build_id": osr["BUILD_ID"], "variant_id": osr["VARIANT_ID"]} {
		if v != "" {
			os[k] = v
		}
	}
	info := collect.ReadDeviceInfo(h)
	dev := map[string]any{"os": os, "arch": goarchToProtocol(), "model": info.Model}
	if info.Vendor != "" {
		dev["vendor"] = info.Vendor
	}
	return dev
}

func bootID(h collect.Env) string {
	if b, err := os.ReadFile(filepath.Join(h.Root, "proc/sys/kernel/random/boot_id")); err == nil {
		id := strings.ReplaceAll(strings.TrimSpace(string(b)), "-", "")
		if len(id) == 32 {
			return strings.ToLower(id)
		}
	}
	st, _ := os.ReadFile(filepath.Join(h.Root, "proc/stat"))
	sum := sha256.Sum256(st)
	return hex.EncodeToString(sum[:16])
}

// Pair performs interactive pairing and saves trust.
func Pair(ctx context.Context, e Env, baseURL, name string) error {
	osr, err := collect.RequireSteamOS(e.Host)
	if err != nil {
		return err
	}
	if err := e.Dirs.Ensure(); err != nil {
		return err
	}
	id, err := identity.LoadOrCreate(e.Dirs.State)
	if err != nil {
		return err
	}
	if baseURL == "" {
		fmt.Fprintln(e.Stdout, "Looking for Home Assistant on the local network…")
		found, err := discovery.Browse(ctx, discovery.MulticastAddr, 3*time.Second)
		if err != nil || len(found) == 0 {
			return errors.New("no Home Assistant found via mDNS; pass --url http://<home-assistant>:8123")
		}
		if len(found) > 1 {
			for _, f := range found {
				fmt.Fprintf(e.Stdout, "  %s  %s\n", f.Name, f.URL)
			}
			return errors.New("several Home Assistant instances found; choose one with --url")
		}
		baseURL = found[0].URL
		fmt.Fprintf(e.Stdout, "Found %s at %s\n", found[0].Name, baseURL)
	}
	if name == "" {
		name, _ = os.Hostname()
		if name == "" {
			name = "SteamOS device"
		}
	}
	dev := deviceInfo(e.Host, osr)
	dev["device_id"] = id.DeviceID
	reader := bufio.NewReader(e.Stdin)
	confirm := func(ctx context.Context, sas string) (bool, error) {
		fmt.Fprintf(e.Stdout, "\nPairing code: %s %s\nCheck that Home Assistant shows the same code, then confirm it there too.\nDoes Home Assistant show this code? [y/N]: ", sas[:3], sas[3:])
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return false, err
		}
		ans := strings.ToLower(strings.TrimSpace(line))
		return ans == "y" || ans == "yes", nil
	}
	tr, err := pairing.Pair(ctx, baseURL, id, pairing.Request{
		AgentVersion: semver(buildinfo.Version), Protocol: map[string]int{"min": 1, "max": 1}, Device: dev, Name: name,
	}, confirm)
	if err != nil {
		return err
	}
	if err := trust.Save(e.Dirs.State, tr); err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "Paired with %q. Device ID: %s\nStart the agent as a background service with: deckbeacon service install   (add --at-boot to start before login)\n", tr.InstanceName, id.DeviceID)
	return nil
}

func semver(v string) string {
	if strings.Count(v, ".") >= 2 {
		return v
	}
	return "0.0.0-dev"
}

// Run is the long-running service.
func Run(ctx context.Context, e Env, telemetryInterval time.Duration) error {
	osr, err := collect.RequireSteamOS(e.Host)
	if err != nil {
		return err
	}
	if err := e.Dirs.Ensure(); err != nil {
		return err
	}
	id, err := identity.LoadOrCreate(e.Dirs.State)
	if err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(e.Dirs.State, "store.db"), 1000)
	if err != nil {
		return err
	}
	defer st.Close()
	sys := collect.NewSystem(e.Host)
	if bt, err := sys.BootTime(); err == nil {
		if booted, err := detectBoot(st, bootID(e.Host), bt, time.Now()); err != nil {
			e.Log.Warn("boot detection", "err", err)
		} else if booted {
			e.Log.Info("new boot detected")
		}
	}
	lc, lcCaps, stopLC := startLifecycle(ctx, e.Log)
	defer stopLC()
	svc, _ := serviceManager(e)
	writeStatus := func(s session.Status) {
		b, _ := json.MarshalIndent(s, "", "  ")
		_ = fsutil.WriteFileAtomic(filepath.Join(e.Dirs.Runtime, "status.json"), b, 0o600)
	}
	for {
		tr, err := trust.Load(e.Dirs.State)
		switch {
		case errors.Is(err, trust.ErrNotPaired):
			writeStatus(session.Status{State: "unpaired", Since: time.Now().UTC()})
			e.Log.Info("not paired; run `deckbeacon pair`", "device_id", id.DeviceID)
		case err != nil:
			return err
		case tr.Revoked:
			writeStatus(session.Status{State: "revoked", Since: time.Now().UTC(), LastError: session.ErrRevoked.Error()})
		default:
			cli, err := session.New(session.Config{
				Identity: id, Trust: tr, Store: st, Collector: sys, Dispatcher: dispatch.New(st, time.Now),
				Hello:             session.HelloInfo{AgentVersion: semver(buildinfo.Version), Device: deviceInfo(e.Host, osr), BootID: bootID(e.Host), Mode: func() string { m, _ := sys.SessionMode(); return m }},
				TelemetryInterval: telemetryInterval, Log: e.Log, OnStatus: writeStatus,
				Lifecycle: lc, ExtraCapabilities: func() []collect.Capability { return lifecycleCapabilities(lcCaps, svc) },
			})
			if err != nil {
				return err
			}
			err = cli.Run(ctx)
			if errors.Is(err, session.ErrRevoked) {
				tr.Revoked = true
				_ = trust.Save(e.Dirs.State, tr)
				e.Log.Warn("device revoked by Home Assistant; idle until re-paired")
				continue
			}
			return err
		}
		// Unpaired or revoked: wait for a (re-)pairing without hammering anything.
		// Lifecycle transitions are acknowledged immediately so the system is
		// never delayed while there is no one to tell.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-lc:
			ev.Ready()
		case <-time.After(5 * time.Second):
		}
	}
}

// Status prints identity, pairing and connection state.
func Status(e Env, asJSON bool) error {
	out := map[string]any{"agent": buildinfo.Get().String()}
	if id, err := identity.LoadOrCreate(e.Dirs.State); err == nil {
		out["device_id"] = id.DeviceID
	}
	if tr, err := trust.Load(e.Dirs.State); err == nil {
		out["paired_with"] = tr.InstanceName
		out["revoked"] = tr.Revoked
		var kinds []string
		for _, u := range tr.URLs {
			kinds = append(kinds, u.Kind)
		}
		out["url_kinds"] = kinds
	} else {
		out["paired_with"] = nil
	}
	if b, err := os.ReadFile(filepath.Join(e.Dirs.Runtime, "status.json")); err == nil {
		var s session.Status
		if json.Unmarshal(b, &s) == nil {
			out["connection"] = s
		}
	}
	if asJSON {
		return json.NewEncoder(e.Stdout).Encode(out)
	}
	for _, k := range []string{"agent", "device_id", "paired_with", "revoked", "url_kinds", "connection"} {
		if v, ok := out[k]; ok {
			fmt.Fprintf(e.Stdout, "%-12s %v\n", k+":", v)
		}
	}
	return nil
}

// Unpair removes local trust. The device should also be deleted in HA.
func Unpair(e Env) error {
	if err := trust.Remove(e.Dirs.State); err != nil {
		return err
	}
	fmt.Fprintln(e.Stdout, "Local pairing removed. Also delete this device in Home Assistant (Settings → Devices & services → DeckBeacon).")
	return nil
}

func ulidNew() string { return ulid.New() }

// Service runs `deckbeacon service <action>`.
func Service(ctx context.Context, e Env, action string, atBoot, force, asJSON bool) error {
	m, err := serviceManager(e)
	if err != nil {
		return err
	}
	var changes []string
	switch action {
	case "install":
		changes, err = m.Install(ctx, atBoot)
	case "uninstall":
		changes, err = m.Uninstall(ctx)
	case "boot-on":
		changes, err = m.SetStartAtBoot(ctx, true, false)
	case "boot-off":
		changes, err = m.SetStartAtBoot(ctx, false, force)
	case "status":
		st := m.Status(ctx)
		if asJSON {
			return json.NewEncoder(e.Stdout).Encode(st)
		}
		fmt.Fprintf(e.Stdout, "unit installed: %v (current: %v)\nenabled: %s\nactive: %s\nstart at boot: %v\n", st.UnitInstalled, st.UnitCurrent, st.Enabled, st.Active, st.StartAtBoot)
		return nil
	default:
		return fmt.Errorf("unknown service action %q", action)
	}
	for _, c := range changes {
		fmt.Fprintln(e.Stdout, "-", c)
	}
	if len(changes) == 0 && err == nil {
		fmt.Fprintln(e.Stdout, "nothing to change")
	}
	return err
}

// Probe prints what the agent would report, without pairing or changing anything:
// device identity, two telemetry samples (so CPU usage has a delta) and capabilities.
func Probe(ctx context.Context, e Env, asJSON bool) error {
	osr, err := collect.RequireSteamOS(e.Host)
	if err != nil {
		return err
	}
	sys := collect.NewSystem(e.Host)
	sys.Collect(ctx)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
	}
	metrics := sys.Collect(ctx)
	lc, lcCaps, stop := startLifecycle(ctx, e.Log)
	_ = lc
	stop()
	svc, _ := serviceManager(e)
	caps := append(sys.Capabilities(ctx), lifecycleCapabilities(lcCaps, svc)...)
	out := map[string]any{"device": deviceInfo(e.Host, osr), "boot_id_ok": len(bootID(e.Host)) == 32, "metrics": metrics, "capabilities": caps}
	if asJSON {
		enc := json.NewEncoder(e.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	fmt.Fprintf(e.Stdout, "device: %v\n\nmetrics:\n", out["device"])
	for _, m := range metrics {
		v := fmt.Sprint(m.Value)
		if m.Value == nil {
			v = "unavailable (" + m.Reason + ")"
		}
		fmt.Fprintf(e.Stdout, "  %-26s %-14s %-4s [%s]\n", m.Key, v, m.Unit, m.Source)
	}
	fmt.Fprintln(e.Stdout, "\ncapabilities:")
	for _, c := range caps {
		fmt.Fprintf(e.Stdout, "  %-26s %-12s %s\n", c.ID, c.Status, c.Reason)
	}
	return nil
}
