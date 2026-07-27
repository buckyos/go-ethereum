#!/usr/bin/env python3

import argparse
import json
import os
import tempfile
from pathlib import Path
from typing import Any


def parse_positive_int(value: str) -> int:
    try:
        parsed = int(value, 0)
    except ValueError as error:
        raise argparse.ArgumentTypeError(f"invalid integer {value!r}") from error
    if parsed <= 0:
        raise argparse.ArgumentTypeError("difficulty must be positive")
    return parsed


def configure_genesis(
    genesis: dict[str, Any], genesis_difficulty: int, minimum_difficulty: int
) -> dict[str, Any]:
    if genesis_difficulty <= 0 or minimum_difficulty <= 0:
        raise ValueError("difficulty values must be positive")
    if genesis_difficulty < minimum_difficulty:
        raise ValueError("genesis difficulty must not be below minimum difficulty")
    config = genesis.get("config")
    if not isinstance(config, dict):
        raise ValueError("genesis has no chain config")
    usdb = config.get("usdb")
    if not isinstance(usdb, dict) or not usdb.get("activations"):
        raise ValueError("genesis has no USDB activation schedule")

    genesis["difficulty"] = hex(genesis_difficulty)
    # ChainConfig uses *big.Int directly, whose JSON form is a decimal number.
    # The top-level Genesis difficulty has a separate hex-quantity codec.
    config["ethPoWMinimumDifficulty"] = minimum_difficulty
    return genesis


def write_atomic(path: Path, value: dict[str, Any]) -> None:
    descriptor, temporary_path = tempfile.mkstemp(
        dir=path.parent,
        prefix=".usdb-pow-calibration-",
        suffix=".json",
        text=True,
    )
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
            json.dump(value, stream, indent=2)
            stream.write("\n")
        os.replace(temporary_path, path)
    finally:
        if os.path.exists(temporary_path):
            os.unlink(temporary_path)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--genesis", required=True, type=Path)
    parser.add_argument("--genesis-difficulty", required=True, type=parse_positive_int)
    parser.add_argument("--minimum-difficulty", required=True, type=parse_positive_int)
    args = parser.parse_args()

    try:
        genesis = json.loads(args.genesis.read_text(encoding="utf-8"))
        if not isinstance(genesis, dict):
            raise ValueError("genesis root must be an object")
        configured = configure_genesis(
            genesis, args.genesis_difficulty, args.minimum_difficulty
        )
        write_atomic(args.genesis, configured)
    except (OSError, json.JSONDecodeError, ValueError) as error:
        parser.error(str(error))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
