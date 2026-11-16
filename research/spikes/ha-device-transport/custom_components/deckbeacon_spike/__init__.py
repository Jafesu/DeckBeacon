"""Milestone 0 spike: device-key authenticated WebSocket inside Home Assistant.

NOT PRODUCTION CODE. Purpose: prove that a custom integration can expose its own
WebSocket endpoint on HA's HTTP server, authenticate a device with an Ed25519
challenge/response (no HA access token on the device), receive telemetry and
push commands. Anything reachable on HA's HTTP server is, by construction, also
reachable through the HA Cloud remote-UI TCP/SNI proxy; that leg still needs an
end-to-end test against a real Nabu Casa subscription.
"""
from __future__ import annotations

import base64
import json
import logging
import secrets
import time

from aiohttp import WSMsgType, web
from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey

from homeassistant.components.http import HomeAssistantView
from homeassistant.core import HomeAssistant
from homeassistant.util import slugify

DOMAIN = "deckbeacon_spike"
_LOGGER = logging.getLogger(__name__)
CONTEXT = b"deckbeacon-spike-v0\x00"
MAX_MSG = 64 * 1024
AUTH_TIMEOUT = 5.0


async def async_setup(hass: HomeAssistant, config: dict) -> bool:
    conf = config.get(DOMAIN, {})
    devices = {
        dev_id: Ed25519PublicKey.from_public_bytes(base64.b64decode(pub))
        for dev_id, pub in conf.get("devices", {}).items()
    }
    hass.data[DOMAIN] = {"devices": devices, "seen_cmd_acks": set(), "connections": {}}
    hass.http.register_view(DeviceWebSocketView(hass))
    return True


class DeviceWebSocketView(HomeAssistantView):
    url = "/api/deckbeacon_spike/ws"
    name = "api:deckbeacon_spike:ws"
    requires_auth = False  # device auth is done in-protocol, never with an HA token

    def __init__(self, hass: HomeAssistant) -> None:
        self.hass = hass

    async def get(self, request: web.Request) -> web.WebSocketResponse:
        data = self.hass.data[DOMAIN]
        ws = web.WebSocketResponse(max_msg_size=MAX_MSG, heartbeat=15)
        await ws.prepare(request)

        nonce = secrets.token_bytes(32)
        await ws.send_json({"type": "challenge", "nonce": base64.b64encode(nonce).decode(), "ts": time.time()})
        try:
            msg = await ws.receive(timeout=AUTH_TIMEOUT)
        except TimeoutError:
            await ws.close(code=4001, message=b"auth timeout")
            return ws
        if msg.type != WSMsgType.TEXT:
            await ws.close(code=4002, message=b"auth expected")
            return ws
        try:
            auth = json.loads(msg.data)
            dev_id = auth["device_id"]
            sig = base64.b64decode(auth["sig"])
            key = data["devices"][dev_id]
            key.verify(sig, CONTEXT + nonce + dev_id.encode())
        except (KeyError, ValueError, InvalidSignature):
            _LOGGER.warning("deckbeacon_spike: rejected device auth")
            await ws.send_json({"type": "auth_invalid"})
            await ws.close(code=4003, message=b"auth failed")
            return ws

        await ws.send_json({"type": "auth_ok"})
        data["connections"][dev_id] = ws
        # Spike: push one command right after auth to prove HA->device direction.
        await ws.send_json({"type": "command", "command_id": "cmd-1", "name": "ping", "expires_at": time.time() + 30})
        try:
            async for msg in ws:
                if msg.type != WSMsgType.TEXT:
                    continue
                payload = json.loads(msg.data)
                if payload.get("type") == "telemetry":
                    entity_id = f"sensor.{DOMAIN}_{slugify(dev_id)}_cpu"
                    self.hass.states.async_set(
                        entity_id, payload["cpu_percent"],
                        {"seq": payload["seq"], "unit_of_measurement": "%"},
                    )
                    _LOGGER.warning("deckbeacon_spike: state %s", self.hass.states.get(entity_id))
                elif payload.get("type") == "command_ack":
                    cid = payload["command_id"]
                    if cid in data["seen_cmd_acks"]:
                        await ws.send_json({"type": "duplicate", "command_id": cid})
                    else:
                        data["seen_cmd_acks"].add(cid)
                        self.hass.bus.async_fire(f"{DOMAIN}_command_ack", {"device_id": dev_id, "command_id": cid})
                        _LOGGER.warning("deckbeacon_spike: command ack %s accepted", cid)
        finally:
            data["connections"].pop(dev_id, None)
        return ws
