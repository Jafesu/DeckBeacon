"""End-to-end (M2 WP7): the real Go agent binary against the real integration.

HA runs in the test harness with a real TCP listener; the agent is the compiled
`deckbeacon` binary reading the synthetic SteamOS trees in apps/agent/testdata.
Skipped when Go is unavailable and DECKBEACON_AGENT_BIN is not set.
"""

from __future__ import annotations

import asyncio
import json
import os
import re
import shutil
import subprocess
from pathlib import Path

import pytest
from homeassistant.const import STATE_UNAVAILABLE
from homeassistant.core import HomeAssistant
from homeassistant.data_entry_flow import FlowResultType
from homeassistant.helpers import entity_registry as er

from custom_components.deckbeacon.const import DOMAIN, EVENT_DEVICE

from .helpers import advance, get_device

REPO = Path(__file__).resolve().parents[3]
ROOTS = REPO / "apps" / "agent" / "testdata" / "roots"


@pytest.fixture(scope="module")
def agent_bin(tmp_path_factory) -> str:
    if path := os.environ.get("DECKBEACON_AGENT_BIN"):
        return path
    if shutil.which("go") is None:
        pytest.skip("Go toolchain not available and DECKBEACON_AGENT_BIN not set")
    out = tmp_path_factory.mktemp("bin") / "deckbeacon"
    subprocess.run(["go", "build", "-o", str(out), "./cmd/deckbeacon"], cwd=REPO / "apps" / "agent", check=True)  # noqa: S603, S607
    return str(out)


class Agent:
    def __init__(self, binary: str, home: Path, root: str, name: str) -> None:
        self.binary, self.root, self.name = binary, str(ROOTS / root), name
        self.state = home / "state"
        self.runtime = home / "run"
        self.env = {
            "PATH": os.environ.get("PATH", ""),
            "HOME": str(home),
            "DECKBEACON_STATE_DIR": str(self.state),
            "DECKBEACON_LOGIND_ADDRESS": "none",
            "DECKBEACON_CONFIG_DIR": str(home / "cfg"),
            "XDG_RUNTIME_DIR": str(self.runtime),
        }
        self.proc: asyncio.subprocess.Process | None = None
        self.log = home / "agent.log"

    async def pair(self, url: str, hass: HomeAssistant) -> str:
        result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": "user"})
        flow_id = result["flow_id"]
        proc = await asyncio.create_subprocess_exec(
            self.binary,
            "pair",
            "--url",
            url,
            "--name",
            self.name,
            "--root",
            self.root,
            stdin=asyncio.subprocess.PIPE,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.STDOUT,
            env=self.env,
        )
        buf = b""
        while b"[y/N]" not in buf:
            chunk = await asyncio.wait_for(proc.stdout.read(256), 15)
            assert chunk, buf.decode()
            buf += chunk
        agent_code = re.search(rb"Pairing code: (\d{3}) (\d{3})", buf)
        assert agent_code, buf
        menu = await advance(hass, flow_id)
        assert menu["type"] is FlowResultType.MENU, menu
        assert menu["description_placeholders"]["code"] == (agent_code.group(1) + b" " + agent_code.group(2)).decode()
        result = await hass.config_entries.flow.async_configure(flow_id, {"next_step_id": "accept"})
        proc.stdin.write(b"y\n")
        await proc.stdin.drain()
        result = await advance(hass, flow_id)
        if result is not None and result["type"] is FlowResultType.SHOW_PROGRESS_DONE:
            result = await advance(hass, flow_id)
        assert result is not None and result["type"] is FlowResultType.CREATE_ENTRY, result
        out, _ = await asyncio.wait_for(proc.communicate(), 20)
        assert proc.returncode == 0, (buf + out).decode()
        return result["result"].entry_id

    async def start(self) -> None:
        self.proc = await asyncio.create_subprocess_exec(
            self.binary,
            "run",
            "--telemetry-interval",
            "1s",
            "--log-level",
            "debug",
            "--root",
            self.root,
            stdout=asyncio.subprocess.DEVNULL,
            stderr=self.log.open("wb"),
            env=self.env,
        )

    def status(self) -> dict:
        try:
            return json.loads((self.runtime / "deckbeacon" / "status.json").read_text())
        except (OSError, ValueError):
            return {}

    def device_id(self) -> str:
        out = subprocess.run([self.binary, "status", "--json"], env=self.env, capture_output=True, check=True)  # noqa: S603
        return json.loads(out.stdout)["device_id"]

    async def stop(self) -> None:
        if self.proc and self.proc.returncode is None:
            self.proc.terminate()
            try:
                await asyncio.wait_for(self.proc.wait(), 10)
            except TimeoutError:
                self.proc.kill()
                await self.proc.wait()


async def until(hass: HomeAssistant, what: str, cond, timeout: float = 20) -> None:
    loop = asyncio.get_running_loop()
    end = loop.time() + timeout
    while loop.time() < end:
        await hass.async_block_till_done()
        if cond():
            return
        await asyncio.sleep(0.1)
    raise AssertionError(f"timeout waiting for {what}")


def state(hass: HomeAssistant, device_id: str, key: str, domain: str = "sensor") -> str | None:
    eid = er.async_get(hass).async_get_entity_id(domain, DOMAIN, f"{device_id}_{key}")
    s = hass.states.get(eid) if eid else None
    return s.state if s else None


async def test_go_agent_end_to_end(hass: HomeAssistant, client, agent_bin, tmp_path) -> None:
    url = str(client.make_url("")).rstrip("/")
    events: list[dict] = []
    hass.bus.async_listen(EVENT_DEVICE, lambda e: events.append(e.data))
    deck = Agent(agent_bin, tmp_path / "deck", "steamdeck-oled", "steamdeck")
    g15 = Agent(agent_bin, tmp_path / "g15", "dell-g15", "dell-g15")
    try:
        # Pairing with SAS confirmed on both sides (AC2), two devices (AC5).
        deck_entry = await deck.pair(url, hass)
        g15_entry = await g15.pair(url, hass)
        deck_id, g15_id = deck.device_id(), g15.device_id()
        await deck.start()
        await g15.start()

        await until(hass, "deck telemetry", lambda: state(hass, deck_id, "battery_level") == "76")
        # The synthetic /proc/stat never changes, so CPU usage has no delta: it must stay
        # unavailable ("warming up") rather than show a fabricated number.
        assert state(hass, deck_id, "cpu_usage") == STATE_UNAVAILABLE
        assert state(hass, deck_id, "thermal_cpu_c") == "64.3"
        assert state(hass, deck_id, "gpu_busy") == "42"
        assert state(hass, deck_id, "session_mode") == "gaming"
        assert state(hass, deck_id, "connection") == "local"
        assert state(hass, deck_id, "power_state") == "on"
        assert state(hass, deck_id, "power_ac_online", "binary_sensor") == "off"
        caps = {c["id"]: c["status"] for c in hass.data[DOMAIN].runtimes[deck_entry].capabilities}
        assert caps["lifecycle.sleep_notify"] == "unavailable"  # logind disabled for the test
        assert caps["lifecycle.resume_detect"] == "available" and caps["service.start_at_boot"] == "disabled"
        await until(hass, "g15 telemetry", lambda: state(hass, g15_id, "thermal_cpu_c") == "71")
        assert state(hass, g15_id, "gpu_busy") == STATE_UNAVAILABLE  # no NVIDIA tool: honest unavailable
        assert state(hass, g15_id, "session_mode") == "desktop"
        assert state(hass, g15_id, "fan_rpm") == "2650"

        deck_dev, g15_dev = get_device(hass, deck_entry), get_device(hass, g15_entry)
        assert (DOMAIN, deck_id) in deck_dev.identifiers and (DOMAIN, g15_id) in g15_dev.identifiers
        assert deck_dev.model == "Steam Deck OLED" and deck_dev.manufacturer == "Valve"
        assert g15_dev.model == "Dell G15 5520" and g15_dev.manufacturer == "Dell Inc."
        assert deck.status()["state"] == "connected_local"

        # Reload (as on HA restart): agents reconnect by themselves, no re-pairing (AC5).
        assert await hass.config_entries.async_reload(deck_entry)
        await until(hass, "deck back", lambda: state(hass, deck_id, "connection") == "local", timeout=30)

        # Forced drop from the server side: reconnect + resume (AC6).
        hub = hass.data[DOMAIN]
        await hub.runtimes[deck_entry].channel.ws.close(code=1011)
        await until(hass, "deck reconnected", lambda: state(hass, deck_id, "connection") == "local" and hub.runtimes[deck_entry].connected, timeout=30)

        # Each connection produced exactly one connection_changed event, never duplicated.
        await until(hass, "events", lambda: sum(1 for e in events if e["device_id"] == deck_id) >= 3)
        deck_seqs = [e["event_seq"] for e in events if e["device_id"] == deck_id]
        assert len(deck_seqs) == len(set(deck_seqs)), deck_seqs
        boots = [e for e in events if e["device_id"] == deck_id and e["event_type"] == "device_booted"]
        assert len(boots) == 1, boots  # once per boot_id, not per connection (M3-AC5)

        # Revocation (AC8): delete the device in HA → agent goes to revoked and stays idle.
        await hass.config_entries.async_remove(deck_entry)
        await until(hass, "deck revoked", lambda: deck.status().get("state") == "revoked", timeout=30)
        assert deck.proc.returncode is None  # idle, not crash-looping
        assert json.loads((deck.state / "trust.json").read_text())["revoked"] is True
        assert state(hass, g15_id, "connection") == "local"  # other device unaffected

        # No HA access token anywhere on the device (AC3).
        for f in (tmp_path / "deck").rglob("*"):
            if f.is_file():
                data = f.read_bytes()
                assert b"eyJ" not in data, f"JWT-like token in {f}"
                assert b"access_token" not in data and b"refresh_token" not in data, f
    except AssertionError:
        for a in (deck, g15):
            if a.log.exists():
                print(f"--- {a.name} log ---\n{a.log.read_text()[-3000:]}")  # noqa: T201
        raise
    finally:
        await deck.stop()
        await g15.stop()
