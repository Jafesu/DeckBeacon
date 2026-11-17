# Milestone 8 — Dashboard: Plan

Status: **In progress** (started 2026-11-17; owner: "lets do M8, we can include the artwork")

Spec 08: native HA entities plus **optional** custom Lovelace cards (device, currently playing, downloads, game library, performance, management) and an optional preconfigured multi-device dashboard. Requirements:
- Custom frontend in TypeScript.
- Cards use the HA integration, never the agent directly.
- Visual editor and YAML config.
- Mobile responsive, keyboard and screen-reader accessible, theme aware.
- Lazy-loaded artwork, no unbounded polling.
- Explain unavailable vs unsupported vs disabled vs unknown.
- Confirmation for destructive and force actions.
- **Authenticated artwork proxy; no exposed agent file URLs.**
- Cards versioned independently; the integration works without them.

## Work packages
| WP | Deliverable |
|---|---|
| WP1 | **Protocol 1.4 (additive):** command `get_artwork {app_id, kind}` → `artwork` messages (chunked: the Noise plaintext limit is 65,519 bytes per message) → `command_result`. Fixtures in both languages. ADR-0009. |
| WP2 | **Agent artwork:** resolve Steam's `appcache/librarycache/<appid>/` (flat and hashed-subdir layouts, G15 §6) and, for shortcuts, the `grid/` folder (pending discovery §14). Path confinement: regular files only, no symlinks, fixed name allowlist. Decode JPEG/PNG, downscale, **re-encode** (metadata stripped; HA never receives raw file bytes), size-capped. Cached by (app, kind, mtime). |
| WP3 | **HA:** artwork fetch with a bounded disk cache (served while the device sleeps), an authenticated view `/api/deckbeacon/artwork/…` (signed URLs for `<img>`), a **"Now playing" image entity** (works on native dashboards, no custom card needed), and a WebSocket API `deckbeacon/devices` / `deckbeacon/library` for the cards: entity map, metric reasons, capability statuses, library with signed artwork URLs. |
| WP4 | **Cards** (`apps/dashboard`, Lit + TypeScript, one bundled `deckbeacon-cards.js`): `deckbeacon-device-card`, `-now-playing-card`, `-downloads-card`, `-library-card` (artwork grid, search, launch with confirmation), `-performance-card`. Each has a visual editor (`getConfigForm`, device selector filtered to DeckBeacon) and YAML. Plus a `custom:deckbeacon` dashboard strategy (one view per device). |
| WP5 | **Distribution:** HACS-plugin layout, versioned independently (`apps/dashboard/package.json`). Dev install on the owner's HA with per-action approval: copy to `/config/www/deckbeacon/` and add a Lovelace resource. |
| WP6 | Tests: Go artwork tests, HA view/cache/WS tests, card unit tests (vitest + happy-dom), real-browser screenshots and an axe accessibility scan (Playwright + Chromium). CI job. G15 + owner HA validation, docs, results. |

## Acceptance criteria
- AC1 Each card renders connected, offline, and unavailable-with-reason states, and the visual editor round-trips its config.
- AC2 Artwork end to end on the G15: an installed game shows its Steam capsule in the library card and the now-playing card; a placeholder appears when none exists; non-Steam shortcuts show their grid artwork if present. The artwork view refuses unauthenticated requests.
- AC3 The library card searches and filters, and launch asks for confirmation. When the `game_management` tier isn't granted, launch is disabled and the card says why.
- AC4 Accessibility and layout: no axe violations; fully keyboard operable; phone width (390 px) and desktop screenshots; light and dark themes.
- AC5 No unbounded polling: each artwork is fetched once per (app, kind, file mtime) and served from cache afterwards. Cards only react to state changes.
- AC6 Without the cards installed, every feature remains available through native entities (incl. the artwork image entity). Cards and integration versions are independent.
- AC7 CI green (new dashboard job: typecheck, lint, unit tests, build).
- AC8 Security: artwork paths are confined (symlink or `..` escape refused), output is re-encoded with metadata gone, sizes are capped, and URLs are signed (tests).
