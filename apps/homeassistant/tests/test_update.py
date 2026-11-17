"""M9 agent update entity: installed/latest, notes, approval with confirmation,
progress, permission, offline."""

from __future__ import annotations

import asyncio

import pytest
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import entity_registry as er

from custom_components.deckbeacon.const import DOMAIN

from .agent_sim import SimAgent
from .helpers import pair

OPTS = {"game_management_local": True, "game_management_remote": False, "updates_local": True, "updates_remote": True, "update_channel": "stable"}


async def settle(hass: HomeAssistant) -> None:
    for _ in range(5):
        await asyncio.sleep(0.02)
        await hass.async_block_till_done()


async def setup(hass, client, transport="local"):
    agent = SimAgent()
    entry = await pair(hass, client, agent)
    welcome = await agent.connect(client, transport=transport)
    eid = er.async_get(hass).async_get_entity_id("update", DOMAIN, f"{agent.device_id}_agent_update")
    return agent, entry, welcome, eid


async def test_update_entity_shows_available_version_and_installs(hass: HomeAssistant, client) -> None:
    agent, entry, welcome, eid = await setup(hass, client)
    assert "updates" in welcome["body"]["policy"]["granted"]
    st = hass.states.get(eid)
    assert st.state == "off" and st.attributes["installed_version"] == "0.1.0"  # nothing reported yet: up to date
    await agent.telemetry(
        [
            {"key": "agent.update.state", "value": "available"},
            {"key": "agent.update.latest", "value": "0.3.0"},
            {"key": "agent.update.notes_url", "value": "https://github.com/Jafesu/DeckBeacon/releases/tag/agent/v0.3.0"},
            {"key": "agent.update.channel", "value": "stable"},
        ]
    )
    await settle(hass)
    st = hass.states.get(eid)
    assert st.state == "on" and st.attributes["latest_version"] == "0.3.0"
    assert st.attributes["release_url"].endswith("agent/v0.3.0") and st.attributes["title"] == "DeckBeacon agent"

    async def device_side():
        cmd = await agent.recv(timeout=5)
        body = cmd["body"]
        assert body["name"] == "approve_agent_update" and body["args"] == {"version": "0.3.0"} and body["confirmed_by_user"] is True
        await agent.send("command_result", {"command_id": body["command_id"], "state": "confirmed", "result": {"version": "0.3.0", "state": "waiting"}})

    task = asyncio.create_task(device_side())
    await hass.services.async_call("update", "install", {"entity_id": eid}, blocking=True)
    await task

    await agent.telemetry([{"key": "agent.update.state", "value": "waiting"}, {"key": "agent.update.reason", "value": "Stardew Valley is running"}])
    await settle(hass)
    st = hass.states.get(eid)
    assert st.attributes["in_progress"] is True and st.attributes["detail"] == "Stardew Valley is running"


async def test_update_refusals(hass: HomeAssistant, client) -> None:
    agent, entry, welcome, eid = await setup(hass, client)
    await agent.telemetry([{"key": "agent.update.state", "value": "available"}, {"key": "agent.update.latest", "value": "0.3.0"}])
    await settle(hass)

    async def device_rejects():
        cmd = await agent.recv(timeout=5)
        await agent.send(
            "command_result",
            {"command_id": cmd["body"]["command_id"], "state": "rejected", "error": {"code": "disabled", "message": "updates are turned off on the device"}},
        )

    task = asyncio.create_task(device_rejects())
    with pytest.raises(HomeAssistantError, match="turned off on the device"):
        await hass.services.async_call("update", "install", {"entity_id": eid}, blocking=True)
    await task

    # Denied over remote when the owner turns that off.
    result = await hass.config_entries.options.async_init(entry.entry_id)
    await hass.config_entries.options.async_configure(
        result["flow_id"],
        OPTS | {"updates_remote": False},
    )
    await settle(hass)
    welcome = await agent.connect(client, transport="remote")
    assert "updates" not in welcome["body"]["policy"]["granted"]
    await agent.telemetry([{"key": "agent.update.state", "value": "available"}, {"key": "agent.update.latest", "value": "0.3.0"}])
    await settle(hass)
    with pytest.raises(HomeAssistantError, match="not allowed over the remote"):
        await hass.services.async_call("update", "install", {"entity_id": eid}, blocking=True)


async def test_update_offline_refused(hass: HomeAssistant, client) -> None:
    agent, entry, welcome, eid = await setup(hass, client)
    await agent.telemetry([{"key": "agent.update.state", "value": "available"}, {"key": "agent.update.latest", "value": "0.3.0"}])
    await settle(hass)
    await agent.ws.close()
    await settle(hass)
    # Live metrics are cleared on disconnect, so HA shows no pending update and
    # refuses the install itself; nothing can reach the offline device.
    with pytest.raises(HomeAssistantError):
        await hass.services.async_call("update", "install", {"entity_id": eid}, blocking=True)
