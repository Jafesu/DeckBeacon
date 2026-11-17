package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jafesu/deckbeacon/apps/agent/internal/activity"
	"github.com/jafesu/deckbeacon/apps/agent/internal/fsutil"
)

// activityConfig is <config>/activity.json. The Discord proxy creates a socket
// on the device, so it is enabled only here, by the device owner; Home
// Assistant cannot turn it on (ADR-0007: off by default, explicit opt-in).
type activityConfig struct {
	DiscordProxy bool `json:"discord_proxy"`
}

func activityPath(e Env) string { return filepath.Join(e.Dirs.Config, "activity.json") }

func loadActivityConfig(e Env) activityConfig {
	var c activityConfig
	if b, err := os.ReadFile(activityPath(e)); err == nil {
		if err := json.Unmarshal(b, &c); err != nil {
			e.Log.Warn("ignoring invalid activity.json", "err", err)
			return activityConfig{}
		}
	}
	return c
}

// startActivity starts the proxy when enabled (nil otherwise).
func startActivity(e Env) *activity.Proxy {
	if !loadActivityConfig(e).DiscordProxy {
		return nil
	}
	p := activity.New(activity.Config{RuntimeDir: os.Getenv("XDG_RUNTIME_DIR"), Log: e.Log})
	if err := p.Start(); err != nil {
		e.Log.Warn("discord activity proxy not started", "reason", p.Status().Reason)
	}
	return p
}

// Activity implements `deckbeacon activity on|off|status`.
func Activity(e Env, action string) error {
	if err := e.Dirs.Ensure(); err != nil {
		return err
	}
	c := loadActivityConfig(e)
	switch action {
	case "on", "off":
		c.DiscordProxy = action == "on"
		b, _ := json.MarshalIndent(c, "", "  ")
		if err := fsutil.WriteFileAtomic(activityPath(e), append(b, '\n'), 0o600); err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "Discord activity proxy: %s (experimental).\n", action)
		if c.DiscordProxy {
			fmt.Fprintln(e.Stdout, "Games with Discord Rich Presence will show their details/state in Home Assistant.")
			fmt.Fprintln(e.Stdout, "If Discord is running, every message is forwarded to it unchanged; if not,")
			fmt.Fprintln(e.Stdout, "DeckBeacon answers the game itself so the activity still reaches Home Assistant.")
		}
		fmt.Fprintln(e.Stdout, "Restart the agent to apply: systemctl --user restart deckbeacon.service")
	case "status":
		state := "off"
		if c.DiscordProxy {
			state = "on"
		}
		fmt.Fprintf(e.Stdout, "Discord activity proxy: %s\n", state)
	default:
		return fmt.Errorf("usage: deckbeacon activity on|off|status")
	}
	return nil
}
