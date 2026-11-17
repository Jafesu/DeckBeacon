# Milestone 9 — Managed agent updates: Plan

Status: **In progress** (started 2026-11-17; owner: "let's start on m9"; the G15 is in transit, so hardware validation comes last). Design: ADR-0010.

## Work packages
| WP | Deliverable |
|---|---|
| WP1 | **Release manifest + signing**: `release.json` schema, Ed25519 sign/verify, embedded trusted keys (rotation list), `deckbeacon release keygen/sign/verify` subcommands (keygen runs on the owner's machine). |
| WP2 | **Release workflow** (monorepo): on `apps/agent/VERSION` change on `main`, build amd64/arm64, write the manifest, sign with the `DECKBEACON_RELEASE_KEY` secret, publish `agent/vX.Y.Z` (pre-release for beta/dev). Refuses to publish unsigned. |
| WP3 | **Agent update client**: channel-aware discovery (GitHub releases), verification, compatibility and monotonicity checks, metrics and events, `deckbeacon update status/check/off/on/rollback`. |
| WP4 | **Apply**: staging, disk check, executes-check, safe-window gating (game running, download, suspend), durable approval with expiry, versioned layout + adopt, independent updater via `systemd-run --user`, health check, automatic rollback. |
| WP5 | **Protocol 1.5** (additive): `updates` tier, `welcome.policy.update_channel`, update events, `approve_agent_update {version}` args. Fixtures in both languages. |
| WP6 | **HA**: `update` entity (installed/latest, notes, progress, install), options (channel; updates tier local/remote), logbook events. |
| WP7 | Tests, docs (update + rollback guide), threat model update, results. **G15 validation** when it is back: real update + forced rollback. |

## Acceptance criteria
- AC1 A tampered manifest, a binary whose hash doesn't match, an unknown key, a downgrade, or an incompatible release (protocol/integration) is refused before anything is switched (tests).
- AC2 No update is installed without an approval for that exact version. The approval survives agent restarts and expires after 7 days (tests).
- AC3 With a game running or a download active, an approved update waits and reports why. It proceeds once the device is idle (tests; G15).
- AC4 A healthy update switches atomically and keeps identity and pairing. An unhealthy one (crash or no reconnect within 120 s) is rolled back automatically, and HA shows the failure (fake-runner tests; G15 forced rollback).
- AC5 HA's update entity shows installed and latest version and release notes, installs on demand, and shows progress and errors. Channel and remote permission are configurable (HA tests).
- AC6 The release workflow publishes signed artifacts only, and the agent verifies them with the embedded key (CI dry run + first real release).
- AC7 No privilege escalation: no sudo/root, user units only (CI grep, existing).
- AC8 CI green.
