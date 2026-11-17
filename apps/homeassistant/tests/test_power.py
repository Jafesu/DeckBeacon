"""Power state (M3-AC6): announced transitions, unknown on unannounced drops,
independent of connection; unknown event types from newer agents are skipped."""

from __future__ import annotations

import asyncio
import json

from homeassistant.core import HomeAssistant
from homeassistant.helpers import entity_registry as er

from custom_components.deckbeacon.const import DOMAIN, EVENT_DEVICE
from custom_components.deckbeacon.protocol import ulid

from .agent_sim import SimAgent, now
from .helpers import pair


def st(hass: HomeAssistant, device_id: str, key: str) -> str:
    eid = er.async_get(hass).async_get_entity_id("sensor", DOMAIN, f"{device_id}_{key}")
    return hass.states.get(eid).state


async def settle(hass: HomeAssistant) -> None:
    for _ in range(5):
        await asyncio.sleep(0.02)
        await hass.async_block_till_done()


async def test_power_state_lifecycle(hass: HomeAssistant, client) -> None:
    agent = SimAgent()
    await pair(hass, client, agent)
    await settle(hass)
    assert st(hass, agent.device_id, "power_state") == "unknown"

    await agent.connect(client)
    await settle(hass)
    assert st(hass, agent.device_id, "power_state") == "on"

    # Announced suspend survives the connection drop.
    await agent.event("device_suspending", {"source": "logind", "confidence": "confirmed"})
    assert (await agent.recv())["type"] == "ack"
    await agent.close()
    await settle(hass)
    assert st(hass, agent.device_id, "power_state") == "suspended"
    assert st(hass, agent.device_id, "connection") == "offline"  # independent axes

    # Resume: hello → on; a backfilled (live=false) suspend event must not override it.
    await agent.connect(client)
    await agent.event("device_suspending", {}, live=False)
    await agent.recv()
    await agent.event("device_resumed", {"source": "logind"})
    await agent.recv()
    await settle(hass)
    assert st(hass, agent.device_id, "power_state") == "on"

    # Unannounced drop (e.g. Wi-Fi loss): power is unknown, never assumed off.
    await agent.close()
    await settle(hass)
    assert st(hass, agent.device_id, "power_state") == "unknown"

    # Boot announcement.
    await agent.connect(client)
    await agent.event("device_booted", {"boot_time": "2026-11-16T14:40:00Z"})
    await agent.recv()
    await settle(hass)
    assert st(hass, agent.device_id, "power_state") == "on"


async def test_unknown_event_type_is_acked_and_skipped(hass: HomeAssistant, client) -> None:
    agent = SimAgent()
    await pair(hass, client, agent)
    fired = []
    hass.bus.async_listen(EVENT_DEVICE, lambda e: fired.append(e.data))
    await agent.connect(client)
    # Bypass the simulator's validation to emulate a newer agent.
    agent.seq += 1
    env = {
        "v": 1,
        "type": "event",
        "id": ulid(),
        "ts": now(),
        "seq": agent.seq,
        "body": {"event_id": ulid(), "event_seq": 1, "event_type": "device_teleported", "occurred_at": now(), "live": True, "data": {}},
    }
    await agent.ws.send_bytes(agent.session.seal(json.dumps(env).encode()))
    ack = await agent.recv()
    assert ack["type"] == "ack" and ack["body"]["event_seq"] == 1
    await agent.send("ping", {})
    assert (await agent.recv())["type"] == "pong"  # session survived
    await settle(hass)
    assert fired == []


async def test_announced_suspend_survives_reload(hass: HomeAssistant, client) -> None:
    """Found on the G15: an entry reload erased a live 'suspended' state."""
    agent = SimAgent()
    entry = await pair(hass, client, agent)
    await agent.connect(client)
    await agent.event("device_suspending", {"source": "logind", "confidence": "confirmed"})
    await agent.recv()
    await agent.close()  # agent closes the session after the ack (M3 follow-up)
    await settle(hass)
    assert st(hass, agent.device_id, "power_state") == "suspended"
    assert await hass.config_entries.async_reload(entry.entry_id)
    await settle(hass)
    assert st(hass, agent.device_id, "power_state") == "suspended"
    assert st(hass, agent.device_id, "connection") == "offline"
    await agent.connect(client)  # wake
    await settle(hass)
    assert st(hass, agent.device_id, "power_state") == "on"
