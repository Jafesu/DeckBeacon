# Capability Matrix (Milestone 0 baseline)

Living document. Each row moves forward only with recorded evidence (test ID, SteamOS build, Steam client build, hardware model, date).

**Research status** (evidence maturity):
- `verified` — tested end-to-end in this repo's evidence.
- `source` — confirmed from upstream source code or official docs, but not run on target.
- `research` — community knowledge or a plausible candidate. Needs validation.
- `experimental` — works only through undocumented interfaces. Ships disabled and version-gated.
- `unsupported` — no viable safe interface known.

**Runtime default** uses the spec's runtime vocabulary (`available` / `unsupported` / `disabled` / `unavailable` / `unknown`). It says what the agent will report when the capability is implemented and its validation passes. Until then, everything reports `unknown`.

**Tested hardware: none yet.** M2 adds collector implementations exercised against *synthetic* trees (Steam Deck OLED, Dell G15). These are code tests, not hardware evidence, so the rows below stay `research`. All SteamOS rows are pending the [hardware validation plan](hardware-validation-plan.md).

**Tier 1 targets (D5):** Steam Deck LCD, Steam Deck OLED, and genuine SteamOS on non-Valve x86_64 hardware (reference unit: Dell G15, Intel/AMD CPU + NVIDIA GPU). Providers are chosen by runtime detection. "Device-dep." rows must list a result per reference unit.

## Transport and platform

| Capability | Chosen source | Privilege | Needs Decky | Research status | Runtime default | Validation |
|---|---|---|---|---|---|---|
| Device-key-authenticated WS into HA (LAN) | Custom `HomeAssistantView` WebSocket + Noise IK (ADR-0001/0002) | none on device | no | **verified** (M2: Go agent ↔ integration E2E on HA 2026.5.1 and 2026.10.4; container, not SteamOS) | available | M2 AC1–AC9 |
| Same endpoint via HA Cloud remote URL | Nabu Casa remote UI → HA aiohttp app | none | no | **verified** (HV-R1, HA 2026.11.1, 15 min soak, p50 92 ms) | available | [HV-R1](evidence/HV-R1-2026-11-16.md); multi-hour soak from device in M5 |
| Same endpoint via user's external HTTPS/WSS URL | Reverse proxy → HA | none | no | source (standard HTTP) | available | HV-R2 |
| HA restricted token on device | — | — | — | **unsupported** (does not exist) | n/a | — |
| Agent start at boot without login | `systemd --user` + linger via `deckbeacon service install --at-boot` (M3) | none (polkit self-linger) | no | source (upstream polkit) | disabled until opted in | HV-S1 |
| Agent in Gaming & Desktop modes | `systemd --user` | none | no | research | available | HV-S2 |
| Root boot helper | system unit | root + sudo password | no | research (deferred, ADR-0003) | unsupported (v1) | — |
| Sleep/shutdown pre-announce | logind delay inhibitor + `PrepareForSleep`/`PrepareForShutdown` (M3; tested against a fake login1 on a private D-Bus) | none (expected) | no | research | available when logind reachable | HV-P3 |
| Resume detection | logind `PrepareForSleep(false)`; fallback wall/monotonic clock divergence (heuristic) | none | no | research | available | HV-P3 |
| Boot detection | kernel `boot_id` change → `device_booted` | none | no | research | available | HV-S2 |
| Discover HA on LAN | mDNS browse `_home-assistant._tcp` | none | no | research | available | HV-N1 |

## System telemetry

| Capability | Chosen source | Privilege | Needs Decky | Research status | Runtime default | Validation |
|---|---|---|---|---|---|---|
| CPU usage, load | `/proc/stat`, `/proc/loadavg` | none | no | research (standard Linux) | available | HV-T1 |
| RAM / swap | `/proc/meminfo` | none | no | research | available | HV-T1 |
| Storage per mount / library | `statfs` | none | no | research | available | HV-T1 |
| Uptime / boot time | `/proc/uptime`, `CLOCK_BOOTTIME` | none | no | research | available | HV-T1 |
| GPU busy % (AMD) | `/sys/class/drm/card*/device/gpu_busy_percent` | none | no | research | available (device-dep.) | HV-T2 |
| GPU busy % / temp (NVIDIA) | NVML via `nvidia-smi` if the proprietary driver is present; otherwise unavailable | none | no | research | available or unavailable (detected) | HV-T7 |
| CPU/GPU/APU temps | hwmon (`amdgpu`, `k10temp`, Deck hwmon) | none | no | research | available (device-dep.) | HV-T2 |
| Fan RPM | Deck hwmon; `dell_smm_hwmon`; any hwmon `fan*_input` | none | no | research | available (device-dep.) | HV-T2/T7 |
| Battery %, charging, rate | `/sys/class/power_supply/BAT*` | none | no | research | available on handhelds | HV-T3 |
| Model identification | DMI `board_name`/`product_name` | none | no | research | available | HV-T4 |
| OS version / channel | `/etc/os-release` | none | no | research | available | HV-T4 |
| Session mode (Gaming/Desktop) | gamescope-session vs Plasma detection | none | no | research | available (confidence) | HV-S2 |
| Docked / external display | DRM connector status, power_supply AC, USB-C (laptops: "external display connected") | none | no | research | available (heuristic) | HV-T5 |
| Volume read/write | PipeWire `wpctl` | none | no | research | available | HV-T6 |
| Brightness read | sysfs backlight | none | no | research | available | HV-T6 |
| Brightness write | Decky `SteamClient` | — | yes | experimental | disabled | HV-D3 |
| Network: interface, SSID, IP, RSSI | NetworkManager D-Bus | none | no | research | available | HV-N1 |

## Power

| Capability | Chosen source | Privilege | Needs Decky | Research status | Runtime default | Validation |
|---|---|---|---|---|---|---|
| Suspend | logind D-Bus (active session); Decky `SteamClient.System` preferred in Gaming Mode | polkit allow_active | optional | research | disabled (admin tier) | HV-P1 |
| Reboot / power off | logind D-Bus | polkit allow_active | no | research | disabled (admin tier) | HV-P2 |
| Ack before connection loss | protocol | — | no | design | available | HV-P2 |
| Wake from sleep / off | WoL (dock or built-in Ethernet, hw-dependent; G15 candidate) | — | no | unsupported (default) | unsupported unless proven per device | HV-P4 |

## Steam / game management

| Capability | Chosen source | Privilege | Needs Decky | Research status | Runtime default | Validation |
|---|---|---|---|---|---|---|
| Installed library (Steam games) | `libraryfolders.vdf` + `appmanifest_*.acf` (read-only, allowlisted keys) | none | no | research (high confidence) | available | HV-G1 |
| Distinguish tools / non-Steam shortcuts | `appinfo`-free heuristics + `shortcuts.vdf` (binary VDF, read-only) | none | no | research | available (confidence) | HV-G1 |
| Removable libraries (SD card) | mount events + `libraryfolders.vdf` | none | no | research | available | HV-G1 |
| Current game | `reaper` argv / `registry.vdf` / gamescope atom / Decky | none | optional | research | available (confidence) | HV-G2 |
| Session start/stop tracking | current-game detector + suspend segmentation | none | no | research | available (opt-in history) | HV-G2 |
| Launch game | `steam steam://rungameid/<id>` | user | no | research | available (game tier) | HV-G3 |
| Close game (graceful) — experimental (D3) | Decky `SteamClient.Apps.TerminateApp` | — | yes | experimental | disabled | HV-D2 |
| Force close | signal to `reaper` process tree | user | no | experimental | disabled (separate gate) | HV-G4 |
| Download state & progress | ACF `StateFlags`/`Bytes*` + `content_log.txt`; Decky for queue | none | optional | research | available (partial) | HV-G5 |
| Download queue order / scheduled | Decky `SteamClient.Downloads` | — | yes | experimental | disabled | HV-D2 |
| Pause / resume downloads | Decky `SteamClient.Downloads` | — | yes | experimental | disabled | HV-D2 |
| Update available per game | ACF `StateFlags` (update required) | none | no | research | available | HV-G5 |
| Trigger game update | Decky only | — | yes | experimental | disabled | HV-D2 |
| Install game | `steam://install/<id>` (opens Steam dialog on device) | user | no | research | available as "dialog opened" | HV-G6 |
| Uninstall game | `steam://uninstall/<id>` (Steam dialog) | user | no | research | disabled by default (destructive tier) | HV-G6 |
| Move game between libraries | Decky only (no URI) | — | yes | experimental | disabled | HV-D2 |
| Proton/compat tool inventory | `steamapps/common/Proton*`, `compatibilitytools.d` | none | no | research | available | HV-G7 |
| Per-game compat override (read) | `config.vdf` `CompatToolMapping` subtree only (file contains login tokens — never copied/logged) | none | no | research | available | HV-G7 |
| Set compat tool | Decky `SteamClient.Apps.SpecifyCompatTool` | — | yes | experimental | disabled | HV-D2 |
| Delete Proton prefix / game dir | — | — | — | **unsupported (forbidden by spec)** | unsupported | — |
| Local artwork | `appcache/librarycache/` (both legacy and per-app layouts) | none | no | research | available | HV-G8 |
| External metadata | Steam store (opt-in) | network | no | research | disabled | — |
| Active Steam account (hashed) | `loginusers.vdf` `MostRecent` (numeric ID only) | none | no | research | available | HV-G1 |

## Notifications and UI

| Capability | Chosen source | Privilege | Needs Decky | Research status | Runtime default | Validation |
|---|---|---|---|---|---|---|
| Gaming Mode toast | `@decky/api` `toaster.toast` | — | yes | source | available with Decky | HV-D1 |
| Interactive actions in toast | — (`onClick` only) | — | — | **unsupported** | unsupported | — |
| Interactive actions (fallback) | toast → plugin notification center buttons | — | yes | source | available with Decky | HV-D1 |
| Desktop Mode notification + actions | freedesktop Notifications D-Bus (`actions`) | none | no | research | available in Desktop Mode | HV-S3 |
| "Displayed" state | Decky toast callback / D-Bus `NotificationClosed`/`ActionInvoked` | — | partial | research | only when observable | HV-D1/HV-S3 |
| Gaming Mode without Decky | — | — | — | unsupported | unavailable (queued, shown in HA) | — |
| Local pairing UI | loopback HTTP (Desktop Mode) + CLI | none | no | design | available | HV-S4 |

## Updates

| Capability | Chosen source | Privilege | Needs Decky | Research status | Runtime default | Validation |
|---|---|---|---|---|---|---|
| Detect agent update | signed release index | network | no | design | available | M9 |
| Approval from HA | `UpdateEntity` install | — | no | source | available | M9 |
| A/B slot switch + rollback | user-owned slots + separate updater unit | none | no | design | available | M9 |
| Game-session deferral | current-game detector | none | no | design | available | M9 |
