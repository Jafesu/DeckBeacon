# Claude Code project handoff
Project: DeckBeacon. Monorepo; planning complete, implementation only when explicitly authorized. Read AGENTS.md and docs/specification/ in numeric order. Start with milestone 0 research and produce a feasibility report before building production features. Propose architecture decision records for unresolved issues. Implement one milestone at a time with tests and acceptance evidence. Do not fabricate Steam APIs, HA Cloud transport support, Decky interfaces, or hardware telemetry. Never silently broaden permissions or use unrestricted HA tokens. Keep agent Go, HA integration Python, Decky and dashboard TypeScript/React as appropriate.

## Working notes (added M1, 2026-11-16)
- Status: M0 approved (ADRs 0001–0006 accepted). M1 (monorepo foundation) in progress. See `docs/milestones/`.
- Local checks before committing:
  - `gofmt -l apps/agent packages/protocol`
  - `go vet ./apps/agent/... ./packages/protocol/...`
  - `go test ./apps/agent/... ./packages/protocol/...`
  - `cd packages/protocol/python && ruff check . && ruff format --check . && pytest`
  - `python3 scripts/check_fixtures.py packages/protocol/fixtures`
- Never install anything on the owner's Home Assistant without explicit per-action approval. The HA connector cannot write to `custom_components/`, so the owner copies files.
- No SteamOS hardware access yet. Use mocked providers and recorded fixtures, and leave capability rows at `research`.
