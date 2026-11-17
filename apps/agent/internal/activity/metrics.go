package activity

import (
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/collect"
)

const source = "discord ipc proxy (game-provided, untrusted text)"

// Metrics reports the current activity. Values are null with a reason when no
// game has set one, so HA shows the sensors as unavailable (never guessed).
func (p *Proxy) Metrics() []collect.Metric {
	at := p.cfg.Now().UTC().Truncate(time.Second)
	keys := []string{"game.activity.details", "game.activity.state", "game.activity.party_size", "game.activity.party_max", "game.activity.started_at"}
	a := p.Current()
	out := make([]collect.Metric, 0, len(keys))
	for _, k := range keys {
		m := collect.Metric{Key: k, SampledAt: at, Source: source, Confidence: "high"}
		var v any
		if a != nil {
			switch k {
			case "game.activity.details":
				v = nonEmpty(a.Details)
			case "game.activity.state":
				v = nonEmpty(a.State)
			case "game.activity.party_size":
				if a.PartyMax > 0 {
					v = a.PartySize
				}
			case "game.activity.party_max":
				if a.PartyMax > 0 {
					v = a.PartyMax
				}
			case "game.activity.started_at":
				if !a.Started.IsZero() {
					v = a.Started.Format(time.RFC3339)
				}
			}
		}
		if v == nil {
			m.Confidence = ""
			m.Reason = "no activity from the running game"
			if p.Status().Listening == "" {
				m.Reason = "activity proxy not listening"
			}
		}
		m.Value = v
		out = append(out, m)
	}
	return out
}

func nonEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Capabilities reports the proxy state (ADR-0007 P1, experimental).
func (p *Proxy) Capabilities() []collect.Capability {
	st := p.Status()
	c := collect.Capability{ID: "game.activity", Tier: "ipc", Experimental: true, Source: "Discord IPC proxy (forwards to Discord, or answers locally when Discord is not running)", Privilege: "none", Reason: st.Reason}
	if st.Listening != "" {
		c.Status = "available"
	} else {
		c.Status = "unavailable"
	}
	return []collect.Capability{c}
}
