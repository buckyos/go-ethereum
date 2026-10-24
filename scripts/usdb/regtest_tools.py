#!/usr/bin/env python3
"""Bind regtest artifacts to the locked USDB release inputs and executable bytes."""

from __future__ import annotations

import argparse
import ast
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import tomllib

from ci_revisions import load_lock


FILES = (
    "ord/ord",
    "bitcoin/bin/bitcoind",
    "bitcoin/bin/bitcoin-cli",
    "bitcoin/core-artifact-provenance.json",
)


def sha256(path: Path) -> str:
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def constants(path: Path, names: tuple[str, ...]) -> dict:
    """Read literal release constants without importing executable repository code."""
    values = {}
    for node in ast.parse(path.read_text()).body:
        if isinstance(node, ast.Assign):
            for target in node.targets:
                if isinstance(target, ast.Name) and target.id in names:
                    if target.id in values:
                        raise ValueError(f"Duplicate release constant: {target.id}")
                    values[target.id] = ast.literal_eval(node.value)
    if set(values) != set(names):
        raise ValueError(f"Missing release constants in {path}")
    return values


def require_match(pattern: str, text: str, expected: str, label: str) -> None:
    if re.findall(pattern, text, re.MULTILINE) != [expected]:
        raise ValueError(f"Release {label} does not match {expected}")


def release_inputs(usdb: Path, lock_path: Path) -> dict:
    """Resolve one exact checkout and reject drift between its release definitions."""
    lock = load_lock(lock_path)
    revision = lock["dependencies"]["usdb"]["revision"]
    head = subprocess.check_output(["git", "-C", str(usdb), "rev-parse", "HEAD"], text=True).strip()
    if head != revision:
        raise ValueError(f"USDB revision mismatch: expected {revision}, have {head}")
    changed = subprocess.check_output(
        ["git", "-C", str(usdb), "status", "--porcelain", "--", "docker", ".dockerignore"], text=True
    )
    if changed:
        raise ValueError("USDB release build inputs must be clean; commit changes and update the CI lock first")
    ord_pin = constants(usdb / "docker/scripts/tools/ord_release.py", ("VERSION", "REVISION", "INDEX_SCHEMA"))
    core_pin = constants(usdb / "docker/scripts/tools/bitcoin_release.py", ("CORE_VERSION", "CORE_SHA256"))
    if not re.fullmatch(r"\d+\.\d+\.\d+", ord_pin["VERSION"]) or not re.fullmatch(r"[a-f0-9]{40}", ord_pin["REVISION"]):
        raise ValueError("Invalid Ord release version or revision")
    lock_file = usdb / f"docker/locks/ord-{ord_pin['VERSION']}.Cargo.lock"
    packages = tomllib.loads(lock_file.read_text())["package"]
    if [p["version"] for p in packages if p["name"] == "ord"] != [ord_pin["VERSION"]]:
        raise ValueError("Reviewed Cargo.lock does not match the Ord release")
    rust = lock["toolchains"]["rust"]
    for name in ("usdb-services", "world-sim-tools"):
        dockerfile = (usdb / f"docker/Dockerfile.{name}").read_text()
        require_match(r"^ARG ORD_VERSION=(\S+)$", dockerfile, ord_pin["VERSION"], "Ord version")
        require_match(r"^ARG ORD_REVISION=(\S+)$", dockerfile, ord_pin["REVISION"], "Ord revision")
        require_match(r"^FROM (\S+) AS ord-builder$", dockerfile, f"rust:{rust}-bookworm", "Ord Rust builder")
        require_match(r"^COPY (docker/locks/\S+) /tmp/ord-release.Cargo.lock$", dockerfile,
                      str(lock_file.relative_to(usdb)), "Ord dependency lock")
    require_match(r"^ARG BITCOIN_VERSION=(\S+)$", (usdb / "docker/Dockerfile.bitcoin-core").read_text(),
                  core_pin["CORE_VERSION"], "Bitcoin version")
    return {
        "schema_version": "usdb-regtest-tools:v2",
        "usdb_revision": revision,
        "ord": {"version": ord_pin["VERSION"], "revision": ord_pin["REVISION"],
                "index_schema": ord_pin["INDEX_SCHEMA"], "rust": rust, "cargo_lock_sha256": sha256(lock_file)},
        "bitcoin_core": {"version": core_pin["CORE_VERSION"], "archive_sha256": core_pin["CORE_SHA256"]},
    }


def check_versions(tools: Path, expected: dict) -> None:
    """Check programs as well as metadata; hashes alone cannot detect a wrong build."""
    core_version = expected["bitcoin_core"]["version"]
    if core_version.count(".") == 1:
        core_version += ".0"
    core_version = re.escape(core_version)
    patterns = {
        "ord/ord": re.escape(f"ord {expected['ord']['version']}"),
        "bitcoin/bin/bitcoind": rf"Bitcoin Core (?:daemon )?version v{core_version}(?: bitcoind)?",
        "bitcoin/bin/bitcoin-cli": rf"Bitcoin Core RPC client version v{core_version}(?: bitcoin-cli)?",
    }
    # Core 31 reads settings even for --version. Keep these probes independent
    # of any operator configuration or data directory on a developer machine.
    with tempfile.TemporaryDirectory(prefix="usdb-tool-version-") as temporary:
        config = Path(temporary) / "bitcoin.conf"
        config.write_text("")
        for name, pattern in patterns.items():
            args = [str((tools / name).resolve())]
            if name == "ord/ord":
                args.extend(["--data-dir", temporary, "--version"])
            else:
                args.extend([f"-datadir={temporary}", f"-conf={config}"])
                if name == "bitcoin/bin/bitcoind":
                    args.append("-nosettings")
                args.append("-version")
            lines = subprocess.check_output(args, text=True, timeout=30).splitlines()
            if not lines or re.fullmatch(pattern, lines[0]) is None:
                raise ValueError(f"Tool version mismatch for {name}: expected {pattern!r}, have {lines[:1]}")


def record(tools: Path, expected: dict) -> None:
    check_versions(tools, expected)
    manifest = {**expected, "files": {name: sha256(tools / name) for name in FILES}}
    (tools / "toolchain-manifest.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")


def verify(tools: Path, expected: dict) -> None:
    """Reject old-baseline artifacts and corrupted files before executing any binary."""
    manifest = json.loads((tools / "toolchain-manifest.json").read_text())
    if not isinstance(manifest, dict):
        raise ValueError("Regtest artifact manifest must be an object")
    metadata = {key: value for key, value in manifest.items() if key != "files"}
    if metadata != expected:
        raise ValueError("Regtest artifact does not match the locked USDB release inputs")
    if manifest.get("files") != {name: sha256(tools / name) for name in FILES}:
        raise ValueError("Regtest artifact file hashes do not match its manifest")
    check_versions(tools, expected)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("describe", "record", "verify"))
    parser.add_argument("--usdb-repo", type=Path, required=True)
    parser.add_argument("--lock", type=Path, default=Path(__file__).with_name("ci-revisions.json"))
    parser.add_argument("--tools", type=Path)
    args = parser.parse_args()
    if args.command != "describe" and args.tools is None:
        parser.error("--tools is required for record and verify")
    try:
        expected = release_inputs(args.usdb_repo.resolve(), args.lock)
        if args.command == "describe":
            print(json.dumps(expected, indent=2, sort_keys=True))
        else:
            {"record": record, "verify": verify}[args.command](args.tools, expected)
            print(f"[usdb-regtest-tools] {args.command}: Ord {expected['ord']['version']}, "
                  f"Bitcoin Core {expected['bitcoin_core']['version']}, USDB {expected['usdb_revision']}")
    except (OSError, ValueError, KeyError, TypeError, SyntaxError, subprocess.SubprocessError) as error:
        print(f"Regtest tool error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
