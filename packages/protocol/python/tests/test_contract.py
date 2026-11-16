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


def _registry() -> Registry:
    resources = []
    for path in sorted(SCHEMAS.glob("*.schema.json")):
        schema = json.loads(path.read_text())
        resources.append((schema["$id"], Resource.from_contents(schema)))
    return Registry().with_resources(resources)


REGISTRY = _registry()


def _validator(name: str) -> Draft202012Validator:
    schema = json.loads((SCHEMAS / f"{name}.schema.json").read_text())
    return Draft202012Validator(schema, registry=REGISTRY, format_checker=Draft202012Validator.FORMAT_CHECKER)


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


def _leaves(error):
    if not error.context:
        return [error]
    return [leaf for child in error.context for leaf in _leaves(child)]


def _pointer(error) -> str:
    return "".join(f"/{part}" for part in error.absolute_path)
