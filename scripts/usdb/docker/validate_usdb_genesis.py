#!/usr/bin/env python3
"""Validate the immutable USDB genesis artifact mounted by runtime containers."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
from typing import Any


def strict_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def load_json(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=strict_object)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"failed to read strict JSON from {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise SystemExit(f"expected a JSON object in {path}")
    return value


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--genesis", type=Path, required=True)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--chain-id", type=int, required=True)
    parser.add_argument("--network-id", type=int, required=True)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    genesis = load_json(args.genesis)
    manifest = load_json(args.manifest)

    actual_hash = sha256(args.genesis)
    expected_hash = manifest.get("file_sha256")
    if expected_hash != actual_hash:
        raise SystemExit(
            f"USDB genesis SHA-256 mismatch: expected {expected_hash!r}, got {actual_hash}"
        )

    if manifest.get("chain_id") != args.chain_id:
        raise SystemExit("USDB genesis manifest chain_id does not match runtime chain ID")
    if manifest.get("network_id") != args.network_id:
        raise SystemExit("USDB genesis manifest network_id does not match runtime network ID")

    config = genesis.get("config")
    if not isinstance(config, dict):
        raise SystemExit("USDB genesis is missing config")
    if config.get("chainId") != args.chain_id or config.get("chainId_alt") != args.chain_id:
        raise SystemExit("USDB genesis chainId/chainId_alt does not match runtime chain ID")

    usdb = config.get("usdb")
    if not isinstance(usdb, dict):
        raise SystemExit("USDB genesis is missing config.usdb")
    activations = usdb.get("activations")
    if not isinstance(activations, list) or not activations or activations[0].get("block") != 0:
        raise SystemExit("USDB genesis activation schedule must start at block 0")

    print(
        json.dumps(
            {
                "chain_id": args.chain_id,
                "network_id": args.network_id,
                "genesis_sha256": actual_hash,
                "activation_count": len(activations),
            },
            sort_keys=True,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
