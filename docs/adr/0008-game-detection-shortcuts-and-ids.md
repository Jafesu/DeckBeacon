# ADR-0008: Game detection, non-Steam shortcuts, and shortcut ID normalization

Status: Proposed (M6, 2026-11-17)

## Context
The G15 discovery (`docs/research/evidence/G15-m6-discovery-2026-11-17.md`, sections 7–12) captured a Proton game (Cyberpunk 2077) and a native non-Steam shortcut (Hollow Acre) starting and stopping. It also read `shortcuts.vdf` with the owner's approval and traced the Steam UI mode. The discovery left three questions for M6.

Facts that drive the decisions:
- **Process table:** in every case the real `reaper` process (`argv[0]` basename `reaper`, argv `SteamLaunch AppId=<n>`) exists for exactly the life of the game. The launch client and supervisor carry the same text inside their own argv, so matching whole command lines double-counts.
- **content_log `App Running`:** only Steam games produce it. Shortcuts log nothing there, and the file also holds LAN peer hostnames/IPs.
- **gameprocess_log:** both kinds log "adding PID" and `Remove <id> from running list`. Steam's tracked set lags real process exit (section 9). The first "adding PID" line holds the full launch command line, including home paths and launch arguments. Logs rotate at 1–8 MiB (`*.previous.txt`).
- **The only display name** for a shortcut is `AppName` in binary `userdata/*/config/shortcuts.vdf`. No process, environment variable or log line carries it. The appid can no longer be computed (the legacy CRC formula failed for 8 of 8 entries), so it must be read from the file. Names aren't unique (three entries are named "Discord"). Other entry fields (`exe`, `StartDir`, `LaunchOptions`, `ShortcutPath`, `icon`) contain home paths and arguments.
- **One shortcut, three ID forms:**

  | Form | Example | Seen in |
  |---|---|---|
  | signed int32 | `-50861101` | shortcuts.vdf `appid`, `Remove … from running list`, `SSGL: change [...]` |
  | unsigned 32-bit | `4244106195` | reaper `AppId=`, env `SteamAppId` |
  | 64-bit GameID | `18228297308309553152` | gameprocess_log `AppID … adding PID`, env `SteamGameId` |

  GameID = `(u32 << 32) | 0x02000000`. Checked arithmetically for every entry. GameID exceeds 2^53, so as a number it can't safely travel in JSON.
- **UI mode:** the latest `SSGL: UI mode (A->B)` line is the current UI: 4 = gamepad UI (Gaming Mode or Big Picture), 7 = desktop UI. The webhelper `-uimode` argv records only the start mode.

## Decision

### D1 Detection: the process watcher decides; the log adds UI mode only
- **Start and stop come from the process watcher alone.** Start = a `reaper SteamLaunch AppId=<n>` process appears (`argv[0]` must be `reaper`). Stop = no reaper with that id remains. This works for Steam games and shortcuts, needs no undocumented log format, and is already implemented (`internal/steam/procs.go`).
- **gameprocess_log.txt is tailed for one thing: the UI mode.** The tailer reads only lines that match `SSGL: UI mode \((\d+)->(\d+)\)` and discards every other line on read, so launch command lines and paths are never stored or sent. It handles truncation and rotation. It reads at most the last 256 KiB at start, then follows the file.
- **content_log.txt is not read** (Steam-only signal, contains LAN peer data).
- Corroboration from the log (e.g. a slightly earlier stop from `Remove … from running list`) is **not** used in M6. The process watcher's 2 s poll is precise enough, and one source of truth avoids conflicting signals. This can be revisited if hardware shows reaper lingering after a game exits.
- New metrics: `steam.ui_mode` = `gamepad` / `desktop` / `unknown:<n>` (raw unknown values are reported as-is). `session.type` = `gaming` (gamescope-session running) / `desktop` (Plasma) / `unknown`, from the process table. Together they distinguish Gaming Mode, Big Picture in Desktop, and Desktop.

### D2 shortcuts.vdf: four fields read, three sent
- **Read:** `appid`, `AppName`, `LastPlayTime`, `IsHidden`. Keys are matched case-insensitively. A strict binary-VDF reader (types 0x00 map, 0x01 string, 0x02 int32, 0x07 uint64, 0x08 end; size cap 1 MiB; depth cap 8) skips every other value without keeping it. `exe`, `StartDir`, `LaunchOptions`, `ShortcutPath`, `icon`, `tags`, `Devkit*` and `FlatpakAppID` are never retained, logged or sent.
- **Sent to HA** (library entry `kind: "shortcut"`): `app_id` (u32), `name` (AppName, length-capped at 256, shown as-is), `last_played`. `IsHidden` entries are left out of the library. The hidden flag itself isn't sent.
- **When:** at agent start and whenever the file's mtime changes (Steam rewrites it about 34 s after a shortcut exits). The path is `<steam>/userdata/*/config/shortcuts.vdf`. With several userdata directories, entries are merged by appid. The userdata directory name (a Steam account ID) is never sent or logged.
- **Name fallback** for a running shortcut not (yet) in the file: the reaper's argv exe basename without extension. The path itself is never sent.
- The credential-file deny list is unchanged. `shortcuts.vdf` is added to the explicit **allow** list as the only file read under `userdata/`.

### D3 One key per shortcut: unsigned 32-bit
- **Canonical key = u32.** It is a JSON-safe integer, matches what the reaper reports, and needs no schema change to `app_id`.
- Normalization (`steam.NormalizeID`):
  - signed int32 `< 0` → `+ 2^32`;
  - 64-bit value `> 2^32` with `(id >> 24) & 0xFF == 0x02` → `id >> 32`;
  - Steam game GameID (type byte 0) → the low 24 bits (equal to the appid);
  - anything else is rejected.
- Steam games and shortcuts share the u32 space without overlap in practice: shortcut ids have the high bit set (all 8 observed are ≥ 2^31), and Steam appids are far below that. `kind` disambiguates anyway.
- **Launch of shortcuts:** `steam steam://rungameid/<GameID>`, with GameID computed from the u32 and formatted as a decimal string (never a JSON number). This is the same fixed-argv path as Steam games. Allowed only for ids present in shortcuts.vdf and not hidden. **This needs a hardware check** (one owner-approved launch of a shortcut on the G15) before AC4 claims it. Until then the capability `games.launch_shortcut` reports `unverified`.

## Consequences
- Protocol **1.3 (minor, additive):** library entries gain an optional `kind` (`"game"` default, `"shortcut"`) plus optional `last_played`. New metric keys `steam.ui_mode` and `session.type`. Old HA versions ignore them (lenient receivers).
- HA: shortcuts appear in `list_games` / the current-game sensor by name. `deckbeacon.launch_game` accepts shortcut ids, under the same `game_management` tier.
- Reading shortcuts.vdf exposes shortcut names to HA. They are user-chosen and may be personal, which is why the hidden entries are excluded. The owner approved names, IDs and last-played time (2026-11-16).
- If a future Steam build changes the GameID layout, normalization rejects the id. The game then shows by u32 with the fallback name, never with a wrong name.

## Validation
- Unit tests: a binary-VDF fixture built from the section 11 shape (synthetic names and paths), with a canary `exe`/`LaunchOptions` value asserted to never reach the output. Normalization table tests using the three recorded forms of all 8 ids. The UI-mode tailer is tested with rotation, truncation, and lines that carry paths.
- G15 (owner-approved, read-only except the one launch): the library shows the 7 non-hidden shortcuts with names. Starting and stopping Hollow Acre gives `game_started`/`game_stopped` named "Hollow Acre". Switching Big Picture changes `steam.ui_mode`. One shortcut launch from HA.
