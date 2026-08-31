#!/usr/bin/env python3
"""Classify whether a Go checkout change requires the cross-repository golden gate."""

from __future__ import annotations

import argparse
import json
import pathlib
import sys


CROSS_REPO_PATHS = frozenset(
    {
        ".github/workflows/usdb-fast.yml",
        "internal/usdb/btc_activation_golden.json",
        "internal/usdb/cross_chain_release_manifest.json",
        "scripts/usdb/ci-revisions.json",
        "scripts/usdb/ci_change_scope.py",
        "scripts/usdb/ci_revisions.py",
        "scripts/usdb/run_fast_ci.sh",
    }
)


def classify(paths: list[str], *, force: bool = False) -> dict[str, object]:
    normalized = sorted(set(paths))
    matched = sorted(CROSS_REPO_PATHS.intersection(normalized))
    required = force or bool(matched)
    if force:
        reason = "forced_by_manual_or_reusable_release_gate"
    elif matched:
        reason = "cross_repository_contract_changed"
    else:
        reason = "no_cross_repository_contract_change"
    return {
        "changed_paths": normalized,
        "matched_paths": matched,
        "reason": reason,
        "required": required,
    }


def write_github_output(path: pathlib.Path, result: dict[str, object]) -> None:
    with path.open("a", encoding="utf-8") as output:
        output.write(f"required={str(result['required']).lower()}\n")
        output.write(f"reason={result['reason']}\n")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("paths", nargs="*", help="repository-relative changed paths")
    parser.add_argument("--force", action="store_true")
    parser.add_argument("--github-output", type=pathlib.Path)
    return parser


def main() -> int:
    args = build_parser().parse_args()
    result = classify(args.paths, force=args.force)
    if args.github_output is not None:
        write_github_output(args.github_output, result)
    print(json.dumps(result, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
