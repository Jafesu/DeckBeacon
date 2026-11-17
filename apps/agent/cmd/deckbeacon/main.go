// Command deckbeacon is the DeckBeacon SteamOS agent.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/app"
	"github.com/jafesu/deckbeacon/apps/agent/internal/buildinfo"
	"github.com/jafesu/deckbeacon/apps/agent/internal/collect"
	"github.com/jafesu/deckbeacon/apps/agent/internal/paths"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

const usageText = `usage: deckbeacon <command> [flags]

commands:
  pair     [--url URL] [--name NAME]   pair with Home Assistant (shows a code to confirm)
  run      [--telemetry-interval 30s]  run the agent (normally via systemd --user)
  status   [--json]                    show identity, pairing and connection state
  probe    [--json]                    show what this device would report (read-only, no pairing)
  unpair                               forget the local pairing
  service  install [--at-boot] | uninstall | status [--json] | boot on|off [--force]
                                       manage the systemd --user service (no sudo)
  activity on|off|status               experimental: report Discord Rich Presence text to HA
  release  keygen|manifest|sign|verify release tooling (signing agent releases, ADR-0010)
  version  [--json]                    print build information`

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usageText)
		return 2
	}
	cmd, rest := args[0], args[1:]
	if cmd == "release" { // release tooling has its own flags and needs no agent state
		if err := app.Release(rest, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "/", "host root for /proc, /sys, /etc (testing only)")
	var (
		url, name     *string
		interval      *time.Duration
		jsonOut       *bool
		logLevel      *string
		atBoot, force *bool
	)
	switch cmd {
	case "pair":
		url = fs.String("url", "", "Home Assistant URL, e.g. http://homeassistant.local:8123")
		name = fs.String("name", "", "device name shown in Home Assistant (default: hostname)")
	case "run":
		interval = fs.Duration("telemetry-interval", 30*time.Second, "telemetry period")
		logLevel = fs.String("log-level", "info", "debug|info|warn|error")
	case "status", "version", "probe":
		jsonOut = fs.Bool("json", false, "machine-readable output")
	case "unpair", "activity":
	case "service":
		atBoot = fs.Bool("at-boot", false, "also start at boot before login (systemd lingering)")
		force = fs.Bool("force", false, "boot off: disable lingering even if DeckBeacon did not enable it")
		jsonOut = fs.Bool("json", false, "machine-readable status")
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, usageText)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s\n", cmd, usageText)
		return 2
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if cmd == "version" {
		if *jsonOut {
			_ = json.NewEncoder(stdout).Encode(buildinfo.Get())
		} else {
			fmt.Fprintln(stdout, buildinfo.Get())
		}
		return 0
	}
	dirs, err := paths.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	host := collect.HostEnv()
	host.Root = *root
	level := slog.LevelInfo
	if logLevel != nil {
		_ = level.UnmarshalText([]byte(*logLevel))
	}
	e := app.Env{Dirs: dirs, Host: host, Stdin: stdin, Stdout: stdout, Log: slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch cmd {
	case "pair":
		err = app.Pair(ctx, e, *url, *name)
	case "run":
		err = app.Run(ctx, e, *interval)
		if ctx.Err() != nil {
			err = nil // clean shutdown on SIGTERM
		}
	case "status":
		err = app.Status(e, *jsonOut)
	case "probe":
		err = app.Probe(ctx, e, *jsonOut)
	case "unpair":
		err = app.Unpair(e)
	case "activity":
		action := ""
		if fs.NArg() > 0 {
			action = fs.Arg(0)
		}
		err = app.Activity(e, action)
	case "service":
		action, rest := "", fs.Args()
		if len(rest) > 0 {
			action = rest[0]
		}
		if action == "boot" {
			if len(rest) < 2 || (rest[1] != "on" && rest[1] != "off") {
				fmt.Fprintln(stderr, "usage: deckbeacon service boot on|off [--force]")
				return 2
			}
			action = "boot-" + rest[1]
		}
		if action == "" {
			fmt.Fprintln(stderr, "usage: deckbeacon service install|uninstall|status|boot on|off")
			return 2
		}
		err = app.Service(ctx, e, action, *atBoot, *force, *jsonOut)
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}
