# Milestone 0 — Research and Feasibility Report

| | |
|---|---|
| Status | **Approved 2026-11-16** with amendments recorded in [`M0-decisions.md`](M0-decisions.md). |
| Date | 2026-11-16 |
| Scope | Spec v1.0 (`docs/specification/00–15`), `CLAUDE.md`, `AGENTS.md` |
| Companion docs | [`M0-decisions.md`](M0-decisions.md), [`capability-matrix.md`](capability-matrix.md), [`hardware-validation-plan.md`](hardware-validation-plan.md), [`../adr/`](../adr/) |
| Evidence | [`research/spikes/ha-device-transport/`](../../research/spikes/ha-device-transport/) |

## 1. Executive summary

The product is **feasible as a local-first HA companion**, with three structural changes and one open blocking gate:

1. **One transport direction (ADR-0001).** The agent always dials *into* a WebSocket endpoint that the DeckBeacon HA integration registers on Home Assistant's own HTTP server, for both LAN and remote. No inbound port on the Deck, and no HA access token on the device. A working local prototype of this pattern exists (§4).
2. **The integration does its own device authentication and encryption (ADR-0002).** Home Assistant has no scoped/restricted tokens. Any HA token is a full user credential, so the spec's "no unrestricted HA token on devices" rule *requires* custom device auth. Local HA is commonly plain `http://`, so TLS alone can't meet "TLS for network transport". The plan is an end-to-end Noise-style channel, keyed at pairing.
3. **Prefer user-service lingering over a root boot helper (ADR-0003).** `loginctl enable-linger` starts the user agent at boot without root. A root helper on SteamOS needs a sudo password that the default `deck` user does not have, and it adds immutable-OS persistence risk.
4. **Blocking gate — HA Cloud remote (spec 09, Q1): ✅ PASSED end to end on 2026-11-16** ([HV-R1 evidence](evidence/HV-R1-2026-11-16.md)). Original M0 assessment: The Nabu Casa remote UI terminates TLS into the *same* aiohttp application that serves HA's API (`snitun.SockSite(runner)` on HA's `AppRunner`). Its only filter is an IP whitelist, not a path filter. So a custom integration view, WebSocket included, should be reachable at the remote URL. This must be proven with a real subscription before any remote claim (test procedure: `hardware-validation-plan.md` §HV-R1).

Steam-side reality is the largest product risk. **Read-only** library, artwork, Proton inventory, and approximate running-game and download state are achievable from local files and processes. **Launch** is achievable via Steam's URL handler. **Close game, pause/resume downloads, set compatibility tool, and move game** have **no supported public interface**. They exist only through Steam's undocumented in-client `SteamClient` JS API, reachable from a Decky plugin. These must ship as *experimental, disabled-by-default, version-gated* capabilities, or be dropped (ADR-0004).

Distribution needs one change: as of **HA 2026.11, HACS is now the built-in *Marketplace***, and a Marketplace repository "holds one item of one type". The monorepo must publish to per-item distribution repositories (ADR-0005).

**Nothing in this milestone was validated on SteamOS hardware.** This environment is a generic Linux container. Every SteamOS/Steam/Decky row in the capability matrix stays `unknown` or `research-only` until the hardware validation plan is run on a real device.

## 2. Method and evidence sources

| Source | What it established | Confidence |
|---|---|---|
| Local HA core 2026.2.3 (PyPI, Python 3.13) running a spike custom integration and a Go client | Custom `HomeAssistantView` with `requires_auth=False` can host a WebSocket with device-key auth, receive telemetry, push commands, and dedupe acks. The device needs no HA token. | **Tested** (output in `RESULTS-2026-11-16.txt`) |
| `hass-nabucasa` 1.12.0 / `snitun` 0.45.1 source | Remote UI binds a `SockSite` to HA's aiohttp `AppRunner`, so every registered view is reachable through the tunnel. The only filter is per-IP. | Source review; **not E2E tested** |
| Read-only system health of the project owner's HA (via the HA MCP connector) | Real target: HA core 2026.11.1, HAOS, aarch64, Python 3.14, HA Cloud remote enabled and connected, Marketplace (ex-HACS) present. No identifiers recorded. | Observed |
| systemd upstream `org.freedesktop.login1.policy` | `set-self-linger` is `yes` for any/inactive/active. `suspend`/`power-off` are `yes` only for *active* sessions, otherwise `auth_admin_keep`. | Upstream source; SteamOS overrides **unverified** |
| `@decky/api` 1.1.3 type definitions (npm) | `toaster.toast()` takes a single `onClick` and has **no action buttons**. Frontend ↔ backend uses `call`/`callable`/events. | Source |
| decky-loader `plugin.py` | Backend runs as the host user unless `plugin.json` `flags` contains `"root"`. The loader itself is root. | Source |
| decky-plugin-database README / template | Store distribution is a git **submodule** per plugin, reviewed by PR. Zip URL install is also possible. | Docs |
| HA Marketplace contributor docs (`contributors.home-assistant.io/marketplace`) | One item of one type per repository. Integration ZIP releases (`zip_release` + `filename`). The manifest `version` must equal the release tag, and releases must be published. | Docs |
| Community/forum sources on Steam files and URIs | `steam steam://rungameid/<id>` is the standard CLI launch. `config.vdf`/`localconfig.vdf`/ssfn files contain login tokens and private data. | Community; must be validated |
| Valve hardware news (Nov 2025 announcement) | Steam Machine (x86) and **Steam Frame (Arm/Snapdragon)** run SteamOS, so arm64 builds are needed. | News; device availability unverified |

## 3. Architecture review — conflicts and gaps

### 3.1 Conflicts between spec documents

| # | Conflict | Where | Recommendation |
|---|---|---|---|
| C1 | HA "Zeroconf discovery" of the agent (04) implies the agent advertises and **listens** on the LAN. 03 says "bind loopback only", and 09 says "agent initiates outbound… no required inbound Deck ports". | 03, 04, 09 | **ADR-0001**: the agent discovers HA (`_home-assistant._tcp`, or a manual URL) and dials in. It announces itself through the pairing endpoint, which raises an HA *integration discovery* flow, so the user still sees "Discovered: Steam Deck" in HA. The agent opens no LAN listener. |
| C2 | "Pairing works without Decky through local UI" (11) plus "explicit on-device approval" (10). But in **Gaming Mode there is no browser** to reach a loopback web UI, so without Decky the user has no on-device surface. | 05, 10, 11 | Pair by comparing a **short authentication string (SAS)**. HA shows a code; the user confirms it on the device through *any* of: the Decky plugin, the loopback web UI in Desktop Mode, or the `deckbeacon pair` CLI (Desktop Mode Konsole or SSH). Optional later: a non-Steam "DeckBeacon Pairing" shortcut that launches a minimal gamepad-navigable window. **Decision needed** on whether Gaming-Mode-without-Decky pairing is required at v1. |
| C3 | "All sources in one monorepo" (00) vs. HA Marketplace "one item of one type per repository" and the Decky store's submodule model. | 00, 11, 12 | **ADR-0005**: the monorepo is the source of truth. CI publishes release-only mirror repos (`deckbeacon-ha`, `deckbeacon-card`, `deckbeacon-decky`). |
| C4 | Spec says HACS (04, 11, 12). HA 2026.11 replaced HACS with the built-in **Marketplace**, which needs `hacs.json` in each release and a manifest version equal to the tag. | 04, 11, 12 | Target the Marketplace. Keep `hacs.json` (still the file name), and set its `homeassistant` minimum. |
| C5 | Optional root **boot helper** (01, 03) vs. "no unrestricted root daemon" and "no silent privilege escalation". The SteamOS `deck` user has **no password by default**, so installing any root unit requires the user to set one. `/etc` persistence across A/B updates is unverified. | 01, 03, 11 | **ADR-0003**: replace the helper with **user lingering**: same single agent, single identity, no root. The boot helper becomes a deferred milestone, opened only if lingering fails a validated need. |
| C6 | "Distinct transport connection state and physical power state" (01). HA cannot tell "asleep" from "powered off" from "lost Wi-Fi" unless the device says so first. | 01, 03 | The agent takes a logind **delay inhibitor** on `PrepareForSleep`/`PrepareForShutdown`, sends `power_state=suspending|shutting_down`, then releases it. HA keeps the last *announced* power state with a timestamp, and otherwise reports `unknown`. Validate (HV-P3). |
| C7 | Actions list (04) includes `install_game`, `move_game`, `uninstall_game`, `set_compatibility_tool`, `pause/resume_downloads`, `close_game`. 02/06 forbid writing Steam data. Only Steam's own UI or undocumented `SteamClient` can perform these. | 02, 04, 06 | Register the actions per the contract, but runtime-gate each by capability. Most will report `unsupported` without Decky and `experimental/disabled` with it (ADR-0004). `install`/`uninstall` via `steam://install|uninstall/<id>` only *opens Steam's own confirmation dialog* on the device. They are reported as "dialog opened", not completed. |
| C8 | "Monotonic durations" for sessions (06), yet sessions span suspend. Go's monotonic clock (`CLOCK_MONOTONIC`) **stops during suspend**. | 06 | Segment sessions at suspend and resume, and record wall-clock plus `CLOCK_BOOTTIME`. "Play time" excludes suspended time by definition. Document it. |

### 3.2 Missing or under-specified requirements

| # | Gap | Proposed default (for approval) |
|---|---|---|
| G1 | Minimum supported versions (Q9). | **Decided (D4):** HA ≥ **2026.5** (6 months back, covering Python 3.14). The Marketplace path requires ≥ 2026.11. Earlier versions install manually or via legacy HACS. SteamOS ≥ the current Stable at M2 start. Decky ≥ current stable at M4 start. Pin in M1. |
| G2 | Initial hardware matrix (Q6). | **Decided (D5):** Tier 1 = Steam Deck LCD/OLED and genuine SteamOS on non-Valve x86_64 hardware (test unit: Dell G15). arm64 (Steam Frame) is build-only until hardware is available. |
| G3 | Multiple HA instances per device? 01 says "one active logical session per HA pairing", which implies several pairings are allowed. | v1: **exactly one** HA pairing per device. A second pairing requires revoking the first. Multi-HA is deferred. |
| G4 | Multiple Steam accounts on one device. | Report the active Steam account only (from `loginusers.vdf` `MostRecent`, numeric ID, hashed by default). Library is per device. Playtime is per active account. |
| G5 | Clock skew for `expires_at` (07, 10). | The wire protocol carries a relative TTL plus HA send time. The agent checks it against its own receive time. Absolute times are informational only. |
| G6 | Rate and size limits on the pre-auth endpoint (internet-exposed through HA Cloud). | 64 KiB max message, 5 s auth timeout, per-IP and global connection limits, failed auth reported through HA's `http.ban.process_wrong_login`. |
| G7 | Telemetry cadence and HA recorder load. | Default 30 s for system metrics, change-driven for Steam state, `state_class` only where long-term stats make sense. High-churn values sit behind a per-entity option. |
| G8 | Retention defaults (Q8). | Session history 90 days locally, never sent with titles unless opted in. Notification log 7 days. Diagnostics bundle redacted by allowlist. |
| G9 | Offline command semantics (Q7). | Commands carry a TTL (default 60 s, max 24 h for update approval). Expired commands become `expired` with a reason. Non-idempotent commands are never replayed after reconnect without a fresh TTL. |
| G10 | Device identity key storage. | Ed25519/X25519 keys in `$XDG_STATE_HOME/deckbeacon/identity` (0600, dir 0700). No TPM/keyring dependency. Optional passphrase-encrypted export for backup (10). |
| G11 | Uninstall and revocation. | `deckbeacon uninstall` notifies HA (`device_removed`) when it can connect. HA "Delete device" revokes the key, so the agent's next connect gets `revoked` and returns to `unpaired`. |
| G12 | Artwork licensing and size. | Serve only the local Steam cache through HA's authenticated proxy (08). No external fetch by default. Size cap and LRU cache in HA. |

## 4. Interface validation

### 4.1 Home Assistant

| Interface | Finding | Evidence |
|---|---|---|
| Custom HTTP/WebSocket endpoint in an integration | **Works.** `HomeAssistantView` (`requires_auth=False`) plus `aiohttp.web.WebSocketResponse`, served through HA's middleware stack (security filter, ban, forwarded). Device auth by Ed25519 challenge/response: valid key accepted; unenrolled key rejected; telemetry → state; HA→device command; duplicate ack detected. HA REST without a token returns 401, and the device key grants nothing on `/api/websocket`. | Spike T1–T4 |
| Scoped / restricted HA tokens | **Do not exist in core.** Long-lived tokens carry full user rights for 10 years. Non-admin users still reach most of the API. | HA auth docs, community threads |
| HA Cloud remote UI carrying the endpoint | **Likely.** The tunnel serves HA's aiohttp app, with no path filtering. WebSockets work (the frontend depends on them). Unknowns: idle/connection limits, per-instance concurrency, fair-use for persistent device connections, behaviour when remote UI is toggled off, and reconnect on Nabu Casa relay migration. | Source review; **HV-R1 passed 2026-11-16** (functional + 15 min soak). Still open: toggle-off behaviour, relay migration, multi-hour soak (M5). |
| Cloudhooks | One-way inbound webhooks (with a response), not a bidirectional transport. A possible fallback for HTTPS long-poll only. | Spec 09 / HA docs |
| Remote URL discovery | `homeassistant.helpers.network.get_url(prefer_cloud=…)` gives the integration internal, external, and cloud URLs. They are handed to the agent inside the encrypted channel during pairing. | HA helper API (standard) |
| Native platforms (sensor, binary_sensor, button, switch, number, select, update, event, notify) | All available in 2026.x. `NotifyEntity` for basic notify. `UpdateEntity` with an install feature provides the approval UX for agent updates. Actions with `SupportsResponse` cover `list_games`/`get_game`. | Standard HA APIs |
| Integration discovery flow from a custom endpoint | `hass.config_entries.flow.async_init(DOMAIN, context={"source": SOURCE_INTEGRATION_DISCOVERY}, …)` raises a "Discovered" card. Abuse must be rate-limited (ADR-0002). | Standard HA API; prototype in M2 |
| Distribution | Marketplace (2026.11+) replaces HACS. One item per repo. `zip_release` is integrations only. Releases must be published, and the manifest version must equal the tag. | Contributor docs |

### 4.2 SteamOS

| Interface | Finding | Status |
|---|---|---|
| `systemd --user` service + linger | Upstream polkit lets any user self-enable linger. With linger the user manager starts at boot, before or without login. Deck auto-login means a session exists in both modes anyway. | Upstream-verified; **SteamOS override unverified** (HV-S1) |
| Power actions (suspend/reboot/poweroff) | logind allows these for an *active* session. Agent processes live in `user@.service`, not the session scope. polkit falls back to the user's display session, so this should work while the `deck` session is active, and not while only lingering. Also, Steam in Gaming Mode expects to own suspend. A logind suspend behind its back may desync the UI. Prefer `SteamClient.System` via Decky when present. | **Unverified** (HV-P1/P2) |
| Sleep/shutdown pre-notification | logind delay inhibitor and `PrepareForSleep` signal. A user process can normally take a `delay` lock. | **Unverified** (HV-P3) |
| Immutable root | `/` is read-only btrfs with A/B images. `/home` persists. The agent installs entirely under `~/.local` and `~/.config`, so no root is needed. | Well documented; re-check on device |
| Telemetry sources | `/proc/stat`, `/proc/meminfo`, `statfs`, `/proc/uptime`. `amdgpu` `gpu_busy_percent` and hwmon. Deck-specific hwmon (`steamdeck_hwmon`/`jupiter`) for fan and temps. `power_supply/BAT1`. DMI `board_name` (`Jupiter` LCD / `Galileo` OLED) for model. `os-release` `ID=steamos`. | Paths from community knowledge; **unverified** (HV-T*) |
| Session mode (Gaming vs Desktop) | Gaming = `gamescope-session` (gamescope is the compositor). Desktop = Plasma. Detect via the user's session units and process, with confidence recorded. | **Unverified** |
| Volume | PipeWire/WirePlumber (`wpctl`) as the user. | Likely; unverified |
| Brightness | `/sys/class/backlight/*/brightness` is root-writable only by default. In Gaming Mode Steam owns brightness. Read-only from sysfs; writing via Decky `SteamClient` only. | Read likely; write **experimental** |
| mDNS | Not needed for the agent to be discovered (ADR-0001). The agent only *browses* `_home-assistant._tcp`, which needs multicast on 5353 (avahi may already be running, and Go browsers coexist via `SO_REUSEADDR`). | Unverified |
| Wake from sleep/off | No supported WoL on Deck Wi-Fi. Dock Ethernet WoL is hardware-dependent. | Treat as `unsupported` unless proven per device |
| arm64 (Steam Frame) | Go cross-compiles trivially. Steam paths and process names under Arm (Steam via an x86 emulation layer) are unknown. | Build-only target until hardware is available |

### 4.3 Steam client

| Interface | Finding | Status |
|---|---|---|
| Library | `~/.local/share/Steam/steamapps/libraryfolders.vdf` and `appmanifest_<id>.acf` (KeyValues text). Read-only parse with an allowlist of keys. Removable libraries appear and disappear as SD cards mount. | High confidence; validate paths |
| Running game | Candidates: (a) `reaper SteamLaunch AppId=<id>` process argv, (b) `registry.vdf` `RunningAppID`, (c) the gamescope `STEAM_GAME` X atom, (d) Decky `SteamClient`. Combine with recorded provenance and confidence. | **Unverified** — choose in HV-G2 |
| Download state | Without Decky: `appmanifest` `StateFlags`, `BytesToDownload`/`BytesDownloaded`/`BytesStaged`, and `logs/content_log.txt`. Speed derived. Queue order and "scheduled" are not reliably available. With Decky: `SteamClient.Downloads.*` (undocumented). | File source likely; **must validate** |
| Launch | `steam steam://rungameid/<id>` forwards to the running client over Steam's IPC pipe. It must run as the `deck` user with the session environment. Gives "request accepted" only. "Running" is confirmed through the running-game detector. | Community-documented; **HV-G3** |
| Install / uninstall / validate | `steam://install/<id>`, `steam://uninstall/<id>`, `steam://validate/<id>` open Steam's own UI/dialog on the device. They require a person at the device. | Community-documented; unverified |
| Close / pause / resume / set compat tool / move | **No public interface.** Only undocumented `SteamClient.Apps.*`/`SteamClient.Downloads.*` inside Steam's CEF context (Decky). These can break on any Steam client update. | **Experimental only** (ADR-0004) |
| Proton inventory | Registered tools: `steamapps/common/Proton*`, `compatibilitytools.d/*/compatibilitytool.vdf`. Per-game overrides: `config.vdf` `CompatToolMapping`. **That file also holds login tokens (`ConnectCache`)**, so parse only that subtree, in-process, and never copy, log, or upload the file. | Read feasible with strict handling |
| Artwork | Local `appcache/librarycache/` (layout changed in 2024 to per-app subdirectories; detect both). | Validate on device |
| Steam Web API / store metadata | External, optional, opt-in only. Store `appdetails` is unofficial and rate-limited. | Out of scope for v1 default |

### 4.4 Decky Loader

| Interface | Finding |
|---|---|
| Plugin model | Frontend TS/React in Steam's shared CEF context (`@decky/ui`, `@decky/api` 1.1.x). Python backend `main.py`, or a custom binary in `bin/`. The backend runs **as the host user unless `"root"` is in `plugin.json` flags.** DeckBeacon must not request root. |
| Toasts | `toaster.toast({title, body, onClick, duration, critical, …})`, a **single click handler with no action buttons**. Interactive actions: the toast click opens the DeckBeacon notification center (a plugin route), which shows the action buttons. This is the fallback that spec 05/07 anticipated. |
| Steam-facing adapter | The global `SteamClient` object is reachable from plugin frontend JS. It is undocumented, unversioned, and changes with Steam client updates. Each call must be feature-detected and wrapped, with a per-Steam-build allow/deny table. |
| Trust boundary | The Decky Loader process is root. All plugins share one JS context. A malicious or compromised plugin can call our backend and read anything our plugin can read. The Decky bridge therefore gets the **lowest privilege tier** (ADR-0006). |
| Distribution | The decky-plugin-database PR adds a git submodule. Plugin licence at repo root. Store-review rules (custom backends get extra review). Zip-URL install for beta/testing. |

## 5. Unsupported assumptions (stop-and-report per AGENTS.md)

| # | Assumption in spec | Reality | Impact | Blocking? |
|---|---|---|---|---|
| U1 | HA can hand the device a restricted credential | No scoped tokens exist | Custom device auth is required (ADR-0002) | Resolved by design |
| U2 | TLS covers local transport | Local HA is often `http://` | App-layer encryption is required (ADR-0002) | Resolved by design |
| U3 | HA Cloud carries authenticated bidirectional device traffic | **Proven** (HV-R1: functional + 15 min soak, p50 92 ms) | Remote transport unblocked. Production soak from real device in M5 | Resolved |
| U4 | Supported Steam interfaces exist for close, pause/resume, compat change, move | They don't (public) | Experimental via Decky or dropped | **Product decision** |
| U5 | Interactive Gaming Mode notification actions | Toast has only `onClick` | Notification-center fallback | No |
| U6 | Boot helper is a reasonable optional component | Needs sudo password, root unit on immutable OS | Replace with linger | **Decision** (ADR-0003) |
| U7 | One monorepo distributes to HACS and Decky | One item per Marketplace repo; submodule store | Mirror repos | No (ADR-0005) |
| U8 | Pairing without Decky via local UI works in Gaming Mode | No browser surface in Gaming Mode | SAS via CLI/Desktop/Decky | **Decision** (C2) |
| U9 | Power commands work from the agent | Depends on polkit session state and Steam's ownership of suspend | Validate; prefer Steam path | HV-P1/P2 |

## 6. Security risk register (summary)

Full threat model is a Milestone 1 deliverable. Key risks found now:

| Risk | Threat | Mitigation |
|---|---|---|
| R1 | Pre-auth parser exposed to the internet via HA Cloud | Minimal pre-auth state machine, size and time limits, rate limits, HA ban integration, fuzz tests on the handshake |
| R2 | Pairing-endpoint spam or rogue device enrolment | The endpoint returns 404 unless a pairing window was opened from the HA UI (≤ 10 min). The SAS must be confirmed in HA *and* on the device. One pending request per window. |
| R3 | Spoofed HA on the LAN (mDNS) | Discovery only proposes. Trust comes from SAS-confirmed static keys pinned at pairing. The agent refuses any server key change without re-pairing. |
| R4 | LAN sniffing or session hijack on `http://` HA | Noise channel (mutual static keys, forward secrecy). The spike's plain challenge/response is **not sufficient** and is spike-only. |
| R5 | Stolen device key / backup clone | Per-install key. The server rejects concurrent sessions for one device ID (clone detection plus a repair issue). Revoke from HA. |
| R6 | Steam credential leakage | Allowlisted file and key reads. `config.vdf`, `localconfig.vdf`, `loginusers.vdf`, and `ssfn*` are never copied, logged, or bundled. Redaction tests on diagnostics. |
| R7 | Command injection through Steam URIs or args | App IDs are validated as unsigned 32-bit integers. `exec` with an argv array, never a shell. URI built from a fixed template. |
| R8 | Compromised Decky plugin or loader (root) | Decky bridge tier = view status, answer *its own* notifications, confirm SAS. It can't change permissions, run power/admin commands, or approve updates by itself. Residual risk: root can read agent keys. Documented as accepted. |
| R9 | Loopback web UI CSRF / DNS rebinding | Strict `Host`/`Origin` checks, per-launch random token in the URL, same-site cookies, no CORS. Unix socket for Decky. |
| R10 | HA account compromise → device control | Agent-side permission tiers per transport (local vs remote). Power, admin, and destructive game ops are off by default for remote. Optional on-device confirmation. Audit log on the agent. |
| R11 | Compromised release artifact | Signed releases (e.g. minisign/Sigstore). Public key embedded in the agent. The updater verifies signature, version monotonicity, and compatibility before the slot switch. |
| R12 | Notification payload abuse | Notifications are data only. Action responses become HA *events*; HA automations decide. Arbitrary service calls are never accepted (spec 07). |

## 7. Recommended architecture changes (ADRs, all **Proposed**)

| ADR | Title | Changes spec |
|---|---|---|
| [0001](../adr/0001-agent-dials-ha-single-transport.md) | Agent dials a DeckBeacon endpoint hosted inside HA for both LAN and remote | 01, 03, 04, 09 |
| [0002](../adr/0002-device-identity-pairing-and-channel-security.md) | Device keys, SAS pairing, Noise channel; no HA tokens on device | 09, 10 |
| [0003](../adr/0003-linger-instead-of-root-boot-helper.md) | User lingering replaces the root boot helper (helper deferred) | 01, 03, 11, roadmap M3 |
| [0004](../adr/0004-steam-integration-tiers.md) | Steam integration tiers: files (read-only) → Steam URL handler → Decky `SteamClient` (experimental) | 02, 06 |
| [0005](../adr/0005-distribution-mirror-repositories.md) | Monorepo as source of truth with generated distribution repos; target HA Marketplace | 00, 11, 12 |
| [0006](../adr/0006-decky-bridge-trust-tier.md) | Decky bridge is a low-trust local client over a Unix socket | 05, 10 |

Roadmap impact: **M3 (boot-time option)** shrinks to "linger opt-in + boot/login/suspend validation". The root helper becomes an unscheduled, demand-driven milestone. **M5 (remote)** reuses the M2 transport unchanged. Only URL selection and failover are new, provided HV-R1 passes.

## 8. Decisions requested from the reviewer

> Answered 2026-11-16. See [`M0-decisions.md`](M0-decisions.md).

1. Approve or amend ADR-0001…0006.
2. **C2/U8:** Is pairing in Gaming Mode *without* Decky a v1 requirement? (Recommended: no. CLI and Desktop Mode are acceptable for v1.)
3. **U4:** Ship close, pause/resume, compat-tool, and move as *experimental Decky-only, disabled by default*, or drop them from v1? (Recommended: experimental, disabled by default.)
4. **G1/G2:** Minimum HA version and hardware tiers.
5. Run **HV-R1** (HA Cloud end-to-end) against the owner's HA Cloud-enabled instance: install the spike integration temporarily, test, then remove. I will not touch the production HA without explicit approval.
6. Provide access to a SteamOS device (SSH or attached machine) for the hardware validation plan, or agree that M2 proceeds with mocked providers and hardware rows stay `unknown`.

## 9. What Milestone 0 did **not** do

- No production code. The spike under `research/spikes/` is deliberately throwaway, insecure (no channel encryption), and excluded from any build.
- No SteamOS, Steam, or Decky behaviour was observed on hardware.
- No changes were made to the owner's Home Assistant. One read-only system-health query was made.
