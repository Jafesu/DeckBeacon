"""M8 artwork: device fetch over the session, sha check, disk cache, authenticated
view, signed URLs, now-playing image entity, WebSocket API."""

from __future__ import annotations

import asyncio
import base64
import hashlib

from homeassistant.core import HomeAssistant
from homeassistant.helpers import entity_registry as er

from custom_components.deckbeacon.const import DOMAIN

from .agent_sim import SimAgent
from .helpers import get_device, pair

LIB = [
    {"app_id": 413150, "name": "Stardew Valley", "kind": "game", "status": "installed"},
    {"app_id": 4244106195, "name": "Hollow Acre", "kind": "shortcut", "status": "installed"},
    {"app_id": 1493710, "name": "Proton Experimental", "kind": "tool", "status": "installed"},
]
JPEG = b"\xff\xd8\xff\xe0" + bytes(range(256)) * 300  # ~77 KB: 3 chunks


async def setup(hass: HomeAssistant, client):
    agent = SimAgent()
    entry = await pair(hass, client, agent)
    await agent.connect(client)
    await agent.send("library", {"revision": 1, "page": 1, "pages": 1, "games": LIB})
    await agent.send("ping", {})
    assert (await agent.recv())["type"] == "pong"
    return agent, entry


async def serve_artwork(agent: SimAgent, data: bytes = JPEG, *, corrupt: bool = False, reject: bool = False) -> dict:
    """Answer one get_artwork command the way the Go agent does."""
    cmd = await agent.recv(timeout=5)
    assert cmd["type"] == "command" and cmd["body"]["name"] == "get_artwork", cmd
    cid, args = cmd["body"]["command_id"], cmd["body"]["args"]
    if reject:
        await agent.send("command_result", {"command_id": cid, "state": "rejected", "error": {"code": "unavailable", "message": "no capsule artwork"}})
        return args
    sha = hashlib.sha256(data).hexdigest()
    parts = [data[i : i + 32768] for i in range(0, len(data), 32768)]
    for i, p in enumerate(parts):
        if corrupt and i == 1:
            p = b"x" * len(p)
        body = {
            "command_id": cid,
            "app_id": args["app_id"],
            "kind": args["kind"],
            "mime": "image/jpeg",
            "width": 600,
            "height": 900,
            "seq": i,
            "total": len(parts),
            "sha256": sha,
            "data": base64.b64encode(p).decode(),
        }
        await agent.send("artwork", body)
    await agent.send("command_result", {"command_id": cid, "state": "confirmed", "result": {"sha256": sha}})
    return args


async def test_view_fetches_caches_and_requires_auth(hass: HomeAssistant, client, hass_client) -> None:
    agent, entry = await setup(hass, client)
    url = f"/api/deckbeacon/artwork/{entry.entry_id}/413150/capsule"
    assert (await client.get(url)).status == 401  # unauthenticated

    auth = await hass_client()
    task = asyncio.create_task(serve_artwork(agent))
    resp = await auth.get(url)
    assert await task == {"app_id": 413150, "kind": "capsule"}
    assert resp.status == 200 and resp.content_type == "image/jpeg" and await resp.read() == JPEG
    assert resp.headers["X-Content-Type-Options"] == "nosniff"

    # Served from cache: no second command reaches the device.
    resp = await auth.get(url)
    assert resp.status == 200 and await resp.read() == JPEG
    await agent.send("ping", {})
    assert (await agent.recv())["type"] == "pong"

    # Unknown apps, tools' ids that were not reported, and bad kinds never reach the device.
    for bad in ("/999/capsule", "/413150/../../config", "/413150/icon"):
        assert (await auth.get(f"/api/deckbeacon/artwork/{entry.entry_id}{bad}")).status == 404


async def test_bad_checksum_and_missing_artwork(hass: HomeAssistant, client, hass_client) -> None:
    agent, entry = await setup(hass, client)
    auth = await hass_client()
    url = f"/api/deckbeacon/artwork/{entry.entry_id}/413150/header"
    task = asyncio.create_task(serve_artwork(agent, corrupt=True))
    assert (await auth.get(url)).status == 404  # sha256 mismatch: discarded, not cached
    await task

    url = f"/api/deckbeacon/artwork/{entry.entry_id}/4244106195/capsule"
    task = asyncio.create_task(serve_artwork(agent, reject=True))
    assert (await auth.get(url)).status == 404
    await task
    assert (await auth.get(url)).status == 404  # remembered: no repeated asks
    await agent.send("ping", {})
    assert (await agent.recv())["type"] == "pong"


async def test_websocket_api_and_signed_urls(hass: HomeAssistant, client, hass_ws_client) -> None:
    agent, entry = await setup(hass, client)
    ws = await hass_ws_client(hass)
    await ws.send_json({"id": 1, "type": "deckbeacon/devices"})
    devices = (await ws.receive_json())["result"]["devices"]
    assert len(devices) == 1
    dev = devices[0]
    assert dev["connected"] and dev["can_launch"] and dev["entities"]["game_to_launch"].startswith("select.")
    assert dev["device_id"] == get_device(hass, entry.entry_id).id

    await ws.send_json({"id": 2, "type": "deckbeacon/library", "device_id": dev["device_id"]})
    lib = (await ws.receive_json())["result"]
    assert [g["name"] for g in lib["games"]] == ["Hollow Acre", "Stardew Valley"]  # tools excluded
    signed = lib["games"][1]["artwork"]["capsule"]
    assert "authSig=" in signed

    # A signed path works for <img> without the bearer token.
    task = asyncio.create_task(serve_artwork(agent))
    resp = await client.get(signed)
    await task
    assert resp.status == 200 and await resp.read() == JPEG

    await ws.send_json({"id": 3, "type": "deckbeacon/artwork_url", "device_id": dev["device_id"], "app_id": 999, "kind": "capsule"})
    assert (await ws.receive_json())["success"] is False


async def test_now_playing_image_entity(hass: HomeAssistant, client, hass_client) -> None:
    agent, entry = await setup(hass, client)
    eid = er.async_get(hass).async_get_entity_id("image", DOMAIN, f"{agent.device_id}_now_playing_artwork")
    assert hass.states.get(eid).state == "unavailable"  # nothing playing
    await agent.telemetry([{"key": "game.current.app_id", "value": 413150}])
    for _ in range(5):
        await asyncio.sleep(0.02)
        await hass.async_block_till_done()
    state = hass.states.get(eid)
    assert state.state != "unavailable"
    auth = await hass_client()
    task = asyncio.create_task(serve_artwork(agent))
    resp = await auth.get(f"/api/image_proxy/{eid}")
    await task
    assert resp.status == 200 and await resp.read() == JPEG


async def test_integration_brand_icon_served_locally(hass: HomeAssistant, client, hass_client) -> None:
    """HA's brands component serves custom_components/deckbeacon/brand/ first (2026.5+)."""
    assert "brands" in hass.config.components
    auth = await hass_client()
    for image in ("icon.png", "logo@2x.png", "dark_icon.png"):
        resp = await auth.get(f"/api/brands/integration/{DOMAIN}/{image}?placeholder=no")
        assert resp.status == 200, image
        assert (await resp.read())[:8] == b"\x89PNG\r\n\x1a\n"
