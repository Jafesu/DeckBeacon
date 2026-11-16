# Milestone 3 — Boot-Time Option and Lifecycle: Plan, Acceptance Criteria, Results

Status: **Complete** (2026-11-16). Software verified. Hardware validation pending (see caveat).

## Scope (roadmap item 3, as reshaped by ADR-0003)
ADR-0003 replaced the root boot helper with **opt-in user lingering**. M3 therefore delivers:
- service management for the single `systemd --user` agent, with an explicit "start at boot" opt-in;
- lifecycle awareness: boot, suspend, resume, shutdown;
- a power state in HA that is **distinct from connection state** (spec 01 invariant).

No root component, no sudo, no second process.

## Work packages
| WP | Deliverable |
|---|---|
| WP1 | `deckbeacon service install / uninstall / status [--at-boot]`. Writes the user unit (ExecStart = the running binary's absolute path), `systemctl --user daemon-reload / enable --now / disable --now`, and opt-in lingering via `loginctl enable-linger` / `disable-linger` (own user, no sudo). Linger state read from `/var/lib/systemd/linger/<user>`. Every external command goes through a mockable runner. |
| WP2 | **logind monitor** (`org.freedesktop.login1` via D-Bus): `PrepareForSleep` / `PrepareForShutdown` signals plus a `delay` inhibitor for `sleep:shutdown`. Degrades to "unavailable" when logind or the system bus is missing. |
| WP3 | **Clock-jump detector** (fallback without logind): wall-clock vs monotonic divergence reveals a suspend gap (Linux `CLOCK_MONOTONIC` stops during suspend). Reports `device_resumed` with `confidence: heuristic`. |
| WP4 | **Session integration**: on sleep/shutdown announce → queue `device_suspending` / `device_shutting_down`, flush and await the ack (bounded, ≤ 3 s), then release the inhibitor. On resume → `device_resumed`, drop the stale connection, reconnect immediately (no backoff), re-take the inhibitor. |
| WP5 | **Boot detection**: persisted `boot_id`. On a new boot, queue `device_booted` {boot_time, previous_boot_id}. Protocol **1.1 (minor)**: new event type `device_booted` (COMPATIBILITY rules: additive). |
| WP6 | **HA**: `power_state` sensor (`on` / `suspended` / `shutting_down` / `unknown`). An announced suspend or shutdown is kept when the connection drops. An *unannounced* drop → `unknown` (never assumed off). Hello/boot/resume → `on`. Backfilled lifecycle events update history but never override a newer live state. |
| WP7 | Capabilities: `lifecycle.sleep_notify`, `lifecycle.shutdown_notify` (logind), `lifecycle.resume_detect` (logind or heuristic), `service.start_at_boot` (linger). Each reports its real status. |
| WP8 | Tests, CI, docs (lifecycle doc, HV-S1/S2/P3 procedures refined), results. |

## Acceptance criteria
- AC1 `service install --at-boot` produces exactly the expected unit file and command sequence. `uninstall` reverses it. Idempotent: a second run changes nothing. Tested with a fake runner. The unit template matches `packaging/systemd/deckbeacon.service`.
- AC2 The logind client, tested against a **fake logind service on a private D-Bus daemon**, takes a delay inhibitor, receives `PrepareForSleep(true/false)` and `PrepareForShutdown(true)`, and releases the inhibitor fd after the agent's flush.
- AC3 On a sleep announcement the agent delivers `device_suspending` to HA **before** releasing the inhibitor (verified with the Go fake HA). On resume it reconnects without backoff and delivers `device_resumed`.
- AC4 The clock-jump detector reports a resume for a ≥ 30 s divergence and stays silent for normal drift and NTP slews < 30 s.
- AC5 A new `boot_id` → exactly one `device_booted`. The same boot_id across agent restarts → none.
- AC6 HA `power_state` follows announced transitions. An unannounced disconnect yields `unknown`. Connection and power stay independent.
- AC7 No privilege escalation: no code path invokes sudo, pkexec, or system units (CI grep).
- AC8 All suites green in CI (HA minimum + latest, E2E).

## Hardware caveat
Lingering, real logind behaviour, Steam's own suspend handling in Gaming Mode, and inhibitor timing are **unverified** until HV-S1, HV-S2, HV-P1 and HV-P3 run on the Dell G15 and a Steam Deck. The logind fake follows the documented `org.freedesktop.login1` interface. It is not a capture of real SteamOS behaviour.

## Results (2026-11-16)
Environment: Linux x86_64 container; Go 1.26.11; godbus/dbus v5.2.4; dbus-daemon (private session bus for the logind fake); Python 3.14.10; HA 2026.5.1 (minimum) and 2026.10.4 (latest) test harness.

| AC | Result | Evidence |
|---|---|---|
| AC1 service install/uninstall | **pass** | `internal/service`: exact command sequence (`daemon-reload` → `enable --now` → `loginctl enable-linger`). A second install is a no-op. Uninstall reverses it. Foreign lingering is never disabled without `--force`. A refused lingering is reported with guidance while the unit stays enabled. Template equals `packaging/systemd/deckbeacon.service` (ExecStart aside). `%` escaping and unsafe-path rejection. |
| AC2 logind client | **pass** | `TestLogindSleepResumeShutdown` against a fake `org.freedesktop.login1` on a private `dbus-daemon`: delay inhibitor `sleep:shutdown` taken. The inhibitor fd stays open until `Ready`, closes after it (observed as EOF on the fake's socket end). Re-taken after resume. Released by the 4 s cap for a slow consumer. `TestLogindUnavailable`: a bus without logind → error and graceful fallback. CI requires dbus-daemon so this never skips. |
| AC3 announce before release; resume reconnect | **pass** | `TestSleepAnnouncedBeforeInhibitorRelease`: at release time HA had received **and acked** `device_suspending`. `TestResumeReconnectsWithoutBackoff`: with a 1 h backoff configured, the agent reconnects immediately and delivers `device_resumed`. `TestSleepWhileOfflineDoesNotDelaySystem`: offline shutdown released at once and queued. |
| AC4 clock-jump heuristic | **pass** | No report for 2 s / 29 s steps. Report for 90 s (`confidence: heuristic`, gap carried). Zero gap for ordinary elapsed time. |
| AC5 boot detection | **pass** | `TestDetectBootOncePerBootID` (a, a, a, b → 2 events, `previous_boot_id` set). E2E: exactly one `device_booted` across a reload and a forced reconnect. |
| AC6 HA power state | **pass** | `test_power.py`: unknown → on (hello) → suspended (announced, survives disconnect while connection = offline) → on (resume; a backfilled suspend does not override) → unknown (unannounced drop) → on (boot). E2E: `power_state=on`. |
| AC7 no privilege escalation | **pass** | CI grep over agent sources (`"sudo"`, `"pkexec"`, `"doas"`, `"su"`, `"--system"`). The service tests assert no privileged calls. |
| AC8 CI | **pass** | [Run #14](https://github.com/Jafesu/DeckBeacon/actions/runs/43969248324): all jobs green: Go (`-race`, logind tests **required** via `DECKBEACON_REQUIRE_DBUS`, govulncheck), protocol-python, fixtures (+schema sync, token grep, privilege grep), HA integration + E2E on 2026.5.1 and latest, secret scan. |

Lenient receivers (protocol 1.1 compatibility, added because `device_booted` is a new event type): an unknown command from a newer HA → `rejected/unsupported` with the session kept (`TestUnknownCommandFromNewerHAIsRejectedNotFatal`). An unknown event type from a newer agent → acked and skipped, session kept (`test_unknown_event_type_is_acked_and_skipped`).

Test counts (local): Go: all 16 agent packages + protocol pass with `-race`. Python protocol: 56. Fixtures: 25 valid / 30 invalid. HA integration + E2E: **29 passed on HA 2026.5.1 and on HA 2026.10.4**.

### Findings during M3
- `StartLimitIntervalSec`/`StartLimitBurst` were in `[Service]` in the M2 unit file. systemd only honours them in `[Unit]`. Fixed.
- Test-harness lesson: `os.File` deadlines need a non-blocking fd. A blocking socketpair made the "not released yet" check silently wait for the 4 s safety release.
- Race detector caught an unsynchronised `MaxDelay` write. Now atomic.

### Known limitations (hardware-dependent, carried forward)
- **Gaming Mode suspend is Steam-driven.** Whether logind emits `PrepareForSleep` early enough for the 3 s ack window on SteamOS is unknown: **HV-P3**.
- Self-lingering under SteamOS polkit policy: **HV-S1**. Unit survival across an A/B update: **HV-S2**.
- Power commands (suspend/reboot from HA) are **not** part of M3. They belong to the power/admin tier (M7) and HV-P1/P2.
- Power state is held in memory: after an HA restart it is `unknown` until the device reconnects.
