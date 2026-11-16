# apps/homeassistant: DeckBeacon Home Assistant integration (Python)

Domain `deckbeacon`. Minimum HA **2026.5**. Hosts the device endpoints (`/api/deckbeacon/v1/pair`, `/api/deckbeacon/v1/ws`), the SAS pairing config flow, entities, diagnostics, and repairs.

## Pairing (M2)
1. Settings → Devices & services → Add integration → **DeckBeacon**. A 10-minute pairing window opens.
2. On the SteamOS device: `deckbeacon pair --url http://<home-assistant>:8123`
3. Compare the 6-digit code on both sides and confirm it in HA **and** on the device.

## Development
```bash
uv venv -p 3.14 .venv && uv pip install -p .venv/bin/python pytest-homeassistant-custom-component jsonschema ruff pytest-timeout
.venv/bin/pytest -q            # includes the Go-agent E2E test if `go` is on PATH
python3 ../../scripts/sync_schemas.py --check
```
Schemas in `custom_components/deckbeacon/schemas/` are vendored from `packages/protocol`. Do not edit them here.
