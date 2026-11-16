# ADR-0004: Steam integration tiers

Status: Accepted (approved by project owner 2026-11-16; see docs/research/M0-decisions.md)

## Context
Valve publishes no local control API for the Steam client. The spec forbids writing Steam internal state (02, 06, AGENTS.md) and forbids fabricating APIs (CLAUDE.md).

## Decision
Every Steam capability belongs to exactly one tier, and the tier is reported with the capability:

| Tier | Mechanism | Allowed operations | Default |
|---|---|---|---|
| A — files (read-only) | Parse `libraryfolders.vdf`, `appmanifest_*.acf`, `registry.vdf`, `compatibilitytool.vdf`, `content_log.txt`, librarycache, and the `CompatToolMapping` subtree of `config.vdf`, through an **allowlisted-key parser**. Process table for `reaper`. | Library, current game, download progress, update-available, Proton inventory, artwork | enabled |
| B — Steam URL handler | `steam steam://<verb>/<appid>` exec'd as `deck` with a fixed argv; app ID validated as uint32 | `rungameid` (launch), `install`/`uninstall`/`validate` (open Steam's own dialog only) | launch: enabled (game tier); install: enabled; uninstall: disabled |
| C — Decky `SteamClient` (undocumented) | Feature-detected calls from the Decky plugin frontend, with a per-Steam-build allowlist | close, pause/resume downloads, queue order, trigger update, move, set compat tool, brightness, suspend-via-Steam | **experimental, disabled**; each needs per-capability opt-in |
| — | Direct writes to Steam files, deleting game dirs or prefixes | none | forbidden |

- Sensitive files: `config.vdf`, `localconfig.vdf`, `loginusers.vdf`, and `ssfn*` are opened only by the allowlisted parser and never copied, cached, logged, or put in diagnostics. Tests enforce this.
- Every command result distinguishes `accepted` → `dispatched` → `confirmed` (for example, launch is confirmed when the Tier A current-game detector sees the app).

## Consequences
- Without Decky, users get read-only management plus launch. That matches spec principles but is less than the spec's action list, so the HA UI must show `unsupported`/`disabled` clearly (spec 08).
- Tier C can break on any Steam update. The agent must check the Steam build and disable unknown builds by default.

## Validation
HV-G1…G8, HV-D2. Product decision requested: keep Tier C in v1 as experimental, or drop it.
