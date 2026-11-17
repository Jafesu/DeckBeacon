"""Validate every protocol fixture against the v1 schemas (Python side of AC3)."""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator
from referencing import Registry, Resource

PROTOCOL = Path(__file__).resolve().parents[2]
SCHEMAS = PROTOCOL / "schemas" / "v1"
FIXTURES = PROTOCOL / "fixtures"


def _open_objects(node) -> None:
    """Receiver schemas: unknown fields allowed except in x-receiver: closed objects."""
    if isinstance(node, dict):
        if node.get("additionalProperties") is False and node.get("x-receiver") != "closed":
            del node["additionalProperties"]
        for child in node.values():
            _open_objects(child)
    elif isinstance(node, list):
        for child in node:
            _open_objects(child)


def _load(path: Path, receiver: bool) -> dict:
    schema = json.loads(path.read_text())
    if receiver:
        _open_objects(schema)
    return schema


def _registry(receiver: bool = False) -> Registry:
    resources = []
    for path in sorted(SCHEMAS.glob("*.schema.json")):
        schema = _load(path, receiver)
        resources.append((schema["$id"], Resource.from_contents(schema)))
    return Registry().with_resources(resources)


REGISTRY = _registry()
RECEIVER_REGISTRY = _registry(receiver=True)


def _validator(name: str, receiver: bool = False) -> Draft202012Validator:
    schema = _load(SCHEMAS / f"{name}.schema.json", receiver)
    registry = RECEIVER_REGISTRY if receiver else REGISTRY
    return Draft202012Validator(schema, registry=registry, format_checker=Draft202012Validator.FORMAT_CHECKER)


def _fixtures(kind: str) -> list[Path]:
    files = sorted((FIXTURES / kind).glob("*.json"))
    assert files, f"no {kind} fixtures found"
    return files


def test_all_schemas_are_valid_2020_12() -> None:
    for path in SCHEMAS.glob("*.schema.json"):
        Draft202012Validator.check_schema(json.loads(path.read_text()))


@pytest.mark.parametrize("path", _fixtures("valid"), ids=lambda p: p.stem)
def test_valid_fixture(path: Path) -> None:
    fixture = json.loads(path.read_text())
    errors = list(_validator(fixture["schema"]).iter_errors(fixture["message"]))
    assert not errors, [e.message for e in errors]


@pytest.mark.parametrize("path", _fixtures("invalid"), ids=lambda p: p.stem)
def test_invalid_fixture(path: Path) -> None:
    fixture = json.loads(path.read_text())
    errors = list(_validator(fixture["schema"]).iter_errors(fixture["message"]))
    assert errors, fixture["description"]
    # Each invalid fixture must fail for exactly the reason it documents: the failing
    # instance location is pinned as a JSON Pointer and asserted in Go as well.
    paths = {_pointer(leaf) for error in errors for leaf in _leaves(error)}
    assert paths == {fixture["error_path"]}, (fixture["description"], paths)


@pytest.mark.parametrize("path", _fixtures("receiver"), ids=lambda p: p.stem)
def test_receiver_fixture(path: Path) -> None:
    """A newer minor version's message: a sender of this version may not emit
    it, but a receiver must accept it."""
    fixture = json.loads(path.read_text())
    errors = list(_validator(fixture["schema"]).iter_errors(fixture["message"]))
    paths = {_pointer(leaf) for error in errors for leaf in _leaves(error)}
    assert paths == {fixture["error_path"]}, (fixture["description"], paths)
    errors = list(_validator(fixture["schema"], receiver=True).iter_errors(fixture["message"]))
    assert not errors, (fixture["description"], [e.message for e in errors])


@pytest.mark.parametrize("path", _fixtures("invalid"), ids=lambda p: p.stem)
def test_invalid_fixture_still_invalid_for_receivers(path: Path) -> None:
    fixture = json.loads(path.read_text())
    receiver = _validator(fixture["schema"], receiver=True)
    assert any(True for _ in receiver.iter_errors(fixture["message"])), fixture["description"]


def _leaves(error):
    if not error.context:
        return [error]
    return [leaf for child in error.context for leaf in _leaves(child)]


def _pointer(error) -> str:
    return "".join(f"/{part}" for part in error.absolute_path)
