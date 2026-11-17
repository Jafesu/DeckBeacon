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

## Progress (2026-11-17, G15 in transit)
| AC | Status | Evidence |
|---|---|---|
| AC1 refusals | **pass (software)** | `internal/release` tests: tampered manifest, unknown key, wrong signer, missing or garbage signature, no keys, channel/version mismatch. `internal/update`: newest tampered and incompatible releases are skipped, a tampered binary is never staged (approval cleared, event emitted). |
| AC2 approval | **pass (software)** | Exact-version approval only, `confirmed_by_user` required (agent and schema), persisted across restarts, 7-day expiry. A version that failed is never retried automatically. |
| AC3 safe window | **pass (software)**, G15 pending | Waits with the reason ("Stardew Valley is running", "Steam is downloading …"), proceeds when idle. |
| AC4 switch and rollback | **pass (software)**, G15 pending | Updater tests with the real layout and simulated restarts: adopt + switch + unit rewrite, crash → rollback, no reconnect → rollback, offline device needs no reconnect. The identity directories are untouched by design. |
| AC5 HA | **pass** | Update entity, Install with confirmation, progress/detail, local/remote permission, channel in the welcome policy. 53 tests on HA 2026.5.1 + 2026.10.4. Integration 0.3.0 published to the Marketplace. |
| AC6 signed pipeline | **pass** | With the owner's key (secret + embedded public key), the workflow published `agent/v0.3.0-dev.1` (pre-release). The first run caught a relative-path bug in the build script, which is fixed and now covered by the CI dry run. A dev build querying the real GitHub API offers 0.3.0-dev.1 on the dev channel, verified with the embedded key, and stays "up to date" on stable. The downloaded binary verifies and reports its version. |
| AC7 no privilege | **pass** | `systemd-run --user`, `systemctl --user` only. CI grep. |

## G15 run 1 (2026-11-17): three bugs found, fixed
Installing 0.3.0-dev.1 from HA (integration 0.4.0, G15 on HA Cloud) left the device offline on the broken version. Evidence: the G15 session's read-only diagnosis (journal, layout, updater units).
1. **Strict receivers.** 0.3.0-dev.1 validated received messages against its own strict schemas and rejected the new `welcome.connection_profile` on every connection. COMPATIBILITY.md requires lenient receivers.
   - **Fix (agent and HA):** receivers validate with `additionalProperties: false` removed. The exceptions are objects marked `x-receiver: closed`: command args, library entries, notification actions and the envelope.
   - **Tests:** shared `fixtures/receiver/` in Go and Python; every invalid fixture is also checked against the receiver validator. The agent test (a newer HA's welcome) and the HA test (a newer agent's hello and telemetry) both fail without the fix. A closed library entry with account data is still refused.
2. **Second updater.** The old agent's 30 s step started another updater 7 s after the first.
   - **Fix:** the client stops after handing over and spends the approval at once. The updater also takes an exclusive lock (`update.lock`) and changes nothing while another one runs.
3. **Rollback undone.** The second updater was launched through `bin/deckbeacon`, which already pointed at the new version, so its "previous" was the new version itself. The first updater correctly detected the failure ("did not reconnect within 2m0s") and rolled back to a96fdba. The second then "rolled back" onto the broken build.
   - **Fix:** the updater always runs from `versions/<running version>`. Apply refuses to switch to the version that is already active.

Recovery was manual, with owner approval: `bin/deckbeacon` was repointed at `versions/0.0.0-dev` (a96fdba) and the service restarted. 0.3.0-dev.1 can never talk to integration ≥ 0.4.0; it is superseded by 0.3.0-dev.2.

## Hardware validation procedure (when the G15 is back)
1. **Bootstrap (one time, approval needed):** build the agent from this branch on the G15 (RC session) so it contains the update client. It still runs as a from-source `0.0.0-dev`.
2. **HA:** set the update channel to `dev`. The Agent update entity offers **0.3.0-dev.1**. Press Install. Expect: waits while a game is running, then adopt + switch, the new version reconnects, the entity shows 0.3.0-dev.1 installed, `deckbeacon update status` shows active 0.3.0-dev.1 / previous 0.0.0-dev, pairing intact.
3. **Forced rollback:** run the release workflow with `unhealthy: true` after bumping to `0.3.0-dev.2`. Install it from HA. Expect: switch, the health check fails within 2 minutes, automatic rollback to 0.3.0-dev.1, and an "agent_update_failed (rolled_back)" event with the reason.
4. **Healthy update:** publish `0.3.0-dev.3`, install, expect success. Then publish stable `0.3.0` from `main` when M9 merges, and switch the channel back to `stable`.
