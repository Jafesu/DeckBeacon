# Milestone 6 — Game Library, Sessions, Downloads, Launch, Activity: Plan

Status: **Implemented, G15 validation pending** (started 2026-11-17; owner: "Lets go with M6", plus a request for rich-presence-style details → ADR-0007)

Spec 06 lists the areas. The roadmap says: library and sessions first, then downloads and launch. Destructive operations (move, uninstall, compat-tool change) are not in M6: they stay ADR-0004 Tier C, M4/experimental.

## Work packages
| WP | Deliverable |
|---|---|
| WP1 | **VDF KeyValues text parser** (strict, size-bounded, allowlisted keys) + **library discovery**: `libraryfolders.vdf` → libraries (incl. removable), `appmanifest_*.acf` → games. Never opens credential files (`config.vdf`, `localconfig.vdf`, `loginusers.vdf`, `ssfn*`). |
| WP2 | Game classification: game vs tool/runtime (Proton, Steam Linux Runtime, redistributables), from known app ids and name rules, with confidence. Non-Steam shortcuts: **in scope since ADR-0008** (allowlisted `shortcuts.vdf` fields; u32 canonical key; launch via 64-bit GameID). |
| WP3 | **Current game** from the process table (`reaper SteamLaunch AppId=N`), mapped to the manifest name. **Sessions**: `game_started`/`game_stopped` events, monotonic session duration, segmented at suspend. Local history is opt-in with bounded retention (90 days default, decision G8). |
| WP4 | **Downloads/updates** from manifest `StateFlags`, `BytesToDownload`/`BytesDownloaded`/`BytesStaged`: status (downloading, update required, fully installed, …), progress, speed (derived), ETA. `download_started`/`download_completed`/`update_available` events. |
| WP5 | **Launch**: `launch_game` command → `steam steam://rungameid/<id>` (fixed argv, uint32 app id, installed check). Results: accepted → dispatched → confirmed (process seen) or failed. Policy: requires the `game_management` tier; the HA options flow grants it (default: local yes, remote no); the agent enforces it. |
| WP6 | **Protocol 1.2**: `library` snapshot message (paged, ≤ 100 games per page), game metrics in telemetry (`game.current.*`, `downloads.*`), `get_game`/`list_games` results. Fixtures + both validators. |
| WP7 | **HA**: sensors (current game, app id, session duration, installed games, updates available, download status/progress/speed), actions `deckbeacon.launch_game`, `list_games`, `get_game`, `refresh_library` (with response data), options flow for permissions, events on the bus. |
| WP7b | **ADR-0008**: Steam UI mode (`steam.ui_mode`, from the `SSGL: UI mode` log line only) and `session.type` (gamescope / Plasma). Protocol 1.3. |
| WP8 | **Activity (ADR-0007, experimental, opt-in)**: Discord IPC proxy → `game.activity.details` / `state` / `party`. |
| WP9 | Tests (recorded G15 library fixture, E2E), G15 validation, docs, results. |

## Acceptance criteria
- AC1 The library on the G15 matches Steam's own list (count and names of installed games). Tools and runtimes are separated.
- AC2 Starting and quitting a game produces exactly one `game_started` and one `game_stopped` with the right app id. Session duration excludes suspended time.
- AC3 A download or update shows status, progress and speed in HA, with a completion event that fires only when installed.
- AC4 `launch_game` from HA starts an installed game on the G15. It is refused for non-installed ids, a denied permission tier, or a malformed id.
- AC5 Credential files are never opened (test with a canary file plus file-access auditing in tests).
- AC6 With activity enabled and a Discord-RPC game, HA shows its `details`/`state`. Discord's own status keeps working. Without data, the sensors are `unavailable`.
- AC8 Non-Steam shortcuts show by name (hidden ones excluded). Starting one produces a named `game_started`. No `exe`/`StartDir`/`LaunchOptions` value ever leaves the agent. Opening and closing Big Picture flips `steam.ui_mode`.
- AC7 CI green on HA minimum and latest, plus E2E.
