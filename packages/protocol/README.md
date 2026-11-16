# packages/protocol: DeckBeacon wire protocol

The contract between the SteamOS agent (Go) and the Home Assistant integration (Python). See ADR-0001 (transport) and ADR-0002 (channel security).

| Path | Content |
|---|---|
| `schemas/v1/*.schema.json` | **Source of truth.** JSON Schema 2020-12. `envelope` dispatches to one body schema per `type`. |
| `fixtures/valid/` | Messages every conforming sender may emit. They double as examples. |
| `fixtures/invalid/` | Messages that must be rejected. `error_path` pins the single failing JSON Pointer. |
| `protocol.go` | Go package: embedded schemas, `Validate`/`ValidateMessage`. |
| `python/` | Python contract tests (`jsonschema` + `referencing`). |
| `COMPATIBILITY.md` | Versioning and evolution rules. |

## Layering
```
WebSocket (HA HTTP server; TLS where HA has it)
└── Noise IK session (mutual static keys, AEAD, forward secrecy)  ← M2
    └── JSON envelope {v, type, id, ts, seq, body}                ← these schemas
```
Pairing messages (Noise XX + SAS) get their own schemas in M2.

## Running the contract tests
```bash
go test ./...                                   # from packages/protocol
cd python && pip install -e '.[dev]' && pytest  # Python side
```
Both suites enumerate the same fixture directories, so a fixture added for one language is automatically enforced in the other (acceptance criterion M1-AC3).

## Message summary
| type | direction | purpose |
|---|---|---|
| `hello` | agent → HA | identity, OS, arch, session mode, transport, protocol range, resume point |
| `welcome` | HA → agent | negotiated protocol, heartbeat, granted permission tiers for this transport |
| `capabilities` | agent → HA | full capability snapshot: status/tier/source/privilege/reason |
| `telemetry` | agent → HA | metric samples with `sampled_at`, `source`, `confidence`. `null` = unavailable now |
| `event` | agent → HA | durable events, `event_seq` persisted across sessions, `live` vs backfill |
| `command` | HA → agent | closed set of command names, typed args, relative `ttl_s` |
| `command_result` | agent → HA | `accepted → dispatched → confirmed` or `deferred/rejected/failed/expired/duplicate` |
| `notification` | HA → agent | spec-07 envelope. Actions are opaque IDs, never service calls |
| `notification_status` | agent → HA | delivery and interaction state, plus the surface used |
| `ack` | both | receipt by envelope id (`event_seq` for events) |
| `ping`/`pong` | both | application heartbeat |
| `error` | both | protocol-level error |
