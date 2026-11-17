# DeckBeacon Threat Model v1

Status: M1 baseline (2026-11-16). Re-reviewed at every milestone that adds an interface. Builds on ADR-0001…0006 and M0 risks R1–R12.

## 1. Assets
| ID | Asset | Why it matters |
|---|---|---|
| A1 | Device static keys (X25519 channel, Ed25519 signing) | Impersonating the device to HA, or decrypting future sessions |
| A2 | Integration static key (per config entry) | Impersonating HA to the device, issuing commands |
| A3 | Pairing trust (pinned peer keys, connection profile) | Root of all later authentication |
| A4 | Device control surface (launch, power, game management, updates) | Physical-world effects, data loss (uninstall), game-session disruption |
| A5 | Steam credentials on disk (`config.vdf` ConnectCache, `ssfn*`, `localconfig.vdf`, `loginusers.vdf`) | Steam account takeover. DeckBeacon must never become an exfiltration path. |
| A6 | Privacy data: play history, library, account ID, location-ish network data, notifications | Profiling. Smart-home notification content can be sensitive. |
| A7 | HA itself (via the DeckBeacon endpoint) | Pivot into the user's home automation |
| A8 | Release artifacts and the signing key | Fleet-wide compromise |

## 2. Trust boundaries
```
 [Internet] ──(HA Cloud / reverse proxy)──┐
 [LAN peers] ─────────────────────────────┤
                                          ▼
                         ┌─ HA HTTP server ──────────────────────────┐
                         │  /api/deckbeacon/v1/pair  (window-gated)  │  B1: pre-auth surface
                         │  /api/deckbeacon/v1/ws    (Noise IK)      │
                         │  DeckBeacon integration ── HA core/users  │  B2: integration ↔ HA authz
                         └───────────────────────────────────────────┘
                                          ▲ outbound only
 SteamOS device ──────────────────────────┘
   deck user: agent (systemd --user) ── Steam files (read-only allowlist)    B3: agent ↔ Steam data
        │  Unix socket 0600 + token
        ▼
   Decky Loader (root) ─ DeckBeacon plugin (deck) ─ other plugins (shared JS)   B4: Decky ↔ agent
   loopback UI (Desktop Mode browser)                                           B5: browser ↔ agent
   logind / polkit (power)                                                      B6: agent ↔ OS
```

## 3. Threats (STRIDE per spec-10 actor) and mitigations
| # | Actor / threat | Boundary | Mitigations | Verified by |
|---|---|---|---|---|
| T1 | **Malicious LAN peer** sniffs or tampers with traffic to `http://` HA | B1 | Noise IK end to end with pinned static keys. TLS is not relied on. | M2: wrong-key, tamper, and replay tests (Go+Py) |
| T2 | **Spoofed HA discovery** (fake `_home-assistant._tcp`) | B1 | Discovery only proposes a URL. Pairing requires SAS confirmation on both sides. After pairing, a server key mismatch → hard fail plus a "re-pair required" state, never silent re-trust. | M2 test: wrong server key |
| T3 | **Pairing spam / rogue enrolment** from LAN or internet | B1 | `/pair` returns 404 unless a ≤10-minute window was opened in the HA UI. One pending request per window. SAS confirmed by a human on both sides. Rate limit plus HA ban integration. | M2 tests |
| T4 | **Pre-auth DoS / parser bugs** via the internet-exposed endpoint | B1 | Handshake-only state machine before auth. 64 KiB frame cap. 5 s auth deadline. Per-IP and global connection caps. `process_wrong_login` on failures. Handshake fuzzing in CI. | M2 fuzz + limits tests |
| T5 | **Stolen agent credential** (copied key files / backup) | A1 | Per-install keys. Concurrent sessions for the same device ID → both dropped plus a repair issue (clone detection). HA "delete device" revokes. Backups encrypted with a user passphrase (Argon2id + AEAD). | M2 clone test, M10 backup test |
| T6 | **Replayed remote commands** | B1 | Noise per-session nonces. Durable `command_id` dedupe. Relative `ttl_s`. Non-idempotent commands never replayed across reconnects. | Protocol fixtures (M1), M2 replay test |
| T7 | **HA account compromise** → abuse of device controls | B2 | Agent-side policy is authoritative. Per-transport permission tiers. `power_admin`, `game_management` destructive ops, and `experimental` are off by default for remote. Optional on-device confirmation. Agent audit log. No arbitrary command set (closed enum, typed args). | Fixtures `command_unknown_name`, `command_launch_extra_arg`; M2 policy tests |
| T8 | **Compromised Decky plugin or loader** (root, shared JS context) | B4 | Plugin runs without the `root` flag. Socket 0600 plus `SO_PEERCRED` plus a minted token. `local-ui-low` tier: can't change permissions, run power/admin, approve updates, or unpair. **Residual:** root can read A1. Accepted and documented. | M4 tests |
| T9 | **Browser-based attacks on the loopback UI** (CSRF, DNS rebinding) | B5 | Bind 127.0.0.1 only. Strict `Host`/`Origin` allowlist. Random per-launch path token. SameSite=Strict. No CORS. Short-lived. | M2 tests |
| T10 | **Steam credential exfiltration** via diagnostics, logs, or telemetry | B3 | Allowlisted-key VDF parser. Sensitive files never copied or cached. Diagnostics built from an allowlist, not a denylist. Redaction tests with canary secrets. | M2/M6 tests |
| T11 | **Command injection** into Steam URIs or exec | B3 | `app_id` is uint32 in the schema and re-validated in Go. `exec.Command` with fixed argv, no shell. URI template is constant. | Fixture `command_launch_string_app_id`; M6 tests |
| T12 | **Notification abuse** (phishing UI, triggering HA actions) | B2 | Actions are opaque IDs (schema forbids extra fields). Responses become HA events only. HA validates notification/action/expiry. Sensitive HA actions need HA-side confirmation. Max 3 actions, length caps. | Fixture `notification_service_call_action`; M7 tests |
| T13 | **Privilege escalation on device** | B6 | No root components in v1 (ADR-0003). Power via logind/polkit as the user only. Never prompt for or store a sudo password. | Install tests (M2/M3) |
| T14 | **Compromised release artifact / update channel** | A8 | Signed artifacts (key offline/hardware-backed). Signature, version monotonicity, and compatibility checked before slot switch. Rollback on failed health check. Unsigned → rejected. | M9 tests |
| T15 | **Privacy over-collection** | A6 | Play history opt-in, local, 90-day default retention, delete/export. Steam account ID hashed. Notification previews follow privacy settings. Telemetry minimised. | M6/M7 |
| T16 | **Integration abused as an HA pivot** | A7 | The device channel can only produce DeckBeacon entities, events, and command results. No device-originated HA service calls. All inputs schema-validated before use. | M2 tests |
| T17 | **Game-session disruption** (power or update during play) | A4 | `busy_in_game` deferral, `defer_if_in_game`, confirmation for force actions, update safe windows. | M6/M9 tests |

## 4. Out of scope / accepted risks
- An attacker with root on the SteamOS device: full compromise of the agent (T8 residual).
- HA admin who deliberately grants all permission tiers: they own the house.
- Undocumented Steam `SteamClient` behaviour changes: handled by gating, not security controls (ADR-0004).
- Nabu Casa infrastructure compromise: it carries TLS to HA. The Noise layer keeps device traffic confidential even there.

## 5. Security requirements checklist (enforced per milestone)
- [x] No HA access tokens on the device: CI grep over agent sources plus E2E scan of agent state (M2).
- [x] Every inbound message is schema-validated before use, on both sides (M2).
- [x] Diagnostics canary test (M2). Agent logs contain no key material by construction (keys are never formatted). A log-capture canary test is still to add.
- [x] Closed command set with typed args (M1 schemas plus fixtures).
- [x] Notification actions cannot carry service calls (M1 schema plus fixture).
- [x] Secret scanning in CI (M1).

## Update (M9, 2026-11-17): managed agent updates (ADR-0010)
| Threat | Mitigation |
|---|---|
| Compromised release artifact or release page | Ed25519 signature over `release.json`, keys embedded in the agent (rotation list), asset sha256 and size bound in the signed manifest. The tag must match the manifest version. Verified before anything is staged. |
| Downgrade or rollback attack | Only versions newer than the running one are offered. Rollback happens only to the locally kept previous version, by the updater or the user. |
| Unattended or forged installation | Requires `approve_agent_update {version}` for the exact version with `confirmed_by_user`, under the separate `updates` tier, enforced on the agent. Approvals expire after 7 days. |
| Broken release bricks the device | Independent updater (transient user unit), health check (new version and pid, reconnect if it was connected), automatic rollback, manual `deckbeacon update rollback`. Identity and trust are never touched. |
| Malicious download host or redirect | HTTPS only. Every byte is verified against the signed manifest before use. |
| Signing key compromise | The key lives only with the owner and in an Actions secret. Rotation by shipping a release that trusts the new key. The residual risk is documented. |
| Privilege escalation | None: user units only (`systemd-run --user`, `systemctl --user`). The existing CI grep covers it. |
