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
