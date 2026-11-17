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
| Agent start at boot without login | `systemd --user` + linger via `deckbeacon service install --at-boot` (M3) | none (polkit self-linger) | no | **verified on Dell G15** (online before Steam starts) | disabled until opted in | [HV-S1](evidence/G15-sleep-test-2026-11-17.md) |
| Agent in Gaming & Desktop modes | `systemd --user` | none | no | research | available | HV-S2 |
| Root boot helper | system unit | root + sudo password | no | research (deferred, ADR-0003) | unsupported (v1) | — |
| Sleep/shutdown pre-announce | logind delay inhibitor + `PrepareForSleep`/`PrepareForShutdown` | none | no | **verified on Dell G15**: sleep (Desktop + Gaming Mode), reboot (`rebooting`) and power-off (`off`) ([HV-P3](evidence/G15-sleep-test-2026-11-17.md)); Steam Deck pending | available when logind reachable | HV-P3 |
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

## Evidence log: Dell G15 5520, SteamOS 3.9.5 (`rel`), read-only capture 2026-11-17
Source: [`evidence/G15-capture-2026-11-17.md`](evidence/G15-capture-2026-11-17.md). The paths and values were **observed on the device**. The agent's parsing of them is verified against the recorded fixture `apps/agent/testdata/roots/dell-g15-5520`. The agent binary itself has **not yet run** on the G15.

| Capability | Observation | Effect |
|---|---|---|
| Model identification | DMI `Dell Inc.` / `Dell G15 5520`. os-release says `VARIANT_ID=steamdeck` and the kernel is `neptune` even here. | Confirms DMI-based identity. os-release must never identify hardware. |
| CPU temperature | `coretemp` "Package id 0" (plus per-core). `alienware_wmi`/`dell_ddv`/`dell_smm` duplicate the CPU/fan data. | Collector picks coretemp. hwmon indices are dynamic, so matching is by name. |
| GPU utilisation/temp (NVIDIA) | Hybrid i915 + RTX 3050 Mobile, proprietary driver. No `gpu_busy_percent`. `nvidia-smi` → `21, 49.62` (**decimal temperature**). | **Bug found and fixed:** the integer parser made both metrics unavailable. A fallback to the hwmon sensor labelled `GPU` was added. |
| Fans | 2 fans via 3 drivers, all 0 RPM at light load. | Reported as 0. Whether fans are stopped or misreport is unknown; re-check under load. |
| Battery | `BAT0`, `CHARGE_*` (µAh) with no `ENERGY_*`, duplicate `TYPE` key, `HEALTH=Good` despite 41 % capacity. | Agent reads capacity/status/current×voltage only. Firmware health is not reported. |
| Session mode | Desktop Mode: `kwin_wayland` + `plasmashell`, no gamescope. | Heuristic matches. Gaming Mode still to observe. |
| Start at boot (linger) | Vendor polkit `set-self-linger` = yes/yes/yes. `/etc/polkit-1/rules.d` unreadable without root. Linger currently off. | HV-S1 policy precondition met. An actual enable test is still pending. |
| Sleep delay inhibitor | `inhibit-delay-sleep` = yes/yes/yes. `InhibitDelayMaxSec` default 5 s. Other user delay inhibitors (kwin, cecd) present. | Agent's 4 s cap is within limits. HV-P3 timing still pending. |
| Power commands | `suspend`: allow_active=yes, inactive/any=auth_admin_keep. | Matches the M0 analysis (works from an active session only). |
| logind / user systemd | logind reachable on the system bus. `systemctl --user` running, user units already in use. | Service model viable. |
| mDNS | `avahi-daemon` active. | Agent's legacy-unicast browse coexists. Untested live. |
| Steam library | 1 library, 37 manifests (tools/runtimes included). `BytesToDownload`/`BytesDownloaded` optional. Proton 9 Beta / 11 / Experimental. librarycache per-app directories. | Inputs for M6. |
| Filesystem | Single btrfs subvolume (not the Steam Deck A/B layout). | HV-S2 (update survival) must be run separately for Deck and G15. |

### Agent run on the G15 (2026-11-17, [`evidence/G15-agent-probe-2026-11-17.md`](evidence/G15-agent-probe-2026-11-17.md))
The real `deckbeacon` binary (commit 673ce5d, built on the device) ran `probe` in Desktop Mode, idle and with a game (Castle Crashers on the RTX 3050). **Every system telemetry metric was available with live values.** Rows verified on this unit:

| Metric | Source on G15 | Observed |
|---|---|---|
| cpu.usage | /proc/stat delta | 7–22 % |
| memory, storage | /proc/meminfo, statfs /home | 30.8 %, 56.2 % |
| battery.* , power.ac_online | power_supply BAT0 / AC | 100 %, full, 0 W on AC |
| thermal.cpu_c | hwmon coretemp "Package id 0" | 53–56 °C |
| gpu.busy, thermal.gpu_c | nvidia-smi (decimal temps parse) | 5–10 % idle → 14–31 % in game; 53.6–54.4 °C |
| fan.rpm | hwmon alienware_wmi (max of 2 fans; 3 drivers agree) | 2134–2313 rpm (0 rpm only while stopped after boot) |
| session.mode | process scan | desktop (correct, also while a game runs) |
| lifecycle.* | logind on the system bus | sleep/shutdown notify + resume detect available, no polkit prompt |

Still pending on this unit: Gaming Mode detection, battery discharge power, pairing/session with a real HA, lingering enable (HV-S1), suspend timing (HV-P3).
