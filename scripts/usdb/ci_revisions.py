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


SCHEMA_VERSION = "usdb-ci-revisions:v2"
DEPENDENCY_KEYS = ("usdb", "source_dao")
EXPECTED_COORDINATOR = ("buckyos/go-ethereum", "go-ethereum")
EXPECTED_DEPENDENCIES = {
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


def _require_repository_entry(
    entry: Any,
    *,
    context: str,
    expected_repository: str,
    expected_directory: str,
    require_revision: bool,
) -> None:
    if not isinstance(entry, dict):
        raise ValueError(f"{context} must be an object")
    expected_keys = {"repository", "directory"}
    if require_revision:
        expected_keys.add("revision")
    _require_exact_keys(entry, expected_keys, context)
    if entry["repository"] != expected_repository:
        raise ValueError(f"{context}.repository must be {expected_repository!r}")
    if entry["directory"] != expected_directory:
        raise ValueError(f"{context}.directory must be {expected_directory!r}")
    if require_revision and (
        not isinstance(entry["revision"], str)
        or not GIT_REVISION_RE.fullmatch(entry["revision"])
    ):
        raise ValueError(f"{context}.revision must be a full lowercase SHA")


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
        {"schema_version", "coordinator", "dependencies", "toolchains"},
        "top-level",
    )
    if data["schema_version"] != SCHEMA_VERSION:
        raise ValueError(f"unsupported schema_version: {data['schema_version']!r}")
    _require_repository_entry(
        data["coordinator"],
        context="coordinator",
        expected_repository=EXPECTED_COORDINATOR[0],
        expected_directory=EXPECTED_COORDINATOR[1],
        require_revision=False,
    )

    dependencies = data["dependencies"]
    if not isinstance(dependencies, dict):
        raise ValueError("dependencies must be an object")
    _require_exact_keys(dependencies, set(DEPENDENCY_KEYS), "dependencies")
    for name in DEPENDENCY_KEYS:
        expected_repository, expected_directory = EXPECTED_DEPENDENCIES[name]
        _require_repository_entry(
            dependencies[name],
            context=f"dependencies.{name}",
            expected_repository=expected_repository,
            expected_directory=expected_directory,
            require_revision=True,
        )

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
    for name in DEPENDENCY_KEYS:
        entry = lock["dependencies"][name]
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
    lock: dict[str, Any], workspace_root: pathlib.Path
) -> None:
    coordinator = lock["coordinator"]
    coordinator_head = _git_head(workspace_root / coordinator["directory"])
    print(f"current coordinator go_ethereum: {coordinator_head}")
    for name in DEPENDENCY_KEYS:
        entry = lock["dependencies"][name]
        path = workspace_root / entry["directory"]
        head = _git_head(path)
        if head != entry["revision"]:
            raise ValueError(
                f"{name} revision mismatch: expected {entry['revision']}, have {head}"
            )
        print(f"verified {name}: {head}")


def set_dependency_revision(lock: dict[str, Any], name: str, revision: str) -> None:
    if name not in DEPENDENCY_KEYS:
        raise ValueError(f"unsupported dependency: {name}")
    if GIT_REVISION_RE.fullmatch(revision) is None:
        raise ValueError("dependency revision must be a full lowercase SHA")
    lock["dependencies"][name]["revision"] = revision


def write_lock(path: pathlib.Path, lock: dict[str, Any]) -> None:
    path.write_text(json.dumps(lock, indent=2) + "\n", encoding="utf-8")


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
    update = subparsers.add_parser("set-dependency", help="update one pinned dependency")
    update.add_argument("--name", choices=DEPENDENCY_KEYS, required=True)
    update.add_argument("--revision", required=True)
    return parser


def main() -> int:
    args = build_parser().parse_args()
    try:
        lock = load_lock(args.lock)
        if args.command == "github-output":
            print("\n".join(github_outputs(lock)))
        elif args.command == "verify":
            verify_worktrees(lock, args.workspace_root)
        elif args.command == "set-dependency":
            set_dependency_revision(lock, args.name, args.revision)
            write_lock(args.lock, lock)
            print(f"updated {args.name} revision in {args.lock}")
        else:
            print(f"validated {args.lock}")
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"CI revision lock error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
