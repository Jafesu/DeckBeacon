# ADR-0005: Monorepo source of truth with generated distribution repositories

Status: Proposed (M0, 2026-11-16)

## Context
- Since HA 2026.11, HACS has been the built-in **Marketplace**. The Marketplace repository-structure reference says: "A repository holds one item of one type". Integrations live in `custom_components/<domain>/` (or the root with `content_in_root`). `zip_release` is for integrations only. Releases must be *published*, and the manifest `version` must equal the tag.
- The Decky plugin store adds plugins as git submodules via PR to `decky-plugin-database`.
- Spec 00 requires every source to live in one monorepo, with versions independent per component (12).

## Decision
- `jafesu/deckbeacon` (this monorepo) is the only place code is written. Per-component tags: `agent/vX.Y.Z`, `ha/vX.Y.Z`, `card/vX.Y.Z`, `decky/vX.Y.Z`, `protocol/vX.Y.Z`.
- CI publishes generated, **release-only mirror repositories** (no hand edits; their README points back here):
  - `deckbeacon-homeassistant`: `custom_components/deckbeacon/` + `hacs.json` (with the `homeassistant` minimum) + matching release tags.
  - `deckbeacon-card`: the built card JS (`deckbeacon-card.js`) attached to releases.
  - `deckbeacon-decky`: plugin at the repo root for the store submodule.
- Agent binaries (linux/amd64 and linux/arm64), signatures, checksums, and SBOMs are published as monorepo releases.
- Alternative considered: make the monorepo itself the Marketplace integration repo via `zip_release`. Rejected because the card still needs its own repo, and the monorepo's release stream would be dominated by HA tags.

## Consequences
- Needs a GitHub token or App with write access to the mirror repos, created by the owner (not by Claude).
- Mirror repos must be created before the first public release (M10). Until then, manual install from monorepo release assets is enough.

## Validation
M10 dry run: Marketplace "custom repository" install of the mirror, plus Decky zip-URL install.
