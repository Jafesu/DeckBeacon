#!/usr/bin/env python3
"""Vendor the protocol schemas into the HA integration (it ships standalone).

    python3 scripts/sync_schemas.py          # copy
    python3 scripts/sync_schemas.py --check  # CI: fail if the copy drifted
"""

from __future__ import annotations

import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SRC = ROOT / "packages" / "protocol" / "schemas" / "v1"
DST = ROOT / "apps" / "homeassistant" / "custom_components" / "deckbeacon" / "schemas"


def main(check: bool) -> int:
    src = {p.name: p.read_bytes() for p in SRC.glob("*.schema.json")}
    dst = {p.name: p.read_bytes() for p in DST.glob("*.schema.json")} if DST.exists() else {}
    if check:
        if src != dst:
            print("vendored schemas differ from packages/protocol; run scripts/sync_schemas.py")
            return 1
        print(f"{len(src)} schemas in sync")
        return 0
    DST.mkdir(parents=True, exist_ok=True)
    for name in set(dst) - set(src):
        (DST / name).unlink()
    for name, data in src.items():
        (DST / name).write_bytes(data)
    print(f"copied {len(src)} schemas")
    return 0


if __name__ == "__main__":
    sys.exit(main("--check" in sys.argv))
