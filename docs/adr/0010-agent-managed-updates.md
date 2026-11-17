# ADR-0010: Agent managed updates (signing, approval, atomic switch, rollback)

Status: Proposed (M9, 2026-11-17)

## Context
Spec 12 requires the following:
- Agent updates are detected automatically but installed only after explicit approval in HA (or Decky). Never unattended by default.
- Releases are signed and checksummed. Per-device stable/beta/dev channels.
- Stage → verify signature, compatibility and disk → wait for an idle/safe window → switch atomically between active/previous slots with an **independent updater** → health-check → roll back on failure.
- An approved update stays pending while the device is asleep or offline. The device is never woken to update. History and errors are visible.

Spec 13: "reject unsigned artifacts … recovery after failed update", "preserve identity across updates". The threat model (R11, compromised release artifact) asks for signed releases, a public key embedded in the agent, version monotonicity and a compatibility check before the switch.

The integration and cards already update through the HA Marketplace (ADR-0005). This ADR covers the **agent** only. Constraints:
- No root (ADR-0003).
- The agent runs as a `systemd --user` unit.
- Cloud sessions cannot push git tags.

## Decision

### D1 Release artifacts and signing
- **Where:** each agent release is a GitHub release in the monorepo, tag `agent/vX.Y.Z` (ADR-0005). It is created by a release workflow when `apps/agent/VERSION` changes on `main`, so no tag pushes are needed.
- **Assets:**
  - `deckbeacon-linux-amd64`, `deckbeacon-linux-arm64` (CGO off, `-trimpath`, version stamped in);
  - `release.json`, the **release manifest**: version, channel, published time, per-asset sha256 and size, `min_protocol`, `min_integration`, notes URL;
  - `release.json.sig`.
- **Signature:** Ed25519 over the exact bytes of `release.json`. The asset hashes in the manifest bind the binaries to that signature. Ed25519 comes from Go's standard library: no new dependency and no network needed at verify time.
- **Keys:** the **public key(s) are embedded in the agent**, as a list, so a key can be rotated by shipping a release that trusts old and new keys. The **private key never exists in this repo or any Claude session**. The owner generates it locally (`deckbeacon release keygen`), stores it as the Actions secret `DECKBEACON_RELEASE_KEY` and gives back only the public key.
- Rejected:
  - **Sigstore keyless:** large dependency, online TUF root, heavy identity policy for an offline device check.
  - **Checksums alone:** they don't protect against a compromised release page.

### D2 Discovery: the agent checks, HA shows it
- **Check:** the agent reads the GitHub releases list (public HTTPS) every 6 h and at start, for its channel:
  - `stable` = no pre-release suffix;
  - `beta` adds `-beta.N`;
  - `dev` adds `-dev.N`.

  It fetches and verifies `release.json` for the newest compatible version greater than its own.
- **Report:** metrics `agent.update.latest`, `agent.update.notes_url` and `agent.update.state` (`up_to_date` / `available` / `approved` / `waiting` / `downloading` / `installing` / `failed` / `rolled_back`), plus events (D5).
- **HA:** a native `update` entity, "DeckBeacon agent", showing installed vs latest, release notes and progress, with **Install** (an HA `UpdateEntity` with the install feature).
- **Channel:** set in the DeckBeacon options (default `stable`) and carried in the `welcome` policy. Protocol 1.5, additive.
- No network → state `check_failed` with a reason. Nothing is guessed.

### D3 Approval
- **Install** sends `approve_agent_update {version}` (already in the protocol; TTL up to 24 h) with `confirmed_by_user`.
- **Tier:** a separate `updates` permission tier (spec 10), enforced on the agent and not just in HA. Default: local yes, remote **yes**. Releases are signature-verified, and approving one from away from home is the point. The owner can turn remote off in the options. *(Owner decision requested.)*
- **Persistence:** the approval is stored durably on the agent until applied, superseded by a newer approval, or expired after 7 days. Approving while the device sleeps (HA queues nothing, see below) means: HA sends the approval when the device next connects, as long as the user approved within the TTL. The device is never woken.
- **Never automatic:** there is no unattended update, and no option for one in M9.

### D4 Apply: stage, safe window, independent updater, health check, rollback
- **Layout** under `~/.local/share/deckbeacon/`:
  - `versions/<X.Y.Z>/deckbeacon`;
  - symlinks `bin/deckbeacon` → active, `previous` → last good.
  - The unit's `ExecStart` stays `%h/.local/share/deckbeacon/bin/deckbeacon`. The first update adopts an existing plain binary as `versions/<current>/`.
- **Stage:**
  1. download to `versions/.staging`;
  2. check free disk (≥ 3× size);
  3. check size and sha256 against the **verified** manifest;
  4. check version monotonicity (no downgrades except rollback);
  5. check compatibility with the connected HA (`min_integration`, `min_protocol`);
  6. run `<new> version --json` to confirm it executes and reports the expected version.
- **Safe window:** wait while a game is running or Steam is downloading, and never during a suspend or shutdown in progress. State `waiting` with the reason.
- **Independent updater:** the running agent starts `deckbeacon update apply --to X.Y.Z` with the **current, known-good binary** as a transient user unit (`systemd-run --user --unit deckbeacon-updater …`). It survives the agent's restart. It:
  1. flips the symlink atomically (`rename`);
  2. restarts `deckbeacon.service`;
  3. waits up to 120 s for health;
  4. on failure, flips back and restarts again.
- **Health:** the new process stays active, writes `status.json` reporting the new version, and, if the old one was connected to HA before the update, connects again. A device that was offline needs only the first two.
- **Recovery:** the previous slot is kept, so a manual `deckbeacon update rollback` is always possible. Only the active and previous slots are kept.
- **Identity:** the identity, trust, store and config live in `~/.local/state` and `~/.config`, untouched by updates (spec 13: preserve identity).

### D5 Visibility
- **Events:** `agent_update_available`, `agent_update_started`, `agent_update_completed` and `agent_update_failed` (with `rolled_back`). Protocol 1.5, additive.
- **HA:** these appear in the logbook. The update entity shows progress and the last error. History is in the event log and the `update` entity's attributes.

## Consequences
- **Owner actions (one time):**
  1. generate the signing key and add the secret;
  2. allow the release workflow to create releases (default `GITHUB_TOKEN` with `contents: write`).
- The agent makes outbound HTTPS requests to `api.github.com` and the release download host. This is the only internet access besides HA Cloud, documented, and skipped entirely if updates are disabled (`deckbeacon update off`).
- Until the first signed release exists, the G15 keeps the from-source build. The adopt step migrates it on its first managed update.

## Validation
- **Unit tests:**
  - manifest verification: good, tampered manifest, tampered binary, wrong key, downgrade, incompatible;
  - updater state machine with a fake systemd runner: apply success, crash, timeout leading to rollback, adopt;
  - safe-window gating;
  - approval persistence and expiry.
- **HA tests:** the update entity and approval flow.
- **G15 (when back):** one real update between two signed releases, plus one forced rollback (a deliberately unhealthy release candidate on the `dev` channel).
