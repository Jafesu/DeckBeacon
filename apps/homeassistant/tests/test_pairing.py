"""Pairing (M2-AC2): SAS confirmed on both sides, window-gated, one device per window."""

from __future__ import annotations

from homeassistant.config_entries import ConfigEntryState
from homeassistant.core import HomeAssistant
from homeassistant.data_entry_flow import FlowResultType

from custom_components.deckbeacon.const import CONF_DEVICE_ID, CONF_DEVICE_STATIC, DOMAIN

from .agent_sim import SimAgent
from .helpers import advance, pair, start


async def test_pair_endpoint_404_without_window(hass: HomeAssistant, client) -> None:
    resp = await client.get("/api/deckbeacon/v1/pair")
    assert resp.status == 404


async def test_full_pairing_creates_entry(hass: HomeAssistant, client) -> None:
    agent = SimAgent()
    entry = await pair(hass, client, agent)
    assert entry.unique_id == agent.device_id
    assert entry.data[CONF_DEVICE_ID] == agent.device_id
    assert entry.data[CONF_DEVICE_STATIC] == agent.static.public.hex()
    assert entry.state is ConfigEntryState.LOADED
    # Window is consumed: a second device cannot reuse it.
    resp = await client.get("/api/deckbeacon/v1/pair")
    assert resp.status == 404


async def test_ha_rejects_code(hass: HomeAssistant, client) -> None:
    agent = SimAgent()
    flow_id, _ = await start(hass, client, agent)
    result = await hass.config_entries.flow.async_configure(flow_id, {"next_step_id": "reject"})
    assert result["type"] is FlowResultType.ABORT and result["reason"] == "code_rejected"
    msg = await agent.recv()
    assert msg["type"] == "pair_abort" and msg["body"]["reason"] == "sas_rejected"
    assert not hass.config_entries.async_entries(DOMAIN)


async def test_device_rejects_code(hass: HomeAssistant, client) -> None:
    agent = SimAgent()
    flow_id, _ = await start(hass, client, agent)
    result = await hass.config_entries.flow.async_configure(flow_id, {"next_step_id": "accept"})
    assert result["type"] is FlowResultType.SHOW_PROGRESS
    await agent.confirm(False)
    result = await advance(hass, flow_id)
    if result is not None and result["type"] is FlowResultType.SHOW_PROGRESS_DONE:
        result = await advance(hass, flow_id)
    assert result is not None and result["type"] is FlowResultType.ABORT and result["reason"] == "device_rejected", result
    assert not hass.config_entries.async_entries(DOMAIN)


async def test_garbage_does_not_burn_window(hass: HomeAssistant, client) -> None:
    result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": "user"})
    flow_id = result["flow_id"]
    ws = await client.ws_connect("/api/deckbeacon/v1/pair")
    await ws.send_bytes(b"\x00" * 7)  # not a valid Noise message
    msg = await ws.receive(timeout=5)
    assert msg.type.name in {"CLOSE", "CLOSED", "CLOSING"}
    await hass.async_block_till_done()
    # The real device can still pair in the same window.
    agent = SimAgent()
    await agent.start_pairing(client)
    result = await advance(hass, flow_id)
    assert result is not None and result["type"] is FlowResultType.MENU


async def test_already_paired_device_is_refused(hass: HomeAssistant, client) -> None:
    agent = SimAgent()
    await pair(hass, client, agent)
    await hass.config_entries.flow.async_init(DOMAIN, context={"source": "user"})
    await agent.start_pairing(client)
    msg = await agent.recv()
    assert msg["type"] == "pair_abort" and msg["body"]["reason"] == "already_paired"
    assert len(hass.config_entries.async_entries(DOMAIN)) == 1
