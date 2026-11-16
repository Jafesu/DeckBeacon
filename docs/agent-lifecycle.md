# Agent lifecycle: service, boot, sleep, shutdown

Applies from M3. Design: ADR-0003 (lingering instead of a root boot helper).

## Installing the service (no sudo)
```bash
deckbeacon service install            # writes ~/.config/systemd/user/deckbeacon.service, enables and starts it
deckbeacon service install --at-boot  # also start at boot, before anyone logs in (user lingering)
deckbeacon service status [--json]
deckbeacon service boot on|off        # toggle start-at-boot later
deckbeacon service uninstall          # stop, disable, remove the unit (pairing and state are kept)
```
- The unit runs the binary you invoked (`ExecStart` = its absolute path).
- **Start at boot** uses `loginctl enable-linger` for your own user. Upstream systemd allows this without a password. Whether SteamOS restricts it is unverified (HV-S1). If it is refused, the error says so, and `sudo loginctl enable-linger deck` is the documented manual alternative. DeckBeacon never runs sudo itself.
- `boot off` / `uninstall` only disable lingering if DeckBeacon enabled it (other user services may rely on it). Use `boot off --force` to override.

## Sleep, resume, shutdown
| Transition | With logind (normal SteamOS) | Without logind |
|---|---|---|
| Sleep | Agent holds a logind **delay** inhibitor. On `PrepareForSleep(true)` it sends `device_suspending` and releases the inhibitor once HA acknowledges (≤ 3 s, hard cap 4 s, below logind's 5 s default). | Not announced. |
| Resume | `PrepareForSleep(false)` → `device_resumed` (confirmed). Immediate reconnect, no backoff. | Wall-clock vs monotonic gap ≥ 30 s → `device_resumed` (heuristic). |
| Shutdown | `PrepareForShutdown(true)` → `device_shutting_down`, same ack-bounded release. | Not announced. |
| Boot | A new kernel `boot_id` → one `device_booted` event. | same |

If HA is unreachable at that moment, the transition is queued (delivered later as backfill, `live: false`) and the system is **never delayed**.

## What Home Assistant shows
- **Connection** (`local` / `remote` / `offline`) and **Power state** (`on` / `suspended` / `shutting_down` / `unknown`) are separate sensors (spec 01).
- An announced suspend/shutdown is kept after the connection drops. An unannounced drop → `unknown`, never "off".
- Backfilled lifecycle events appear on the event bus (`deckbeacon_event`, `live: false`) but do not change the current power state.

## Capabilities reported
`lifecycle.sleep_notify`, `lifecycle.shutdown_notify`, `lifecycle.resume_detect` (source: logind or heuristic), `service.start_at_boot` (`disabled` until you opt in).

## Not yet verified on hardware
Gaming Mode suspend is driven by Steam. Whether Steam's suspend path emits logind `PrepareForSleep` with enough time for the agent is exactly what HV-P3 measures. Lingering under SteamOS polkit policy: HV-S1. Behaviour across an A/B SteamOS update: HV-S2.
