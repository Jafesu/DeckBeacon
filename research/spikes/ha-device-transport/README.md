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

## v0.0.2 (2026-11-16): config flow for the HV-R1 HA Cloud test
- `config_flow.py`: enrols one `device_id` and Ed25519 public key as a config entry (no YAML needed).
- Unloading or deleting the entry removes the key and closes any live session. With no keys enrolled, the endpoint returns **404**.
- Client: `-soak <dur> -interval <dur>` keeps the session open, sending app-level ping and telemetry, and reports RTT p50/p95/max.
- Local regression re-run: T1–T4 pass, 20 s soak with no disconnects (`RESULTS-2026-11-16-v2.txt`).

### Temporary install on a real HA (HV-R1)
From the HA **Terminal & SSH** add-on:
```bash
mkdir -p /config/custom_components/deckbeacon_spike && cd /config/custom_components/deckbeacon_spike && \
for f in __init__.py config_flow.py manifest.json; do \
  wget -q "https://raw.githubusercontent.com/Jafesu/DeckBeacon/claude/jolly-mendel-9ig3mz/research/spikes/ha-device-transport/custom_components/deckbeacon_spike/$f"; done && ls
```
Then restart HA. Removal: delete the config entry, run `rm -rf /config/custom_components/deckbeacon_spike`, and restart.
