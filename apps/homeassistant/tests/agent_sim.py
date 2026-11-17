"""A Python stand-in for the Go agent, speaking the real protocol.

Used to test the integration in isolation; the Go agent ↔ integration interop is
covered by tests/e2e at the repository root.
"""

from __future__ import annotations

import base64
import hashlib
import json
import os
from datetime import UTC, datetime
from typing import Any

from aiohttp import WSMsgType

from custom_components.deckbeacon import noise
from custom_components.deckbeacon.protocol import load_validator, ulid

VALIDATOR = load_validator()


def now() -> str:
    return datetime.now(UTC).replace(microsecond=0).isoformat().replace("+00:00", "Z")


class SimAgent:
    def __init__(self, name: str = "steamdeck") -> None:
        self.static = noise.KeyPair.generate()
        digest = hashlib.sha256(os.urandom(32)).digest()
        self.device_id = base64.b32encode(digest).decode().lower()[:26]
        self.name = name
        self.server_static: bytes | None = None
        self.ws = None
        self.session: noise.Session | None = None
        self.seq = 0
        self.event_seq = 0
        self.sas: str | None = None

    def device(self) -> dict[str, Any]:
        return {"device_id": self.device_id, "model": "Steam Deck OLED", "vendor": "Valve", "os": {"id": "steamos", "version_id": "3.7.0"}, "arch": "x86_64"}

    async def send(self, msg_type: str, body: dict[str, Any]) -> str:
        self.seq += 1
        env = {"v": 1, "type": msg_type, "id": ulid(), "ts": now(), "seq": self.seq, "body": body}
        errs = list(VALIDATOR.iter_errors(env))
        assert not errs, errs[0].message
        await self.ws.send_bytes(self.session.seal(json.dumps(env).encode()))
        return env["id"]

    async def recv(self, timeout: float = 5) -> dict[str, Any]:
        msg = await self.ws.receive(timeout=timeout)
        if msg.type != WSMsgType.BINARY:
            raise ConnectionResetError(f"{msg.type} {msg.data} {msg.extra}")
        env = json.loads(self.session.open(msg.data))
        errs = list(VALIDATOR.iter_errors(env))
        assert not errs, errs[0].message
        return env

    async def _handshake(self, client, path: str, pattern: str, prologue: bytes, remote: bytes | None = None) -> None:
        self.ws = await client.ws_connect(path)
        hs = noise.Handshake(pattern, True, prologue, self.static, remote_static=remote)
        while not hs.done:
            if hs.my_turn:
                await self.ws.send_bytes(hs.write_message())
            else:
                msg = await self.ws.receive(timeout=5)
                hs.read_message(msg.data)
        self.session = hs.session()
        self.seq = 0

    async def start_pairing(self, client) -> str:
        await self._handshake(client, "/api/deckbeacon/v1/pair", "XX", noise.PROLOGUE_PAIR)
        await self.send("pair_request", {"agent_version": "0.1.0", "protocol": {"min": 1, "max": 1}, "device": self.device(), "name": self.name})
        self.sas = noise.sas(self.session.handshake_hash)
        self.server_static = self.session.remote_static
        return self.sas

    async def confirm(self, accepted: bool = True) -> None:
        await self.send("pair_confirm", {"accepted": accepted})

    async def connect(self, client, transport: str = "local", resume: int = 0, via: str | None = None) -> dict[str, Any]:
        assert self.server_static is not None
        await self._handshake(client, "/api/deckbeacon/v1/ws", "IK", noise.PROLOGUE_SESSION, self.server_static)
        await self.send(
            "hello",
            {
                "agent_version": "0.1.0",
                "protocol": {"min": 1, "max": 1},
                "device": self.device(),
                "boot_id": "0" * 32,
                "session": {"mode": "gaming", "transport": transport, "resume_after_event_seq": resume} | ({"via": via} if via else {}),
            },
        )
        return await self.recv()

    async def telemetry(self, metrics: list[dict[str, Any]]) -> None:
        await self.send("telemetry", {"metrics": [{"sampled_at": now(), "source": "sim", **m} for m in metrics]})

    async def event(self, event_type: str, data: dict[str, Any], live: bool = True, seq: int | None = None) -> dict[str, Any]:
        if seq is None:
            self.event_seq += 1
            seq = self.event_seq
        body = {"event_id": ulid(), "event_seq": seq, "event_type": event_type, "occurred_at": now(), "live": live, "data": data}
        await self.send("event", body)
        return body

    async def close(self) -> None:
        if self.ws is not None and not self.ws.closed:
            await self.ws.close()
