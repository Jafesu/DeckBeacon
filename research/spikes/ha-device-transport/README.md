# Spike: device-key authenticated WebSocket inside Home Assistant

**Throwaway Milestone 0 research code. Not production. Not part of any build or release.**

Question: can a custom integration expose its own WebSocket endpoint on HA's HTTP server and authenticate a device *without* any HA access token? If so, HA Cloud's remote UI, which tunnels into the same aiohttp app, should carry it too.

## Run
```bash
python3 -m venv /tmp/havenv && /tmp/havenv/bin/pip install homeassistant
./run_spike.sh /tmp/havenv/bin/python /tmp/spikerun
```
Requires Go ≥ 1.22. HA installs frontend dependencies on first start, which takes a few minutes.

## Result (2026-11-16, HA core 2026.2.3 on Python 3.13, Linux container)
See `RESULTS-2026-11-16.txt`.

| Test | Result |
|---|---|
| T1 enrolled Ed25519 key → `auth_ok`, HA→device command received, telemetry written to HA state, duplicate ack detected | pass |
| T2 unenrolled key → `auth_invalid`, close 4003 | pass |
| T3 HA REST `/api/states` without token → 401 | pass |
| T4 device key against HA's own `/api/websocket` → `auth_required` (no HA access) | pass |

## Known deficiencies (intentional for a spike)
- No channel encryption or integrity after auth. Over plain HTTP the session is hijackable. Production uses Noise (ADR-0002).
- Device keys come from YAML. No pairing, rate limiting, or ban integration.
- HA 2026.2.3 was used because PyPI's latest HA requires Python 3.14. Re-test on ≥ 2026.11 in M2.
