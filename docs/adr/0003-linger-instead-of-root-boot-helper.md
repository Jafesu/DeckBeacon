# ADR-0003: User lingering replaces the root boot helper

Status: Proposed (M0, 2026-11-16)

## Context
Spec 01/03 allow an optional root boot helper for metrics before login. On SteamOS:
- The `deck` user has no password by default. Installing a system unit needs `sudo`, so the user must first set a password, and the installer must handle a root step.
- The root FS is read-only with A/B images. Whether `/etc/systemd/system` additions survive updates needs per-release validation.
- A second process creates handoff, leader election, and duplicate-identity risks (spec 03 calls these out).
- Upstream systemd polkit grants `org.freedesktop.login1.set-self-linger` to any user. With linger, the user's systemd manager, and with it the agent, starts at boot without login. SteamOS also auto-logs in `deck`.

## Decision
- v1 ships a **single agent process**: a `systemd --user` unit, with an **opt-in "start at boot" toggle** that runs `loginctl enable-linger` (and `disable-linger` to undo).
- Under linger with no active session, the agent reports Steam and session capabilities as `unavailable` and power actions as `unavailable` (polkit `allow_any` = `auth_admin_keep`). System telemetry and connectivity stay available.
- The root boot helper is **removed from v1 scope**. It may come back as its own ADR if a validated need appears that linger can't meet.
- Roadmap M3 becomes: linger opt-in plus boot, login, logout, suspend, and SteamOS-update lifecycle tests.

## Consequences
- No root and no sudo password during install. Identity, event stream, and collectors all stay in one process.
- If SteamOS overrides the self-linger policy (HV-S1 fails), "start at boot" is unavailable without sudo. In that case the installer offers `sudo loginctl enable-linger deck` as an explicit, documented step, which is still not a root daemon.

## Validation
HV-S1 and HV-S2. HV-P1 for power under linger only.
