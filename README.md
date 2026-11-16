# DeckBeacon

SteamOS-only, local-first monitoring and management companion for Home Assistant.

**Status:** M0 (research and feasibility) approved. **M1 (monorepo foundation) in progress.** No end-user functionality yet.

- Specification: [`docs/specification/`](docs/specification/) (v1.0, authoritative), [`CLAUDE.md`](CLAUDE.md), [`AGENTS.md`](AGENTS.md)
- Feasibility report: [`docs/research/M0-feasibility-report.md`](docs/research/M0-feasibility-report.md)
- Capability matrix: [`docs/research/capability-matrix.md`](docs/research/capability-matrix.md)
- Hardware validation plan: [`docs/research/hardware-validation-plan.md`](docs/research/hardware-validation-plan.md)
- Accepted ADRs: [`docs/adr/`](docs/adr/) · Owner decisions: [`docs/research/M0-decisions.md`](docs/research/M0-decisions.md)
- Milestones: [`docs/milestones/`](docs/milestones/) · Threat model: [`docs/security/threat-model.md`](docs/security/threat-model.md)
- Protocol: [`packages/protocol/`](packages/protocol/)
- Research spikes (throwaway): [`research/spikes/`](research/spikes/)

Layout: `apps/agent` (Go), `apps/homeassistant` (Python), `apps/decky` (TS/React, optional), `apps/dashboard` (TS, optional), `packages/protocol`, `scripts`, `tests`, `.github`.
