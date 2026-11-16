"""Byte-for-byte interop with the Go agent's Noise implementation (M2-AC1)."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from custom_components.deckbeacon import noise

VECTORS = json.loads(
    (Path(__file__).resolve().parents[3] / "packages" / "protocol" / "vectors" / "noise.json").read_text()
)


def _h(x: str) -> bytes:
    return bytes.fromhex(x)


def _peers(v: dict) -> tuple[noise.Handshake, noise.Handshake]:
    i_static, r_static = noise.KeyPair(_h(v["init_static_private"])), noise.KeyPair(_h(v["resp_static_private"]))
    assert i_static.public.hex() == v["init_static_public"]
    assert r_static.public.hex() == v["resp_static_public"]
    prologue = _h(v["prologue"])
    ini = noise.Handshake(
        v["pattern"],
        True,
        prologue,
        i_static,
        remote_static=r_static.public if v["pattern"] == "IK" else None,
        ephemeral_factory=lambda: noise.KeyPair(_h(v["init_ephemeral_private"])),
    )
    res = noise.Handshake(
        v["pattern"],
        False,
        prologue,
        r_static,
        ephemeral_factory=lambda: noise.KeyPair(_h(v["resp_ephemeral_private"])),
    )
    return ini, res


@pytest.mark.parametrize("v", VECTORS, ids=lambda v: v["name"])
def test_vector(v: dict) -> None:
    ini, res = _peers(v)
    for msg in v["handshake"]:
        writer, reader = (ini, res) if ini.my_turn else (res, ini)
        ct = writer.write_message(_h(msg["payload"]))
        assert ct.hex() == msg["ciphertext"]
        assert reader.read_message(ct).hex() == msg["payload"]
    si, sr = ini.session(), res.session()
    assert si.handshake_hash.hex() == v["handshake_hash"] == sr.handshake_hash.hex()
    assert noise.sas(si.handshake_hash) == v["sas"]
    for t in v["transport"]:
        sender, receiver = (si, sr) if t["from_initiator"] else (sr, si)
        ct = sender.seal(_h(t["payload"]))
        assert ct.hex() == t["ciphertext"]
        assert receiver.open(ct).hex() == t["payload"]


@pytest.mark.parametrize("v", VECTORS, ids=lambda v: v["name"])
def test_python_responder_reads_go_initiator_messages(v: dict) -> None:
    """Python as responder consumes the Go-generated initiator messages directly."""
    _, res = _peers(v)
    for idx, msg in enumerate(v["handshake"]):
        if idx % 2 == 0:
            assert res.read_message(_h(msg["ciphertext"])).hex() == msg["payload"]
        else:
            assert res.write_message(_h(msg["payload"])).hex() == msg["ciphertext"]
    assert res.session().handshake_hash.hex() == v["handshake_hash"]


def test_tamper_replay_and_wrong_key() -> None:
    v = next(x for x in VECTORS if x["pattern"] == "IK")
    ini, res = _peers(v)
    res.read_message(ini.write_message())
    ini.read_message(res.write_message())
    si, sr = ini.session(), res.session()
    ct = si.seal(b"hello")
    bad = bytes([ct[0] ^ 1]) + ct[1:]
    with pytest.raises(noise.NoiseError):
        sr.open(bad)
    assert sr.open(ct) == b"hello"
    with pytest.raises(noise.NoiseError):
        sr.open(ct)

    agent, server, impostor = noise.KeyPair.generate(), noise.KeyPair.generate(), noise.KeyPair.generate()
    ini = noise.Handshake("IK", True, noise.PROLOGUE_SESSION, agent, remote_static=impostor.public)
    res = noise.Handshake("IK", False, noise.PROLOGUE_SESSION, server)
    with pytest.raises(noise.NoiseError):
        res.read_message(ini.write_message())


def test_turns_and_bounds() -> None:
    hs = noise.Handshake("XX", True, noise.PROLOGUE_PAIR, noise.KeyPair.generate())
    with pytest.raises(noise.NoiseError):
        hs.read_message(b"")
    res = noise.Handshake("XX", False, noise.PROLOGUE_PAIR, noise.KeyPair.generate())
    with pytest.raises(noise.NoiseError):
        res.read_message(b"short")
    with pytest.raises(noise.NoiseError):
        res.read_message(b"x" * (noise.MAX_MESSAGE + 1))
    with pytest.raises(ValueError, match="responder static"):
        noise.Handshake("IK", True, noise.PROLOGUE_SESSION, noise.KeyPair.generate())
