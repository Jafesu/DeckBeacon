# Milestone 5 — Remote access: Plan

Status: **In progress** (started 2026-11-17; owner: "Let's do M5"; G15 in transit, so the away-from-home test comes last). Design: ADR-0011. The transport path through HA Cloud was proven in M0 (HV-R1).

## Work packages
| WP | Deliverable |
|---|---|
| WP1 | **Protocol 1.6**: `welcome.connection_profile`, `hello.session.via`, `manual_remote` URL kind. Fixtures. |
| WP2 | **Agent profile refresh**: validate and persist URLs from the authenticated welcome; never wipe on an empty or invalid profile. |
| WP3 | **Fast failover**: network watcher (addresses and default route), ping-on-change with a 5 s pong deadline, per-kind dial timeouts (5 s local, 20 s remote). |
| WP4 | **Return to local**: 30 s probes plus a probe on network change, 3 consecutive successes. |
| WP5 | **mDNS rediscovery** before falling back to remote; pinned-key handshake is the only trust. |
| WP6 | **HA**: profile in welcome, "extra device URL" option, `via` on the connection sensor, transition test (supersede and event dedupe). |
| WP7 | Docs (remote access guide: requirements, timings, limits), results. **G15 (owner):** home → hotspot → home with HA Cloud, timed. |

## Acceptance criteria
- AC1 A profile change in HA (e.g. enabling HA Cloud, a new external URL, a new IP) reaches the agent at its next connection without re-pairing. An empty or invalid profile never removes working URLs (tests).
- AC2 After a network change (Wi-Fi lost or switched), the agent is connected over a remaining path in under 30 s (tests with the fake HA; G15 timing).
- AC3 After returning home, the agent is back on local within about 90 s, and one successful probe alone never switches (tests; G15).
- AC4 If HA's LAN address changes, mDNS rediscovery reconnects locally. A spoofed announcement never results in a session (tests).
- AC5 Across a local ↔ remote transition: same device and HA entities, no duplicate or lost events, the old session is closed (tests).
- AC6 HA shows the transport and the path (`via`: internal/external/cloud/…). The owner can add a device URL (VPN or reverse proxy) (HA tests).
- AC7 Still no inbound port, no HA token on the device, and remote permissions unchanged (review + existing CI greps).
- AC8 CI green.

## Progress (2026-11-17, G15 in transit)
| AC | Status | Evidence |
|---|---|---|
| AC1 profile refresh | **pass (software)** | `trust.ValidateProfile` tests (schemes, credentials, query, length, count, kinds, dedupe). Session tests: the profile from the welcome is applied and persisted, an invalid one never wipes stored URLs, and the connected URL is kept when a profile omits it. HA: the profile in the welcome includes internal, external and the extra URL, plus the address in use. The Go ↔ HA E2E test caught the dropped-working-URL regression, which is now fixed on both sides. |
| AC2 failover | **pass (software)**, G15 pending | `netwatch` change detection on a fake `/proc`. A network change with a muted fake HA leads to a new session in < 7 s. Per-kind dial timeouts. |
| AC3 return to local | **pass (software)**, G15 pending | `switchableProxy` test: back on local only after ≥ 3 consecutive probes. |
| AC4 rediscovery | **pass (software)** | Discovery is tried before remote, and a spoofed responder fails the pinned-key handshake and never yields a session. |
| AC5 transition | **pass** | HA `test_local_to_remote_transition`: the old session is closed (4009), the event replay is deduplicated (1, 2, 3 fired once each), entities are unchanged, one config entry. |
| AC6 visibility + extra URL | **pass** | The Connection sensor has the `via` attribute (cleared when offline). Extra-URL option validation (scheme, credentials, query, length) and normalization; it appears as `manual_remote` and disappears when cleared. 57 HA tests on 2026.5.1 + 2026.10.4. |
| AC7 no new exposure | **pass (review)** | Still outbound only. The profile is accepted only inside the authenticated session, and the existing CI greps are unchanged. |
| AC8 CI | pending push | |

Guide: `docs/remote-access.md`.

## Hardware validation (owner, when the G15 is back)
1. Update the agent (built from this branch), and update the integration to 0.4.0 from the Marketplace. Confirm the Connection sensor shows `local` with `via: internal` (or `manual`).
2. HA Cloud remote UI on. Switch the G15 to a phone hotspot. Time how long until the sensor shows `remote` with `via: cloud` (target < 30 s).
3. Rejoin home Wi-Fi. Time until `local` (target ≤ ~90 s, never a quick flip-flop).
4. Check the logbook: no duplicate DeckBeacon events across the switches.
