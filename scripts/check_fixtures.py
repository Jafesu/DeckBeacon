#!/usr/bin/env python3
"""Structural checks on protocol fixtures (stdlib only).

- every fixture has description/schema/message
- every invalid and receiver fixture declares error_path (a JSON Pointer
  string, where a strict sender schema fails); valid ones do not
- fixture names are unique across valid/, invalid/ and receiver/
"""

from __future__ import annotations

import json
import sys
from pathlib import Path


def main(root: Path) -> int:
    problems: list[str] = []
    seen: set[str] = set()
    counts: dict[str, int] = {}
    for kind in ("valid", "invalid", "receiver"):
        files = sorted((root / kind).glob("*.json"))
        counts[kind] = len(files)
        if not files:
            problems.append(f"{kind}: no fixtures")
        for path in files:
            if path.stem in seen:
                problems.append(f"{path}: duplicate fixture name")
            seen.add(path.stem)
            data = json.loads(path.read_text())
            for key in ("description", "schema", "message"):
                if key not in data:
                    problems.append(f"{path}: missing {key}")
            if kind in ("invalid", "receiver"):
                ptr = data.get("error_path")
                if not isinstance(ptr, str) or (ptr and not ptr.startswith("/")):
                    problems.append(f"{path}: error_path must be a JSON Pointer")
            elif "error_path" in data:
                problems.append(f"{path}: valid fixture must not declare error_path")
    for problem in problems:
        print(problem)
    print(f"{counts.get('valid', 0)} valid, {counts.get('invalid', 0)} invalid, {counts.get('receiver', 0)} receiver, {len(problems)} problems")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main(Path(sys.argv[1])))
