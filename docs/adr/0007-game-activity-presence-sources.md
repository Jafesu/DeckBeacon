# ADR-0007: Game activity ("rich presence") sources

Status: Proposed (M6, 2026-11-17). The owner asked for Discord-style activity details (level, area, etc.).

## Context
"What game is running" comes from the process table (`reaper SteamLaunch AppId=…`, ADR-0004 Tier A). The owner wants richer, game-specific state. Research (2026-11-17):

- **Steam Rich Presence**: games set key/values. The Steam client renders them through the game's published localisation tokens (`steam_display`). There is **no local file and no public Web API** to read your own rich presence. It is visible only inside the Steam client, which means reaching it via Decky's undocumented `SteamClient` (ADR-0004 Tier C).
- **Discord Rich Presence**: games built with Discord RPC connect to a local IPC socket (`$XDG_RUNTIME_DIR/discord-ipc-0` … `-9`) and send `SET_ACTIVITY` with `details`, `state`, timestamps and party size. The protocol is half-documented but stable, with open re-implementations (e.g. arRPC). Proton games use a Windows named pipe and reach the Linux socket only through a bridge (Valve's Proton wiki documents this).
- **Valve Game State Integration (GSI)**: official for CS2 and Dota 2. A `gamestate_integration_*.cfg` in the game's cfg folder makes the game POST JSON (map, round, player state) to a local HTTP endpoint.

## Decision
Activity details are an **optional, layered** feature. Each source is a separate capability with its own status and an explicit opt-in:

| Tier | Source | Default | Notes |
|---|---|---|---|
| A | Process table: app id, name, session start/duration | on | Always available. |
| P1 | **Discord IPC proxy** in the agent: listens on the first free `discord-ipc-N`. Because the agent starts at boot (before Discord), it usually holds slot 0. It forwards every frame unchanged to the real Discord client if one is running on a later slot, so the user's Discord status is never broken. It reports `details`/`state`/party/elapsed to HA. | **off (experimental)** | Only games with Discord RPC. Proton games need a bridge (documented, not installed by DeckBeacon). Payload text is shown as-is (untrusted), length-capped, never executed. Images/URLs from the payload are dropped. |
| P2 | Valve GSI for CS2/Dota 2 | off | Needs a cfg file written into the game's folder: explicit per-game consent, and removal on opt-out. Later milestone. |
| P3 | Steam Rich Presence via Decky `SteamClient` | off (experimental) | M4. Undocumented, Steam-build gated. |

- No source may impersonate a service *outside* the device. The Discord proxy only serves local IPC and forwards to the local Discord client. It never talks to Discord's servers itself.
- Privacy: activity text is sent to HA only while the capability is enabled. It is never stored in agent history beyond the current session.

## Consequences
- Coverage is partial and game-dependent, and HA shows `unavailable` when no source has data (never guessed).
- The Discord proxy must handle Discord starting before the agent (agent takes the next free slot, so games that connect to slot 0 reach Discord directly and we get nothing). That case is reported as reduced capability.

## Validation
G15 discovery (`docs/research/evidence/G15-m6-discovery-2026-11-17.md`). A unit test of the IPC framing against recorded/spec frames. A live test with a Discord-RPC-enabled game on the G15 when available.
