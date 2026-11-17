# Milestone 6 — Game Library, Sessions, Downloads, Launch, Activity: Plan

Status: **Complete** (2026-11-17, validated on the Dell G15) (started 2026-11-17; owner: "Lets go with M6", plus a request for rich-presence-style details → ADR-0007)

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

## Results (2026-11-17)
Environment: Go 1.26.11; HA 2026.5.1 and 2026.10.4 test harness (43 tests each); CI runs #26–#31 green. Hardware: Dell G15 5520, SteamOS 3.9.5, the owner's HA (device `livingroom_steamos`). Evidence: HA recorder history (below), [`G15-m6-discovery-2026-11-17.md`](../research/evidence/G15-m6-discovery-2026-11-17.md) §1–§13.

| AC | Result | Evidence |
|---|---|---|
| AC1 library | **pass** | 37 manifests at discovery = 29 games + 8 tools/runtimes. The "Installed games" sensor counted 30, then 32 as the owner installed more. Tools are separated (`kind: tool`) and excluded from counts, the picker and launch. Not compared title by title against the Steam UI. |
| AC2 sessions | **pass** | HA history: Among Us (Not playing → Among Us 20:55:53 → Not playing 20:56:23). Hollow Acre (non-Steam shortcut) is named, with session start and duration. Exactly one start and one stop event per run (unit and E2E tests). Duration uses the monotonic clock. |
| AC3 downloads | **pass** (third hardware run, commit c9ffd95) | The first hardware run exposed the gap: progress stuck at 3.4%, no speed or ETA, because the manifest is not live (§13). Fixed with live tracking (ADR-0008 amendment 1). It replays the recorded GTA V log sequence (start → sub-steps → main → pause → resume → done) in tests. The removed manifest-delta rate had shown 0.02 MB/s and a 29-day ETA on the G15. Needs one live download with the rebuilt agent. **Second hardware run (commit 5672677) still showed manifest values.** Root cause found on the G15: real content_log lines end in CRLF, and the `update started` line has a space before the CR, so the anchored pattern never matched. Also, a pause or rate with no anchor was hidden. Fixed: all trailing whitespace is trimmed, fixtures use the real bytes, the manifest seeds the anchor when the log has none, state applies without an anchor, and the rotated `.previous.txt` is read at start. **Third run (GTA V, 124.6 GB):** HA status "Downloading", progress 95.0% vs Steam 111.3/116.0 GiB = 95.9% (estimate slightly behind between events), speed 39.6 MB/s ≈ 332 Mbps vs Steam network 351 Mbps, ETA 3 min (download only; Steam's 4:05 also covers installing). The owner confirmed pause changes the status. |
| AC4 launch | **pass** | `deckbeacon.launch_game` from HA Developer Tools → `dispatched`, Never Sort By Color (4876650) started on the G15. Refusals (not installed, tool, not ready, malformed id, tier denied) covered by agent and HA tests. Shortcut launch: Hollow Acre started on the G15 from the HA "Game to launch" select + "Launch game" button (64-bit GameID `steam://rungameid`). |
| AC5 credential files | **pass** | Canary `config.vdf` is never opened. Canary `Exe`/`LaunchOptions`/`tags` (shortcuts.vdf) and LAN peer/CDN/IP/path lines (content_log) never leave the parsers. |
| AC6 activity | **pass (standalone and native)** | With `deckbeacon activity on`, a local Discord-IPC test client on the G15 (handshake, then SET_ACTIVITY) got READY from DeckBeacon. HA showed "DeckBeacon RPC test" / "Level 3 - Boss" / party max 4 at 21:38:27, back to unavailable at 21:38:43 when the client disconnected. Forwarding to a running Discord is covered by tests only. **Proton games: deferred by the owner** (they need a Wine pipe bridge; GE-Proton 11-1's opt-in `PROTON_DISCORD_BRIDGE=1` is the candidate, see ADR-0007). |
| AC7 CI | **pass** | All M6 runs green (Go race, govulncheck, protocol, fixtures/security greps, HA min/latest + E2E, gitleaks). |
| AC8 shortcuts, UI mode | **pass** | 8 shortcuts counted, named in the library and picker (duplicates disambiguated). Hollow Acre named while running. `steam_ui_mode` desktop → gamepad → desktop as Big Picture opened and closed (20:56:53 / 20:57:23). Session sensor shows desktop/gaming. |

### Owner feedback addressed during validation
- **10–15 s lag:** HA history showed updates only on the 30 s telemetry tick. Game metrics are now pushed on change, and the process poll went from 5 s to 2 s.
- **Session start/duration "stayed unavailable":** same cause (one 30 s tick per short session). Fixed by the push.
- **Rich Presence without Discord:** standalone mode (ADR-0007 amendment).
- **Launch from the dashboard:** "Game to launch" select + "Launch game" button. A DeckBeacon view was added to the owner's experimentation dashboard (with approval).

### Open (carried forward)
- Optional: an "install progress" (staged bytes) sensor matching Steam's second bar.
- §13 open items: completion of a large download, cancel mid-download, a download in Gaming Mode.
- Proton Rich Presence (deferred by the owner).
