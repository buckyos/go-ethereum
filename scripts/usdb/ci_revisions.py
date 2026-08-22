#!/usr/bin/env python3
"""Validate and expose the pinned USDB cross-repository CI baseline."""

from __future__ import annotations

import argparse
import json
import pathlib
import re
import subprocess
import sys
from typing import Any


SCHEMA_VERSION = "usdb-ci-revisions:v1"
REPOSITORY_KEYS = ("go_ethereum", "usdb", "source_dao")
EXPECTED_REPOSITORIES = {
    "go_ethereum": ("buckyos/go-ethereum", "go-ethereum"),
    "usdb": ("buckyos/usdb", "usdb"),
    "source_dao": ("buckyos/SourceDAO", "SourceDAO"),
}
TOOLCHAIN_KEYS = (
    "runner",
    "go_release",
    "go_compatibility",
    "python",
    "rust",
    "node",
    "npm",
)
GIT_REVISION_RE = re.compile(r"^[0-9a-f]{40}$")
VERSION_RE = re.compile(r"^[0-9]+\.[0-9]+(?:\.[0-9]+)?$")


def _object_without_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def _reject_constant(value: str) -> None:
    raise ValueError(f"non-finite JSON number is not allowed: {value}")


def _require_exact_keys(value: dict[str, Any], expected: set[str], context: str) -> None:
    actual = set(value)
    if actual != expected:
        missing = sorted(expected - actual)
        unknown = sorted(actual - expected)
        raise ValueError(f"{context} keys mismatch: missing={missing}, unknown={unknown}")


def load_lock(path: pathlib.Path) -> dict[str, Any]:
    data = json.loads(
        path.read_text(encoding="utf-8"),
        object_pairs_hook=_object_without_duplicates,
        parse_constant=_reject_constant,
    )
    if not isinstance(data, dict):
        raise ValueError("CI revision lock must be a JSON object")
    _require_exact_keys(
        data,
        {"schema_version", "coordinator", "repositories", "toolchains"},
        "top-level",
    )
    if data["schema_version"] != SCHEMA_VERSION:
        raise ValueError(f"unsupported schema_version: {data['schema_version']!r}")
    if data["coordinator"] != "go_ethereum":
        raise ValueError("coordinator must be go_ethereum")

    repositories = data["repositories"]
    if not isinstance(repositories, dict):
        raise ValueError("repositories must be an object")
    _require_exact_keys(repositories, set(REPOSITORY_KEYS), "repositories")
    for name in REPOSITORY_KEYS:
        entry = repositories[name]
        if not isinstance(entry, dict):
            raise ValueError(f"repositories.{name} must be an object")
        _require_exact_keys(entry, {"repository", "directory", "revision"}, name)
        expected_repository, expected_directory = EXPECTED_REPOSITORIES[name]
        if entry["repository"] != expected_repository:
            raise ValueError(
                f"repositories.{name}.repository must be {expected_repository!r}"
            )
        if entry["directory"] != expected_directory:
            raise ValueError(
                f"repositories.{name}.directory must be {expected_directory!r}"
            )
        if not isinstance(entry["revision"], str) or not GIT_REVISION_RE.fullmatch(
            entry["revision"]
        ):
            raise ValueError(f"repositories.{name}.revision must be a full lowercase SHA")

    toolchains = data["toolchains"]
    if not isinstance(toolchains, dict):
        raise ValueError("toolchains must be an object")
    _require_exact_keys(toolchains, set(TOOLCHAIN_KEYS), "toolchains")
    if toolchains["runner"] != "ubuntu-24.04":
        raise ValueError("toolchains.runner must be ubuntu-24.04")
    for name in TOOLCHAIN_KEYS[1:]:
        value = toolchains[name]
        if not isinstance(value, str) or not VERSION_RE.fullmatch(value):
            raise ValueError(f"toolchains.{name} must be a fixed numeric version")
    return data


def github_outputs(lock: dict[str, Any]) -> list[str]:
    outputs: list[str] = []
    for name in REPOSITORY_KEYS:
        entry = lock["repositories"][name]
        outputs.extend(
            (
                f"{name}_repository={entry['repository']}",
                f"{name}_revision={entry['revision']}",
            )
        )
    for name in TOOLCHAIN_KEYS:
        outputs.append(f"{name}_version={lock['toolchains'][name]}")
    return outputs


def _git_head(path: pathlib.Path) -> str:
    if not path.is_dir():
        raise ValueError(f"repository checkout is missing: {path}")
    completed = subprocess.run(
        ["git", "-C", str(path), "rev-parse", "HEAD"],
        check=True,
        capture_output=True,
        text=True,
    )
    return completed.stdout.strip()


def verify_worktrees(
    lock: dict[str, Any], workspace_root: pathlib.Path, current_repository: str | None
) -> None:
    if current_repository is not None and current_repository not in REPOSITORY_KEYS:
        raise ValueError(f"unsupported current repository: {current_repository}")
    for name in REPOSITORY_KEYS:
        entry = lock["repositories"][name]
        path = workspace_root / entry["directory"]
        head = _git_head(path)
        if name == current_repository:
            print(
                f"current checkout {name}: head={head}, "
                f"validated_baseline={entry['revision']}"
            )
            continue
        if head != entry["revision"]:
            raise ValueError(
                f"{name} revision mismatch: expected {entry['revision']}, have {head}"
            )
        print(f"verified {name}: {head}")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--lock",
        type=pathlib.Path,
        default=pathlib.Path(__file__).with_name("ci-revisions.json"),
    )
    subparsers = parser.add_subparsers(dest="command", required=True)
    subparsers.add_parser("validate", help="validate the lock schema and values")
    subparsers.add_parser("github-output", help="emit values for GITHUB_OUTPUT")
    verify = subparsers.add_parser("verify", help="verify checked-out repository heads")
    verify.add_argument("--workspace-root", type=pathlib.Path, required=True)
    verify.add_argument("--current-repository", choices=REPOSITORY_KEYS)
    return parser


def main() -> int:
    args = build_parser().parse_args()
    try:
        lock = load_lock(args.lock)
        if args.command == "github-output":
            print("\n".join(github_outputs(lock)))
        elif args.command == "verify":
            verify_worktrees(lock, args.workspace_root, args.current_repository)
        else:
            print(f"validated {args.lock}")
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"CI revision lock error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
