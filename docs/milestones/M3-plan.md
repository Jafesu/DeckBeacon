# Milestone 3 — Boot-Time Option and Lifecycle: Plan, Acceptance Criteria, Results

Status: **In progress** (started 2026-11-16; owner instruction: "Continue with m3")

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
