"""Pairing helper shared by tests."""

from __future__ import annotations

import asyncio

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant
from homeassistant.data_entry_flow import FlowResultType

from custom_components.deckbeacon.const import DOMAIN

from .agent_sim import SimAgent


async def advance(hass: HomeAssistant, flow_id: str, user_input=None, timeout: float = 10.0):
    """Advance a flow past a pending progress step.

    Progress tasks wait on the device (background tasks), so poll until the step
    moves on; tolerate HA having auto-advanced the flow by itself.
    """
    loop = asyncio.get_running_loop()
    deadline = loop.time() + timeout
    first = None
    while True:
        await hass.async_block_till_done()
        if all(f["flow_id"] != flow_id for f in hass.config_entries.flow.async_progress()):
            return None
        result = await hass.config_entries.flow.async_configure(flow_id, user_input)
        if first is None:
            first = result.get("progress_action")
        if result["type"] is not FlowResultType.SHOW_PROGRESS or result.get("progress_action") != first or loop.time() > deadline:
            return result
        await asyncio.sleep(0.05)


async def start(hass: HomeAssistant, client, agent: SimAgent) -> tuple[str, dict]:
    """Open the window, connect the agent, return (flow_id, menu result)."""
    result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": "user"})
    assert result["type"] is FlowResultType.SHOW_PROGRESS, result
    flow_id = result["flow_id"]
    await agent.start_pairing(client)
    result = await advance(hass, flow_id)
    assert result is not None and result["type"] is FlowResultType.MENU, result
    return flow_id, result


async def pair(hass: HomeAssistant, client, agent: SimAgent) -> ConfigEntry:
    """Run the full SAS pairing between the config flow and a simulated agent."""
    flow_id, menu = await start(hass, client, agent)
    assert menu["description_placeholders"]["code"].replace(" ", "") == agent.sas
    result = await hass.config_entries.flow.async_configure(flow_id, {"next_step_id": "accept"})
    assert result["type"] is FlowResultType.SHOW_PROGRESS, result
    await agent.confirm(True)
    result = await advance(hass, flow_id)
    if result is not None and result["type"] is FlowResultType.SHOW_PROGRESS_DONE:
        result = await advance(hass, flow_id)
    assert result is not None and result["type"] is FlowResultType.CREATE_ENTRY, result
    msg = await agent.recv(timeout=15)
    assert msg["type"] == "pair_complete", msg
    await agent.close()
    await hass.async_block_till_done()
    return result["result"]
