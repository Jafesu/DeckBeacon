# Protocol compatibility rules

## Versions
- **Major** (`v` in the envelope, `protocol` in hello/welcome): an incompatible change. Agent and integration each support a range. `hello.protocol {min,max}` is answered by `welcome.protocol` = the highest common major. With no overlap, HA sends `error{code: protocol_version, fatal: true}` and raises a repair issue telling the user which side to update.
- **Minor** (documented here, not on the wire): additive changes within a major.
- Each component (agent, integration, Decky plugin, dashboard) is versioned independently (spec 12). The protocol package is tagged `protocol/vX.Y.Z`.
- Support window: an integration release supports the current major and the previous one for at least 6 months after a new major ships.

## Senders vs receivers
- The schemas are **strict** (`additionalProperties: false`, closed enums). They define exactly what a sender of this minor version may emit. CI validates every message a component sends in tests.
- Receivers are **lenient**:
  - Unknown object fields are ignored.
  - An unknown enum value is treated as `unknown` (statuses, modes) or the item is skipped (capability IDs, metric keys, event types). It is logged once and never fatal.
  - An unknown message `type` gets `error{code: unsupported_type, fatal: false}`, and the connection stays up.
  - An unknown command `name` gets `command_result{state: rejected, error.code: unsupported}`.
- **Numbers:** integers on the wire must stay within ±(2^53−1) (`common#/$defs/counter`). Larger values, such as nanosecond timestamps, travel as strings or RFC 3339 times. Found in HV-R1, where a JSON float64 decode silently altered an int64 nanosecond value.
- Never rely on field order. Never reuse a removed field name with a different meaning.

## Allowed in a minor release
Add an optional field; add an enum value; add a message type; add a command, metric key, capability ID, or event type; relax a constraint (larger max length).

## Requires a major release
Remove or rename a field; make an optional field required; change a type or meaning; tighten a constraint; change the security layering (ADR-0002).

## Identity and replay invariants (all versions)
- `device_id` never changes for an install (spec 01). Key rotation keeps the ID.
- `event_seq` is persisted and strictly increasing per device across sessions and restarts. HA deduplicates on `(device_id, event_id)` and resumes from `welcome.last_received_event_seq`.
- `command_id` is deduplicated durably by the agent. A replayed command yields `command_result{state: duplicate}` and is never re-executed.
- Expiry is relative: the agent evaluates `ttl_s` from its own receipt time (clock-skew safe).
- Backfilled events carry `live: false`. HA exposes them as events but does not fire live-only device triggers for them by default.

## Change process
1. Edit the schema(s).
2. Add valid and invalid fixtures covering the change.
3. Update this file's changelog.
4. Both language suites must pass.

## Changelog
- **1.0** (M1, 2026-11-16): initial schema set. Sequence counters capped at 2^53−1.
- **1.0+pairing** (M2): `pair_request`, `pair_confirm`, `pair_complete`, `pair_abort` (additive).
- **1.1** (M3): event type `device_booted` (additive).
- **1.2** (M6): message type `library` (paged snapshot, no account data). Telemetry keys `game.*`, `library.*`, `downloads.*` (additive). Older receivers treat it as an unknown event type (logged, skipped).
- **1.3** (M6, ADR-0008): library entry `kind` value `"shortcut"` (non-Steam games, unsigned 32-bit `app_id`). Telemetry keys `library.shortcuts`, `steam.ui_mode`, `session.type` (additive). Telemetry keys `game.activity.*` and capability tier `ipc` (ADR-0007, additive).
