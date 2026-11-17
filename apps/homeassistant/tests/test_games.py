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
    {"app_id": 4244106195, "name": "Hollow Acre", "kind": "shortcut", "status": "installed", "last_played": "2026-11-17T02:19:37Z"},
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
    assert [g["name"] for g in res["games"]] == ["Castle Crashers", "Hollow Acre", "Stardew Valley"] and res["revision"] == 1
    res = await hass.services.async_call(DOMAIN, "list_games", {"device_id": device_id, "include_tools": True}, blocking=True, return_response=True)
    assert res["count"] == 4
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


async def test_launch_shortcut_sends_u32(hass: HomeAssistant, client) -> None:
    agent, entry, device_id, _ = await setup(hass, client)

    async def device_side():
        cmd = await agent.recv(timeout=5)
        # The canonical key travels; the agent derives the 64-bit GameID itself (ADR-0008 D3).
        assert cmd["body"]["args"] == {"app_id": 4244106195}
        await agent.send("command_result", {"command_id": cmd["body"]["command_id"], "state": "dispatched"})

    task = asyncio.create_task(device_side())
    res = await hass.services.async_call(DOMAIN, "launch_game", {"device_id": device_id, "app_id": 4244106195}, blocking=True, return_response=True)
    await task
    assert res["name"] == "Hollow Acre"


async def test_ui_mode_and_session_sensors(hass: HomeAssistant, client) -> None:
    agent, entry, device_id, _ = await setup(hass, client)
    await agent.telemetry(
        [
            {"key": "steam.ui_mode", "value": "gamepad"},
            {"key": "session.type", "value": "desktop"},
            {"key": "library.shortcuts", "value": 7},
        ]
    )
    await settle(hass)
    assert st(hass, agent.device_id, "steam_ui_mode") == "gamepad"  # Big Picture inside Desktop Mode
    assert st(hass, agent.device_id, "session_type") == "desktop"
    assert st(hass, agent.device_id, "library_shortcuts") == "7"
    await agent.telemetry([{"key": "steam.ui_mode", "value": "unknown:9"}, {"key": "session.type", "value": None, "reason": "neither"}])
    await settle(hass)
    assert st(hass, agent.device_id, "steam_ui_mode") == "unknown"
    assert st(hass, agent.device_id, "session_type") in ("unknown", "unavailable")


async def test_activity_sensors(hass: HomeAssistant, client) -> None:
    agent, entry, device_id, _ = await setup(hass, client)
    await agent.telemetry(
        [
            {"key": "game.activity.details", "value": "Night City — Watson"},
            {"key": "game.activity.state", "value": "Act 1"},
            {"key": "game.activity.party_size", "value": 2},
            {"key": "game.activity.party_max", "value": 4},
            {"key": "game.activity.started_at", "value": "2026-11-17T02:15:18Z"},
        ]
    )
    await settle(hass)
    assert st(hass, agent.device_id, "game_activity_details") == "Night City — Watson"
    assert st(hass, agent.device_id, "game_activity_party_max") == "4"
    none = {"value": None, "reason": "no activity from the running game"}
    await agent.telemetry([{"key": "game.activity.details", **none}, {"key": "game.activity.state", **none}])
    await settle(hass)
    assert st(hass, agent.device_id, "game_activity_details") == "unavailable"  # AC6: never guessed


async def test_game_picker_and_launch_button(hass: HomeAssistant, client) -> None:
    agent, entry, device_id, _ = await setup(hass, client)
    await settle(hass)
    reg = er.async_get(hass)
    sel = reg.async_get_entity_id("select", DOMAIN, f"{agent.device_id}_game_to_launch")
    btn = reg.async_get_entity_id("button", DOMAIN, f"{agent.device_id}_launch_game")
    state = hass.states.get(sel)
    assert state.attributes["options"] == ["Castle Crashers", "Hollow Acre", "Stardew Valley"]  # tools excluded

    with pytest.raises(ServiceValidationError, match="Choose a game"):
        await hass.services.async_call("button", "press", {"entity_id": btn}, blocking=True)

    await hass.services.async_call("select", "select_option", {"entity_id": sel, "option": "Hollow Acre"}, blocking=True)
    assert hass.states.get(sel).state == "Hollow Acre" and hass.states.get(sel).attributes["app_id"] == 4244106195

    async def device_side():
        cmd = await agent.recv(timeout=5)
        assert cmd["body"]["name"] == "launch_game" and cmd["body"]["args"] == {"app_id": 4244106195}
        await agent.send("command_result", {"command_id": cmd["body"]["command_id"], "state": "dispatched"})

    task = asyncio.create_task(device_side())
    await hass.services.async_call("button", "press", {"entity_id": btn}, blocking=True)
    await task


async def test_launch_button_unavailable_when_remote_denied(hass: HomeAssistant, client) -> None:
    agent, entry, device_id, _ = await setup(hass, client, transport="remote")
    await settle(hass)
    btn = er.async_get(hass).async_get_entity_id("button", DOMAIN, f"{agent.device_id}_launch_game")
    assert hass.states.get(btn).state == "unavailable"


def test_duplicate_names_are_disambiguated() -> None:
    from types import SimpleNamespace

    from custom_components.deckbeacon.select import launchable

    rt = SimpleNamespace(
        library={
            1: {"app_id": 2961728726, "name": "Discord", "kind": "shortcut", "status": "installed"},
            2: {"app_id": 3248090148, "name": "Discord", "kind": "shortcut", "status": "installed"},
            3: {"app_id": 1716740, "name": "Starfield", "kind": "game", "status": "downloading"},
        }
    )
    assert launchable(rt) == {"Discord (2961728726)": 2961728726, "Discord (3248090148)": 3248090148}


async def test_close_game_action_and_buttons(hass: HomeAssistant, client) -> None:
    agent, entry, device_id, _ = await setup(hass, client)
    reg = er.async_get(hass)
    close_btn = reg.async_get_entity_id("button", DOMAIN, f"{agent.device_id}_close_game")
    force_id = reg.async_get_entity_id("button", DOMAIN, f"{agent.device_id}_force_close_game")
    assert reg.async_get(force_id).disabled_by is not None  # opt-in: no confirmation possible on a button
    assert hass.states.get(close_btn).state == "unavailable"  # nothing running
    with pytest.raises(ServiceValidationError, match="No game is running"):
        await hass.services.async_call(DOMAIN, "close_game", {"device_id": device_id}, blocking=True, return_response=True)

    await agent.telemetry([{"key": "game.current.app_id", "value": 413150}, {"key": "game.current.name", "value": "Stardew Valley"}])
    await settle(hass)
    assert hass.states.get(close_btn).state != "unavailable"

    async def device_side(expect_force: bool):
        cmd = await agent.recv(timeout=5)
        body = cmd["body"]
        assert body["name"] == "close_game" and body["args"] == {"app_id": 413150, "force": expect_force}
        assert body.get("confirmed_by_user", False) is expect_force  # force carries HA-side confirmation
        await agent.send("command_result", {"command_id": body["command_id"], "state": "dispatched"})

    task = asyncio.create_task(device_side(False))
    res = await hass.services.async_call(DOMAIN, "close_game", {"device_id": device_id}, blocking=True, return_response=True)
    await task
    assert res == {"state": "dispatched", "app_id": 413150, "name": "Stardew Valley", "force": False}

    task = asyncio.create_task(device_side(True))
    await hass.services.async_call(DOMAIN, "close_game", {"device_id": device_id, "force": True}, blocking=True, return_response=True)
    await task

    task = asyncio.create_task(device_side(False))
    await hass.services.async_call("button", "press", {"entity_id": close_btn}, blocking=True)
    await task


async def test_close_game_denied_remotely(hass: HomeAssistant, client) -> None:
    agent, entry, device_id, _ = await setup(hass, client, transport="remote")
    await agent.telemetry([{"key": "game.current.app_id", "value": 413150}])
    await settle(hass)
    with pytest.raises(ServiceValidationError, match="not allowed over the remote"):
        await hass.services.async_call(DOMAIN, "close_game", {"device_id": device_id}, blocking=True, return_response=True)
