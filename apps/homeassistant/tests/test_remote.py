"""M5 remote access on the HA side: connection profile, extra URL, via, transitions (ADR-0011)."""

from __future__ import annotations

import asyncio

from aiohttp import WSMsgType
from homeassistant.core import HomeAssistant
from homeassistant.core_config import async_process_ha_core_config
from homeassistant.data_entry_flow import FlowResultType
from homeassistant.helpers import entity_registry as er

from custom_components.deckbeacon.const import DOMAIN, EVENT_DEVICE

from .agent_sim import SimAgent
from .helpers import pair

OPTS = {"game_management_local": True, "game_management_remote": False, "updates_local": True, "updates_remote": True, "update_channel": "stable"}


async def settle(hass: HomeAssistant) -> None:
    for _ in range(5):
        await asyncio.sleep(0.02)
        await hass.async_block_till_done()


def conn_entity(hass: HomeAssistant, device_id: str) -> str:
    eid = er.async_get(hass).async_get_entity_id("sensor", DOMAIN, f"{device_id}_connection")
    assert eid
    return eid


async def set_options(hass: HomeAssistant, entry, **extra) -> dict:
    result = await hass.config_entries.options.async_init(entry.entry_id)
    return await hass.config_entries.options.async_configure(result["flow_id"], OPTS | extra)


async def test_welcome_carries_profile_with_working_url(hass: HomeAssistant, client) -> None:
    await async_process_ha_core_config(hass, {"internal_url": "http://10.10.10.10:8123", "external_url": "https://deck.example.org"})
    agent = SimAgent()
    await pair(hass, client, agent)
    welcome = await agent.connect(client, via="manual")
    urls = welcome["body"]["connection_profile"]["urls"]
    kinds = {u["url"]: u["kind"] for u in urls}
    assert kinds["http://10.10.10.10:8123"] == "internal"
    assert kinds["https://deck.example.org"] == "external"
    # The address the device actually used is always in the list, even when
    # HA's configured URLs are wrong (the regression the Go E2E test found).
    here = [u for u in urls if u["kind"] == "manual"]
    assert len(here) == 1 and here[0]["url"].startswith("http://127.0.0.1")
    await agent.close()


async def test_working_url_labelled_remote_when_arrived_remotely(hass: HomeAssistant, client) -> None:
    agent = SimAgent()
    await pair(hass, client, agent)
    welcome = await agent.connect(client, transport="remote", via="cloud")
    here = [u for u in welcome["body"]["connection_profile"]["urls"] if u["url"].startswith("http://127.0.0.1")]
    assert here and here[0]["kind"] == "manual_remote"  # never tried as a fast local URL
    await agent.close()


async def test_extra_device_url_option(hass: HomeAssistant, client) -> None:
    agent = SimAgent()
    entry = await pair(hass, client, agent)
    for bad in ("ftp://vpn.example", "https://user:pw@vpn.example", "https://vpn.example/?t=1", "not a url", "https://" + "a" * 520 + ".example"):
        result = await set_options(hass, entry, extra_device_url=bad)
        assert result["type"] is FlowResultType.FORM and result["errors"] == {"extra_device_url": "invalid_url"}, bad
    result = await set_options(hass, entry, extra_device_url=" https://vpn.example:8443/ ")
    assert result["type"] is FlowResultType.CREATE_ENTRY
    assert entry.options["extra_device_url"] == "https://vpn.example:8443"
    await settle(hass)
    welcome = await agent.connect(client)
    assert {"url": "https://vpn.example:8443", "kind": "manual_remote"} in welcome["body"]["connection_profile"]["urls"]
    await agent.close()
    # Clearing it removes it from the profile.
    result = await set_options(hass, entry)
    assert "extra_device_url" not in entry.options
    await settle(hass)
    welcome = await agent.connect(client)
    assert all(u["kind"] != "manual_remote" for u in welcome["body"]["connection_profile"]["urls"])
    await agent.close()


async def test_local_to_remote_transition(hass: HomeAssistant, client) -> None:
    """AC5/AC6: same device and entities, old session closed, no duplicate or lost events."""
    agent = SimAgent()
    await pair(hass, client, agent)
    fired = []
    hass.bus.async_listen(EVENT_DEVICE, lambda e: fired.append(e.data["event_seq"]))
    await agent.connect(client, via="internal")
    await settle(hass)
    eid = conn_entity(hass, agent.device_id)
    st = hass.states.get(eid)
    assert st.state == "local" and st.attributes["via"] == "internal"
    for _ in range(2):
        await agent.event("connection_changed", {"transport": "local"})
        await agent.recv()
    old_ws = agent.ws
    entry_id = hass.config_entries.async_entries(DOMAIN)[0].entry_id
    entities_before = {e.entity_id for e in er.async_entries_for_config_entry(er.async_get(hass), entry_id)}

    # The LAN drops without a close; the agent arrives over HA Cloud and
    # replays the last event it never saw acked plus a new one.
    remote = SimAgent()
    remote.static, remote.device_id, remote.server_static = agent.static, agent.device_id, agent.server_static
    welcome = await remote.connect(client, transport="remote", resume=1, via="cloud")
    assert welcome["type"] == "welcome" and welcome["body"]["last_received_event_seq"] == 2
    msg = await old_ws.receive(timeout=5)
    assert msg.type in (WSMsgType.CLOSE, WSMsgType.CLOSED) and msg.data == 4009  # superseded
    remote.event_seq = 1
    for _ in range(2):  # seq 2 (replay) and 3 (new)
        await remote.event("connection_changed", {"transport": "remote"})
        await remote.recv()
    await settle(hass)
    assert fired == [1, 2, 3]
    st = hass.states.get(eid)
    assert st.state == "remote" and st.attributes["via"] == "cloud"
    assert {e.entity_id for e in er.async_entries_for_config_entry(er.async_get(hass), entry_id)} == entities_before
    assert len(hass.config_entries.async_entries(DOMAIN)) == 1
    await remote.close()
    await settle(hass)
    assert hass.states.get(eid).state == "offline" and "via" not in hass.states.get(eid).attributes
