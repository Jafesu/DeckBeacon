# ADR-0011: Connection profile refresh, fast failover, and local rediscovery

Status: Proposed (M5, 2026-11-17)

## Context
Spec 09 asks for the following:
- Local first (mDNS plus a manual address), with remote sessions always outbound.
- HA tells the agent its remote URL (incl. HA Cloud), and the agent caches both profiles.
- Automatic fallback local → remote when the LAN fails, and remote → local after a stability threshold.
- Identity is preserved, and sessions and events are deduplicated across transitions.
- A manual external HTTPS/WSS URL is supported.
- Heartbeat 15 s / timeout 45 s are provisional.

HV-R1 proved the HA Cloud path. ADR-0001 fixed the direction: the agent dials HA. What M2–M3 built:
- URLs from pairing only;
- local URLs tried first, then remote;
- while remote, a local probe every 5 min, switching back after 2 successes;
- 20 s dial timeout for every URL;
- HA supersedes an older session of the same device;
- events dedupe by sequence.

Gaps measured from the code:
1. **The profile never refreshes.** Turning on HA Cloud, changing the external URL or moving HA's IP needs a re-pair.
2. **Failover is slow.** A dropped LAN is only noticed through the heartbeat (45 s), then up to 20 s are spent on an unreachable local URL: about 65 s.
3. **Switching back is slow.** About 10 min.
4. **No rediscovery.** Nothing finds HA again if its LAN address changes.

## Decision
1. **Profile refresh (protocol 1.6, additive).** `welcome` carries an optional `connection_profile {urls: [{url, kind}]}`, computed the same way as at pairing. Kinds: `internal`, `external`, `cloud`, `manual`. It is plus an optional owner-set **extra device URL** (HA option), e.g. a VPN or reverse-proxy address, sent as `manual_remote`.
   - The agent accepts it **only from the authenticated session**: Noise IK with the pinned HA key, so a LAN peer can't inject URLs. It validates every entry (http/https, no credentials, ≤ 8 URLs, ≤ 512 chars) and persists the list atomically in `trust.json`.
   - An empty or invalid profile never wipes the stored list.
2. **Network-change awareness.** The agent watches its own interface addresses and default route (`/proc/net/route` and interface addresses, polled every 3 s: no privileges, no netlink dependency). On a change:
   - **while connected:** send a ping at once and expect the pong within 5 s, otherwise drop the session and reconnect immediately (no backoff);
   - **while remote:** probe local at once.
3. **Dial timeouts by kind.** 5 s for local and discovered URLs, 20 s for remote, so an unreachable LAN address can't stall failover.
4. **Return to local faster, without flapping.** While remote, probe local every 30 s (and on any network change), and switch after **3 consecutive successes**: at most about 90 s after arriving home, and never because of one lucky probe.
5. **Local rediscovery.** If every stored local URL fails, browse mDNS (`_home-assistant._tcp`, 3 s, existing `discovery` package) before going remote, and try what it finds.
   - **Trust still comes only from the pinned key:** a spoofed announcement can't complete the Noise IK handshake, so it fails like any unreachable URL.
   - A discovered URL that works is used for that session. HA's next profile refresh makes it permanent if it is HA's real internal URL.
6. **Visibility.** `hello.session.via` (optional): the URL kind in use (`internal|external|cloud|manual|manual_remote|discovered`). The HA connection sensor shows it as an attribute.

## Consequences
- **Typical failover:** Wi-Fi off or a network switch is noticed within about 3 s, the local dial fails fast (≤ 5 s), then the remote connects: well under 30 s. The unannounced-drop case still relies on the 45 s heartbeat. Arriving home switches back within about 90 s.
- **Unchanged:**
  - **No inbound port:** the agent still only dials out.
  - **No HA token** on the device.
  - **Same session and permissions:** the Noise session and the per-transport permission tiers are unchanged.
  - **Remote stays opt-in on HA's side:** it requires HA Cloud remote UI or an external URL.

## Validation
- **Unit tests:**
  - profile validation and persistence (incl. the no-wipe rule and rejection from an unauthenticated source);
  - network-watcher change detection with a fake `/proc`;
  - ping-on-change failover against the fake HA;
  - 3-success return to local;
  - dial timeouts;
  - discovery fallback with a spoofed responder failing the handshake.
- **HA tests:** profile in welcome, extra URL option, `via` attribute, event dedupe across a local → remote transition.
- **G15 (owner, later):** home → phone hotspot → home, timing each switch, with HA Cloud.
