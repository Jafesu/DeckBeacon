<p align="center"><img src="docs/brand/png/readme-banner.png" alt="DeckBeacon" width="600"></p>

# DeckBeacon

SteamOS-only, local-first monitoring and management companion for Home Assistant.

**Status:** M0–M3 and M6 complete and validated on real SteamOS hardware (Dell G15). The Go agent pairs with Home Assistant over a SAS-confirmed Noise channel and reports system telemetry, power state, the Steam library (games and non-Steam shortcuts), current game and sessions, live downloads, Steam UI mode and optional Discord Rich Presence activity. Games can be launched and closed from HA. Optional dashboard cards with Steam artwork (M8). The integration and cards install through the HA Marketplace from generated mirror repositories. Pre-release.

- Specification: [`docs/specification/`](docs/specification/) (v1.0, authoritative), [`CLAUDE.md`](CLAUDE.md), [`AGENTS.md`](AGENTS.md)
- Feasibility report: [`docs/research/M0-feasibility-report.md`](docs/research/M0-feasibility-report.md)
- Capability matrix: [`docs/research/capability-matrix.md`](docs/research/capability-matrix.md)
- Hardware validation plan: [`docs/research/hardware-validation-plan.md`](docs/research/hardware-validation-plan.md)
- Accepted ADRs: [`docs/adr/`](docs/adr/) · Owner decisions: [`docs/research/M0-decisions.md`](docs/research/M0-decisions.md)
- Milestones: [`docs/milestones/`](docs/milestones/) · Threat model: [`docs/security/threat-model.md`](docs/security/threat-model.md)
- Protocol: [`packages/protocol/`](packages/protocol/)
- Research spikes (throwaway): [`research/spikes/`](research/spikes/)

Layout: `apps/agent` (Go), `apps/homeassistant` (Python), `apps/decky` (TS/React, optional), `apps/dashboard` (TS, optional), `packages/protocol`, `scripts`, `tests`, `.github`.
