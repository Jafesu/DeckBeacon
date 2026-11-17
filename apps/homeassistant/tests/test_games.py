"""M6 games: library snapshot, actions, permissions, game and download sensors."""

from __future__ import annotations

import asyncio

import pytest
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError, ServiceValidationError
from homeassistant.helpers import entity_registry as er

from custom_components.deckbeacon.const import DOMAIN

from .agent_sim import SimAgent
from .helpers import get_device, pair

LIB = [
    {"app_id": 413150, "name": "Stardew Valley", "kind": "game", "status": "installed", "size_on_disk": 708901690, "library": "lib0"},
    {"app_id": 204360, "name": "Castle Crashers", "kind": "game", "status": "installed", "last_played": "2026-11-17T01:47:20Z"},
    {"app_id": 1493710, "name": "Proton Experimental", "kind": "tool", "status": "installed"},
]


async def settle(hass: HomeAssistant) -> None:
    for _ in range(5):
        await asyncio.sleep(0.02)
        await hass.async_block_till_done()


def st(hass: HomeAssistant, device_id: str, key: str) -> str:
    eid = er.async_get(hass).async_get_entity_id("sensor", DOMAIN, f"{device_id}_{key}")
    return hass.states.get(eid).state


async def setup(hass: HomeAssistant, client, transport: str = "local"):
    agent = SimAgent()
    entry = await pair(hass, client, agent)
    welcome = await agent.connect(client, transport=transport)
    # Two pages to exercise assembly.
    await agent.send("library", {"revision": 1, "page": 1, "pages": 2, "games": LIB[:2]})
    await agent.send("library", {"revision": 1, "page": 2, "pages": 2, "games": LIB[2:]})
    await agent.send("ping", {})
    assert (await agent.recv())["type"] == "pong"
    return agent, entry, get_device(hass, entry.entry_id).id, welcome


async def test_library_list_and_get(hass: HomeAssistant, client) -> None:
    agent, entry, device_id, _ = await setup(hass, client)
    res = await hass.services.async_call(DOMAIN, "list_games", {"device_id": device_id}, blocking=True, return_response=True)
    assert [g["name"] for g in res["games"]] == ["Castle Crashers", "Stardew Valley"] and res["revision"] == 1
    res = await hass.services.async_call(DOMAIN, "list_games", {"device_id": device_id, "include_tools": True}, blocking=True, return_response=True)
    assert res["count"] == 3
    game = await hass.services.async_call(DOMAIN, "get_game", {"device_id": device_id, "app_id": 413150}, blocking=True, return_response=True)
    assert game["name"] == "Stardew Valley" and "library" not in game
    with pytest.raises(ServiceValidationError):
        await hass.services.async_call(DOMAIN, "get_game", {"device_id": device_id, "app_id": 1}, blocking=True, return_response=True)


async def test_launch_game_local(hass: HomeAssistant, client) -> None:
    agent, entry, device_id, welcome = await setup(hass, client)
    assert "game_management" in welcome["body"]["policy"]["granted"]

    async def device_side():
        cmd = await agent.recv(timeout=5)
        assert cmd["type"] == "command" and cmd["body"]["name"] == "launch_game" and cmd["body"]["args"] == {"app_id": 413150}
        cid = cmd["body"]["command_id"]
        await agent.send("command_result", {"command_id": cid, "state": "accepted"})
        await agent.send("command_result", {"command_id": cid, "state": "dispatched", "result": {"app_id": 413150}})

    task = asyncio.create_task(device_side())
    res = await hass.services.async_call(DOMAIN, "launch_game", {"device_id": device_id, "app_id": 413150}, blocking=True, return_response=True)
    await task
    assert res == {"state": "dispatched", "app_id": 413150, "name": "Stardew Valley"}

    with pytest.raises(ServiceValidationError):  # not installed: refused before reaching the device
        await hass.services.async_call(DOMAIN, "launch_game", {"device_id": device_id, "app_id": 999}, blocking=True, return_response=True)


async def test_launch_rejected_by_device(hass: HomeAssistant, client) -> None:
    agent, entry, device_id, _ = await setup(hass, client)

    async def device_side():
        cmd = await agent.recv(timeout=5)
        await agent.send(
            "command_result",
            {
                "command_id": cmd["body"]["command_id"],
                "state": "rejected",
                "error": {"code": "unavailable", "message": "Stardew Valley is not ready (updating)"},
            },
        )

    task = asyncio.create_task(device_side())
    with pytest.raises(HomeAssistantError, match="not ready"):
        await hass.services.async_call(DOMAIN, "launch_game", {"device_id": device_id, "app_id": 413150}, blocking=True, return_response=True)
    await task


async def test_remote_denied_by_default_and_options(hass: HomeAssistant, client) -> None:
    agent, entry, device_id, welcome = await setup(hass, client, transport="remote")
    assert welcome["body"]["policy"]["granted"] == ["monitoring"]
    with pytest.raises(ServiceValidationError, match="not allowed over the remote"):
        await hass.services.async_call(DOMAIN, "launch_game", {"device_id": device_id, "app_id": 413150}, blocking=True, return_response=True)

    result = await hass.config_entries.options.async_init(entry.entry_id)
    result = await hass.config_entries.options.async_configure(result["flow_id"], {"game_management_local": True, "game_management_remote": True})
    await settle(hass)  # options change reloads the entry; the device reconnects
    welcome = await agent.connect(client, transport="remote")
    assert welcome["body"]["policy"]["granted"] == ["monitoring", "game_management"]


async def test_game_and_download_sensors(hass: HomeAssistant, client) -> None:
    agent, entry, device_id, _ = await setup(hass, client)
    none = {"value": None, "reason": "no game running"}
    await agent.telemetry(
        [
            {"key": "game.current.name", **none},
            {"key": "game.current.app_id", **none},
            {"key": "library.games", "value": 28},
            {"key": "library.updates", "value": 1},
            {"key": "downloads.status", "value": "downloading"},
            {"key": "downloads.name", "value": "Starfield"},
            {"key": "downloads.progress", "value": 25.0, "unit": "%"},
            {"key": "downloads.rate_bps", "value": 52428800, "unit": "B/s"},
            {"key": "downloads.eta_s", "value": 1800, "unit": "s"},
        ]
    )
    await settle(hass)
    assert st(hass, agent.device_id, "game_current_name") == "Not playing"
    assert st(hass, agent.device_id, "library_games") == "28"
    assert st(hass, agent.device_id, "downloads_status") == "downloading"
    assert st(hass, agent.device_id, "downloads_progress") == "25.0"
    assert float(st(hass, agent.device_id, "downloads_rate_bps")) == pytest.approx(52.4288, rel=1e-3)  # MB/s
    assert float(st(hass, agent.device_id, "downloads_eta_s")) == 30  # minutes

    await agent.telemetry(
        [
            {"key": "game.current.name", "value": "Castle Crashers"},
            {"key": "game.current.app_id", "value": 204360},
            {"key": "game.session.started_at", "value": "2026-11-17T02:30:00Z"},
            {"key": "game.session.duration_s", "value": 600, "unit": "s"},
        ]
    )
    await settle(hass)
    assert st(hass, agent.device_id, "game_current_name") == "Castle Crashers"
    eid = er.async_get(hass).async_get_entity_id("sensor", DOMAIN, f"{agent.device_id}_game_current_name")
    assert hass.states.get(eid).attributes["app_id"] == 204360
    assert float(st(hass, agent.device_id, "game_session_duration_s")) == 10
