# Architecture
- apps/agent: Go standalone user-session service; core collectors, device identity, secure transport, local event store, capability registry, command engine.
- apps/boot-helper: optional boot-time minimal service, subject to feasibility; system telemetry and connectivity only. No second competing HA identity or unrestricted root daemon.
- apps/homeassistant: Python custom integration, discovery/config flow, entities, actions, events, notify, device triggers, reauth and repair.
- apps/decky: optional TypeScript/React Gaming Mode interface with narrowly scoped bridge to agent.
- apps/dashboard: optional TypeScript custom Lovelace cards, configurable through HA card editor.
- packages/protocol: schemas, compatibility rules, examples and contract fixtures.
- scripts, tests, docs, .github: tooling, integration tests, specifications, CI/release.

Agent is authoritative for local safety and permissions; HA manages pairing and connection profile, agent caches it securely. Decky is optional presentation and supported Steam-facing adapter, not a mandatory runtime dependency. UI calls HA integration, not SteamOS device directly.

## Invariants
One stable device ID; one active logical session per HA pairing; deduplicated event and command IDs; distinct transport connection state and physical power state; source provenance and confidence for telemetry; no silent privilege escalation.
