# Hardware and Environment Validation Plan

These tests turn `research` rows in the [capability matrix](capability-matrix.md) into evidence. They need a real SteamOS device and, for HV-R1, a Home Assistant instance with HA Cloud. **None have been run yet.**

Reference units: **Dell G15 (SteamOS)**, the project's primary test machine, then Steam Deck LCD/OLED when available.

Record for every run: date, device model (DMI `board_name`), SteamOS version and channel (`/etc/os-release`, `steamos-update --check` output), Steam client build, Decky version (if any), HA version, and full command output. Store results in `docs/research/evidence/<test-id>-<date>.md`.

All tests are **read-only or reversible**. None writes Steam files.

## Remote transport (blocking gate)

### HV-R1 — HA Cloud carries the device WebSocket
Preconditions: HA with HA Cloud remote UI enabled (the project owner's instance qualifies: HA 2026.11.1). **Needs explicit owner approval before touching that instance.**
1. Copy `research/spikes/ha-device-transport/custom_components/deckbeacon_spike` into `/config/custom_components/`.
2. Add a `deckbeacon_spike: devices: {deck-test-1: <pubkey>}` block to `configuration.yaml`. Generate the key as in `run_spike.sh`. Restart HA.
3. From a network **outside** the LAN (e.g. phone hotspot), run `client -url wss://<remote-ui-host>/api/deckbeacon_spike/ws -key good.seed`.
4. Expected: `auth_ok`, command received, duplicate detected. Then repeat with the bad key and expect rejection.
5. Soak test: modify the client to send telemetry every 15 s for 2 h. Record disconnects, latency (p50/p95), and close codes.
6. Toggle remote UI off, then on. Record client behaviour and reconnect time.
7. **Remove** the spike integration and config, then restart HA.

Pass: steps 4–6 behave as expected, with no forced idle disconnect under 15 s heartbeats. Fail → evaluate an external URL/VPN or HTTPS long-poll (spec 09 fallback).

### HV-R2 — External HTTPS URL / reverse proxy
Same as HV-R1 through a user-managed reverse proxy (nginx/Caddy/Cloudflare tunnel). Confirm WebSocket upgrade and idle timeouts.

## Service lifecycle (SteamOS)

| ID | Test | Expected |
|---|---|---|
| HV-S1 | `deckbeacon service install --at-boot` (runs `loginctl enable-linger` as `deck`, no sudo); record the exact error if refused. Reboot. Check `systemctl --user status` of a dummy unit before the Steam UI appears. Check `/usr/share/polkit-1/actions/org.freedesktop.login1.policy` and `/etc/polkit-1/rules.d` for SteamOS overrides. | Linger is allowed and the unit is running at boot. |
| HV-S2 | Dummy user unit survives Gaming↔Desktop switches, suspend/resume, and a SteamOS update (A/B switch). Detect the session mode. | Unit persists. Mode detected correctly. |
| HV-S3 | `gdbus call … org.freedesktop.Notifications.Notify` with actions in Desktop Mode, and in Gaming Mode. | Desktop: actions work. Gaming: record behaviour. |
| HV-S4 | Loopback HTTP server reachable from the Desktop Mode browser. Confirm there is no Gaming Mode path without Decky. | As stated. |

## Power

| ID | Test | Expected |
|---|---|---|
| HV-P1 | From a lingering user unit (with the deck session active, and again after logout), call logind `Suspend(false)` via D-Bus. Observe Steam's UI on resume. | Allowed when the session is active. Record any Steam UI desync. |
| HV-P2 | Same for `Reboot`/`PowerOff` (do last). Confirm an ack can be sent before the network drops. | Allowed when active. Ack delivered. |
| HV-P4 | (Non-Valve with Ethernet, e.g. G15) `ethtool <if> | grep Wake-on`. Suspend, then send a magic packet from the LAN. Record the result. |
| HV-P3 | With the agent running (`--log-level debug`), suspend from **Gaming Mode (Steam power menu)** and from Desktop Mode. In HA, check `device_suspending` arrives before sleep and `power_state=suspended`. `busctl --system call org.freedesktop.login1 /org/freedesktop/login1 org.freedesktop.login1.Manager ListInhibitors` shows the DeckBeacon delay lock. Repeat for shutdown. Record time from announcement to ack. | Lock granted. Announcement acked before sleep in both modes. |

## Telemetry

| ID | Test |
|---|---|
| HV-T1 | Dump `/proc/stat`, `/proc/meminfo`, `/proc/uptime`, and `df` for `/home` and the SD card. |
| HV-T2 | `ls -l /sys/class/hwmon/*/name` with all `temp*_input`, `fan*_input`, and `gpu_busy_percent`, idle and in-game. Record names per model. |
| HV-T3 | `/sys/class/power_supply/*/uevent` on battery, charging, and docked. |
| HV-T4 | `/sys/class/dmi/id/{board_name,product_name,sys_vendor}` and `/etc/os-release`. |
| HV-T5 | DRM connector status (`/sys/class/drm/*/status`) handheld vs docked. Record USB-C/PD signals. |
| HV-T6 | `wpctl get-volume @DEFAULT_AUDIO_SINK@` (set and restore). Backlight read. |
| HV-T7 | **Non-Valve (Dell G15):** `lspci -k` for GPU and driver. `nvidia-smi --query-gpu=utilization.gpu,temperature.gpu --format=csv` if present. hwmon names (`coretemp`/`k10temp`/`dell_smm`). `power_supply` names. Confirm `ID=steamos` and the SteamOS build/channel. |
| HV-N1 | `nmcli -t` device/wifi info. mDNS browse `_home-assistant._tcp` from the device (is avahi running?). |

## Steam (read-only)

| ID | Test |
|---|---|
| HV-G1 | Locate `libraryfolders.vdf` and the ACF files (internal and SD). Insert and remove the SD card while watching changes. Read `loginusers.vdf` **structure only** (redact names). |
| HV-G2 | While a native game, a Proton game, and a non-Steam shortcut run, capture `ps -eo pid,args` (filter `reaper`), `registry.vdf` `RunningAppID`, and the gamescope atom (`xprop -root` against gamescope's Xwayland where accessible). Repeat across suspend. |
| HV-G3 | As `deck` via SSH and from a user unit: `steam steam://rungameid/<free game id>` in Gaming Mode and in Desktop Mode. Record time to "running" and any prompt. |
| HV-G4 | Force-close semantics (SIGTERM to `reaper` child) on a throwaway free game. Observe Steam UI and cloud-sync state. *Optional; destructive-adjacent.* |
| HV-G5 | Start a download or update of a small free game. Sample its ACF and `logs/content_log.txt` every 2 s through download, pause (via Steam UI), resume, verify, and complete. Map `StateFlags` values. |
| HV-G6 | `steam://install/<id>` and `steam://uninstall/<id>`. Confirm Steam shows its own dialog and does nothing without user confirmation. |
| HV-G7 | List `compatibilitytools.d` and Proton dirs. Extract only `CompatToolMapping` from `config.vdf` with a parser that discards all other keys. Confirm there are no other keys in memory dumps or logs. |
| HV-G8 | Inspect the `appcache/librarycache` layout. Record file names and formats. |

## Decky

| ID | Test |
|---|---|
| HV-D1 | Minimal plugin: `toaster.toast` with `onClick` → navigate to a plugin route with buttons. Record behaviour while a game is running, in quiet hours, and with the QAM closed. |
| HV-D2 | In a dev plugin: feature-detect `SteamClient.Apps`, `SteamClient.Downloads`, and `SteamClient.System` methods. Record signatures per Steam build. **Do not call mutating methods** except on a throwaway free game, with explicit approval. |
| HV-D3 | Brightness via `SteamClient`. Read only first. |
| HV-D4 | Backend runs as `deck` (no `root` flag). Connect to a 0600 Unix socket owned by `deck`. |
