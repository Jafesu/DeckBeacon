# apps/agent: DeckBeacon SteamOS agent (Go)

Standalone `systemd --user` service (ADR-0003). Always dials into the DeckBeacon endpoint inside Home Assistant (ADR-0001) over a Noise channel (ADR-0002). It holds no HA tokens.

```bash
deckbeacon pair --url http://homeassistant.local:8123   # or omit --url to use mDNS
deckbeacon run                                          # normally via packaging/systemd/deckbeacon.service
deckbeacon status [--json]
deckbeacon unpair
deckbeacon service install [--at-boot] | uninstall | status | boot on|off   # see docs/agent-lifecycle.md
```

| Package | Role |
|---|---|
| `internal/identity` | X25519 + Ed25519 keys, stable `device_id`, refuses group/world-readable keys |
| `internal/trust` | pinned HA key + connection profile |
| `internal/store` | bbolt: bounded event queue, persisted `event_seq`, durable command dedupe |
| `internal/channel` | WebSocket + Noise + schema-validated envelopes |
| `internal/pairing` | SAS pairing client |
| `internal/session` | connection state machine, local-first failover, backoff, heartbeat, resume |
| `internal/dispatch` | commands: dedupe, TTL, unsupported |
| `internal/collect` | system telemetry under a configurable root (synthetic trees plus the recorded `dell-g15-5520` tree in `testdata/roots`). `fan.rpm` is the fastest fan. |
| `internal/service` | systemd --user unit + opt-in lingering (never sudo) |
| `internal/lifecycle` | logind sleep/shutdown with delay inhibitor; clock-jump resume fallback |
| `internal/discovery` | mDNS legacy-unicast browse for `_home-assistant._tcp` |

Testing: `go test -race ./...` (CI). On SteamOS itself use `CGO_ENABLED=0 go test ./...`: stock SteamOS gcc lacks `cc1`, and godbus pulls in `os/user`, which needs cgo when it is enabled. The `testdata/roots/*` trees are **synthetic** until replaced by real captures.
