"""Diagnostics never contain keys or secrets (AC10)."""

from __future__ import annotations

import json

from homeassistant.core import HomeAssistant

from custom_components.deckbeacon.diagnostics import async_get_config_entry_diagnostics

from .agent_sim import SimAgent
from .helpers import pair


async def test_diagnostics_have_no_secrets(hass: HomeAssistant, client) -> None:
    agent = SimAgent()
    entry = await pair(hass, client, agent)
    await agent.connect(client)
    await agent.telemetry([{"key": "cpu.usage", "value": 1.0}])
    await agent.send("ping", {})
    assert (await agent.recv())["type"] == "pong"  # telemetry (sent earlier) has been processed
    await hass.async_block_till_done()
    diag = json.dumps(await async_get_config_entry_diagnostics(hass, entry))
    hub = hass.data["deckbeacon"]
    for secret in (hub.static.private.hex(), agent.static.private.hex(), agent.static.public.hex(), hub.static.public.hex()):
        assert secret not in diag
    assert "cpu.usage" in diag and "steamos" in diag
