# apps/agent: DeckBeacon SteamOS agent (Go)

Standalone `systemd --user` service (ADR-0003). Always dials into the DeckBeacon endpoint in Home Assistant (ADR-0001).

**M1 status:** skeleton. `deckbeacon version [--json]` only.

```bash
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o deckbeacon ./cmd/deckbeacon
```
Planned packages (M2): `internal/identity`, `internal/channel` (Noise), `internal/session` (state machine), `internal/store` (durable events/commands), `internal/capability`, `internal/collect/{system,steam,network}` with mockable providers, `internal/policy`.
