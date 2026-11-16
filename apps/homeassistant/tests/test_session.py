"""Device sessions: telemetry → entities, staleness, events, revocation, limits."""

from __future__ import annotations

import asyncio

import pytest
from aiohttp import WSMsgType
from homeassistant.const import STATE_UNAVAILABLE
from homeassistant.core import HomeAssistant
from homeassistant.helpers import entity_registry as er

from custom_components.deckbeacon import noise
from custom_components.deckbeacon.const import DOMAIN, EVENT_DEVICE

from .agent_sim import SimAgent
from .helpers import get_device, pair

METRICS = [
    {"key": "cpu.usage", "value": 23.5, "unit": "%"},
    {"key": "battery.level", "value": 76, "unit": "%"},
    {"key": "battery.charging", "value": False},
    {"key": "thermal.cpu_c", "value": 64.3, "unit": "°C"},
    {"key": "gpu.busy", "value": None, "reason": "no supported GPU utilisation source"},
    {"key": "session.mode", "value": "gaming"},
    {"key": "system.boot_time", "value": "2026-11-16T14:40:00Z"},
    {"key": "unknown.future_metric", "value": 1},
]


def ent(hass: HomeAssistant, device_id: str, key: str, domain: str = "sensor") -> str:
    entity_id = er.async_get(hass).async_get_entity_id(domain, DOMAIN, f"{device_id}_{key}")
    assert entity_id, f"no entity for {key}"
    return entity_id


async def settle(hass: HomeAssistant) -> None:
    for _ in range(5):
        await asyncio.sleep(0.02)
        await hass.async_block_till_done()


@pytest.fixture
async def paired(hass: HomeAssistant, client):
    agent = SimAgent()
    entry = await pair(hass, client, agent)
    return agent, entry


async def test_telemetry_creates_entities_and_staleness(hass: HomeAssistant, client, paired) -> None:
    agent, entry = paired
    welcome = await agent.connect(client)
    assert welcome["type"] == "welcome"
    assert welcome["body"]["policy"]["granted"] == ["monitoring"]
    await agent.telemetry(METRICS)
    await settle(hass)

    dev = get_device(hass, entry.entry_id)
    assert dev is not None and dev.model == "Steam Deck OLED" and dev.manufacturer == "Valve"

    assert hass.states.get(ent(hass, agent.device_id, "cpu_usage")).state == "23.5"
    assert hass.states.get(ent(hass, agent.device_id, "battery_level")).attributes["device_class"] == "battery"
    assert hass.states.get(ent(hass, agent.device_id, "thermal_cpu_c")).attributes["unit_of_measurement"] == "°C"
    assert hass.states.get(ent(hass, agent.device_id, "session_mode")).state == "gaming"
    assert hass.states.get(ent(hass, agent.device_id, "system_boot_time")).state == "2026-11-16T14:40:00+00:00"
    # null value → unavailable, never a cached number (AC4)
    assert hass.states.get(ent(hass, agent.device_id, "gpu_busy")).state == STATE_UNAVAILABLE
    assert hass.states.get(ent(hass, agent.device_id, "connection")).state == "local"
    assert hass.states.get(ent(hass, agent.device_id, "connected", "binary_sensor")).state == "on"
    assert hass.states.get(ent(hass, agent.device_id, "battery_charging", "binary_sensor")).state == "off"

    await agent.close()
    await settle(hass)
    assert hass.states.get(ent(hass, agent.device_id, "cpu_usage")).state == STATE_UNAVAILABLE
    assert hass.states.get(ent(hass, agent.device_id, "connection")).state == "offline"
    assert hass.states.get(ent(hass, agent.device_id, "connected", "binary_sensor")).state == "off"
    last_seen = hass.states.get(ent(hass, agent.device_id, "last_seen")).state
    assert last_seen not in (STATE_UNAVAILABLE, "unknown")  # staleness stays visible

    # HA restart equivalent: reload entry; entities for seen metrics are recreated, no re-pairing (AC5)
    assert await hass.config_entries.async_reload(entry.entry_id)
    await settle(hass)
    assert hass.states.get(ent(hass, agent.device_id, "cpu_usage")).state == STATE_UNAVAILABLE
    welcome = await agent.connect(client)
    assert welcome["type"] == "welcome"
    await agent.telemetry([{"key": "cpu.usage", "value": 5.0}])
    await settle(hass)
    assert hass.states.get(ent(hass, agent.device_id, "cpu_usage")).state == "5.0"


async def test_events_deduplicated_and_resumed(hass: HomeAssistant, client, paired) -> None:
    agent, _ = paired
    fired = []
    hass.bus.async_listen(EVENT_DEVICE, lambda e: fired.append(e.data))
    await agent.connect(client)
    e1 = await agent.event("connection_changed", {"transport": "local"})
    ack = await agent.recv()
    assert ack["type"] == "ack" and ack["body"]["event_seq"] == e1["event_seq"]
    await agent.event("connection_changed", {"transport": "local"}, seq=e1["event_seq"])  # replay
    ack = await agent.recv()
    assert ack["type"] == "ack"
    await settle(hass)
    assert len(fired) == 1 and fired[0]["live"] is True

    await agent.close()
    await settle(hass)
    welcome = await agent.connect(client, resume=1)
    assert welcome["body"]["last_received_event_seq"] == 1  # agent can drop acked events
    await agent.event("device_resumed", {}, live=False)
    await agent.recv()
    await settle(hass)
    assert [f["event_seq"] for f in fired] == [1, 2] and fired[1]["live"] is False


async def test_revocation_on_entry_removal(hass: HomeAssistant, client, paired) -> None:
    agent, entry = paired
    await agent.connect(client)
    await hass.config_entries.async_remove(entry.entry_id)
    await settle(hass)
    # HA unloads the entry before removing it, so the live session is closed first...
    with pytest.raises(ConnectionResetError):
        await agent.recv()
    # ...and the agent's reconnect with the old key is refused (AC8).
    welcome = await agent.connect(client)
    assert welcome["type"] == "error" and welcome["body"]["error"]["code"] == "permission_denied"


async def test_unknown_device_rejected(hass: HomeAssistant, client, paired) -> None:
    agent, _ = paired
    stranger = SimAgent()
    stranger.server_static = agent.server_static  # knows HA's public key, but was never paired
    reply = await stranger.connect(client)
    assert reply["type"] == "error" and reply["body"]["error"]["code"] == "permission_denied"


async def test_device_id_must_match_key(hass: HomeAssistant, client, paired) -> None:
    agent, _ = paired
    impostor = SimAgent()
    impostor.static = agent.static
    impostor.server_static = agent.server_static
    reply = await impostor.connect(client)  # right key, different device_id in hello
    assert reply["type"] == "error" and reply["body"]["error"]["code"] == "permission_denied"


async def test_newer_session_supersedes_older(hass: HomeAssistant, client, paired) -> None:
    agent, _ = paired
    await agent.connect(client)
    first_ws = agent.ws
    second = SimAgent()
    second.static, second.device_id, second.server_static = agent.static, agent.device_id, agent.server_static
    assert (await second.connect(client))["type"] == "welcome"
    msg = await first_ws.receive(timeout=5)
    assert msg.type in (WSMsgType.CLOSE, WSMsgType.CLOSED) and msg.data == 4009


async def test_unknown_message_type_is_non_fatal(hass: HomeAssistant, client, paired) -> None:
    """Lenient receiver: an unexpected (but schema-valid) type yields a non-fatal error."""
    agent, _ = paired
    await agent.connect(client)
    await agent.send("pair_confirm", {"accepted": True})  # valid envelope, wrong context
    msg = await agent.recv()
    assert msg["type"] == "error" and msg["body"]["error"]["code"] == "unsupported_type" and msg["body"]["fatal"] is False
    await agent.send("ping", {})
    assert (await agent.recv())["type"] == "pong"


async def test_preauth_garbage_and_oversize(hass: HomeAssistant, client, paired) -> None:
    """Pre-auth limits (AC9): garbage handshake, oversize frame, no crash."""
    ws = await client.ws_connect("/api/deckbeacon/v1/ws")
    await ws.send_bytes(b"\x01" * 10)
    msg = await ws.receive(timeout=5)
    assert msg.type in (WSMsgType.CLOSE, WSMsgType.CLOSED)

    ws = await client.ws_connect("/api/deckbeacon/v1/ws")
    await ws.send_bytes(b"\x00" * 70_000)
    msg = await ws.receive(timeout=5)
    assert msg.type in (WSMsgType.CLOSE, WSMsgType.CLOSED, WSMsgType.ERROR)

    ws = await client.ws_connect("/api/deckbeacon/v1/ws")
    await ws.send_str("hello")  # text frame during handshake
    msg = await ws.receive(timeout=5)
    assert msg.type in (WSMsgType.CLOSE, WSMsgType.CLOSED)

    # The real device still works afterwards.
    agent, _ = paired
    assert (await agent.connect(client))["type"] == "welcome"


async def test_wrong_server_key_fails_handshake(hass: HomeAssistant, client, paired) -> None:
    agent, _ = paired
    agent.server_static = noise.KeyPair.generate().public
    with pytest.raises((ConnectionResetError, noise.NoiseError, TimeoutError, TypeError, AttributeError)):
        await agent.connect(client)
