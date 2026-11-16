# ADR-0001: Agent dials a DeckBeacon endpoint hosted inside Home Assistant

Status: Proposed (M0, 2026-11-16)

## Context
Spec 04 asks for HA Zeroconf discovery of the agent, which implies the agent listens on the LAN. Spec 03 says the agent binds to loopback only. Spec 09 says the agent initiates remote sessions with no inbound ports. Using HA→agent on the LAN and agent→HA remotely would mean two transports, two auth paths, and harder session dedupe during local↔remote failover.

The M0 spike showed that a custom integration can host its own WebSocket endpoint on HA's HTTP server, with device authentication independent of HA tokens. Source review shows HA Cloud's remote UI tunnels into that same aiohttp app.

## Decision
- The DeckBeacon HA integration registers `/api/deckbeacon/v1/ws` (device channel) and `/api/deckbeacon/v1/pair` (active only during a pairing window) as `HomeAssistantView`s with `requires_auth=False`. Authentication happens in-protocol (ADR-0002).
- The agent is **always the client**. It connects to HA's internal URL on the LAN, or to the external/cloud URL remotely, choosing from the connection profile HA delivers inside the encrypted channel at pairing.
- The agent opens **no LAN listener**. Discovery runs the other way: the agent browses `_home-assistant._tcp` (or takes a manual URL). Its pairing request makes the integration start an `integration_discovery` config flow, so HA's "Discovered" UX is preserved.
- Failover: the agent keeps one active connection. Local is preferred. It switches to remote after N failed local attempts and returns to local after a stability threshold. The server enforces one live session per device ID; a newer authenticated session supersedes the old one, which closes with code `4009 superseded`.

## Consequences
- One transport, one protocol, one auth path. HA restarts and IP changes never need re-pairing, because the agent just reconnects.
- HA must be reachable from the Deck. That's true for any LAN setup; remote depends on HA Cloud or an external URL (HV-R1/R2).
- The pre-auth endpoint is exposed wherever HA is exposed (including the internet via HA Cloud). It must be minimal and hardened (report §6 R1).
- If HA is down, the agent buffers events in a bounded queue, as specified.

## Validation
Spike T1–T4 (done, LAN, HA 2026.2.3). HV-R1 (HA Cloud), HV-R2 (external URL). Re-run the spike on HA ≥ 2026.11 in M2.
