#!/usr/bin/env python3
"""Require critical tests to run and pass in a real go test -json report."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Iterable


REQUIRED_TESTS = Path(__file__).with_name("fast_go_required_tests.json")


def check_coverage(required: dict[str, list[str]], lines: Iterable[str]) -> int:
    if not isinstance(required, dict) or not required:
        raise ValueError("required test manifest must contain at least one package")
    expected = set()
    for package, names in required.items():
        if (
            not isinstance(package, str) or not package
            or not isinstance(names, list) or not names
            or any(not isinstance(name, str) or not name.startswith("Test") or "/" in name for name in names)
            or len(names) != len(set(names))
        ):
            raise ValueError(f"invalid required test list for {package!r}")
        expected.update((package, name) for name in names)

    started = set()
    passed = set()
    rejected = set()
    packages_passed = set()
    packages_rejected = set()
    for line_number, line in enumerate(lines, 1):
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except ValueError as exc:
            raise ValueError(f"invalid Go JSON event at line {line_number}") from exc
        if not isinstance(event, dict):
            raise ValueError(f"invalid Go event object at line {line_number}")
        package, name, action = event.get("Package"), event.get("Test"), event.get("Action")
        if not isinstance(package, str) or (name is not None and not isinstance(name, str)):
            raise ValueError(f"invalid Go package/test identity at line {line_number}")
        if name is None:
            if action == "pass":
                packages_passed.add(package)
            elif action in ("fail", "skip"):
                packages_rejected.add(package)
        elif (package, name) in expected:
            identity = (package, name)
            if action == "run":
                started.add(identity)
            elif action == "pass" and identity in started:
                passed.add(identity)
            elif action in ("fail", "skip"):
                rejected.add(identity)

    missing = expected - (passed - rejected)
    incomplete_packages = set(required) - (packages_passed - packages_rejected)
    if missing or incomplete_packages:
        raise ValueError(
            "critical Go coverage missing or unsuccessful: "
            f"tests={['/'.join(identity) for identity in sorted(missing)]}, "
            f"packages={sorted(incomplete_packages)}"
        )
    return len(expected)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", type=Path, required=True)
    parser.add_argument("--required", type=Path, default=REQUIRED_TESTS)
    args = parser.parse_args()
    try:
        required = json.loads(args.required.read_text(encoding="utf-8"))
        with args.report.open(encoding="utf-8") as report:
            count = check_coverage(required, report)
    except (OSError, ValueError) as exc:
        parser.exit(1, f"[usdb-fast-go-coverage] {exc}\n")
    print(f"[usdb-fast-go-coverage] {count} critical tests ran and passed: {args.report}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
