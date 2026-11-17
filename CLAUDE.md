# Claude Code project handoff
Project: DeckBeacon. Monorepo; planning complete, implementation only when explicitly authorized. Read AGENTS.md and docs/specification/ in numeric order. Start with milestone 0 research and produce a feasibility report before building production features. Propose architecture decision records for unresolved issues. Implement one milestone at a time with tests and acceptance evidence. Do not fabricate Steam APIs, HA Cloud transport support, Decky interfaces, or hardware telemetry. Never silently broaden permissions or use unrestricted HA tokens. Keep agent Go, HA integration Python, Decky and dashboard TypeScript/React as appropriate.

## Working notes (added M1, 2026-11-16)
- Status: M0 approved (ADRs 0001–0006 accepted). M3 (service, boot, lifecycle), M6 (library, sessions, shortcuts, downloads, launch/close, UI mode, Discord activity proxy) and M8 (cards, artwork) complete and hardware-validated on the Dell G15. See `docs/milestones/M3-plan.md`, `M6-plan.md`, `docs/agent-lifecycle.md`, ADR-0007/0008. Proton Rich Presence is deferred by the owner. Lifecycle tests use a private `dbus-daemon` (skipped if absent). Python 3.14 + `pytest-homeassistant-custom-component` for integration tests (`apps/homeassistant`); after a schema change run `python3 scripts/sync_schemas.py`; regenerate Noise vectors with `go test ./noise -update` in `packages/protocol`.
- Local checks before committing:
  - `gofmt -l apps/agent packages/protocol`
  - `go vet ./apps/agent/... ./packages/protocol/...`
  - `go test ./apps/agent/... ./packages/protocol/...`
  - `cd packages/protocol/python && ruff check . && ruff format --check . && pytest`
  - `python3 scripts/check_fixtures.py packages/protocol/fixtures`
- Never install anything on the owner's Home Assistant without explicit per-action approval. The HA connector cannot write to `custom_components/`, so the owner copies files.
- SteamOS hardware: the owner's Dell G15 runs a Remote Control Claude session (reachable via send_message after owner approval). Read-only runs only unless the owner approves each change. Evidence goes to `docs/research/evidence/`. On SteamOS run local tests with `CGO_ENABLED=0` (stock gcc lacks `cc1`).
- Distribution mirrors (ADR-0005, public): `jafesu/deckbeacon-homeassistant` (integration), `jafesu/deckbeacon-card` (cards), `jafesu/deckbeacon-decky` (placeholder until M4). Regenerate with `python3 scripts/build_mirrors.py --ha DIR --card DIR` (build the cards first) and push to the mirror's `main`. Its workflow publishes `v<version>` when the version has no release yet. Bump `manifest.json` / `apps/dashboard/package.json` for an update to show in the owner's Marketplace. Tag pushes are refused from cloud sessions; never edit mirrors by hand.
