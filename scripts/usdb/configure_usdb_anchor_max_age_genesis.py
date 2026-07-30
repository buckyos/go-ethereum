#!/usr/bin/env python3

import argparse
import json
import os
import tempfile


def configure_anchor_max_age(genesis: dict, max_age_blocks: int) -> dict:
    if max_age_blocks <= 0 or max_age_blocks > 0xFFFFFFFF:
        raise ValueError("BTC anchor max age must fit one positive uint32")
    try:
        activations = genesis["config"]["usdb"]["activations"]
    except (KeyError, TypeError) as error:
        raise ValueError(f"genesis has no USDB activation schedule: {error}") from error
    if (
        not isinstance(activations, list)
        or len(activations) != 1
        or activations[0].get("block") != 0
    ):
        raise ValueError(
            "anchor-boundary genesis requires one block-0 USDB activation checkpoint"
        )

    activations[0]["btcAnchorMaxAgeBlocks"] = max_age_blocks
    return genesis


def write_json_atomically(path: str, payload: dict) -> None:
    directory = os.path.dirname(os.path.abspath(path))
    descriptor, temporary_path = tempfile.mkstemp(
        dir=directory,
        prefix=".usdb-anchor-max-age-",
        suffix=".json",
        text=True,
    )
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
            json.dump(payload, stream, indent=2)
            stream.write("\n")
        os.replace(temporary_path, path)
    finally:
        if os.path.exists(temporary_path):
            os.unlink(temporary_path)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--genesis", required=True)
    parser.add_argument("--max-age-blocks", required=True, type=int)
    args = parser.parse_args()

    with open(args.genesis, "r", encoding="utf-8") as stream:
        genesis = json.load(stream)
    try:
        configured = configure_anchor_max_age(genesis, args.max_age_blocks)
    except ValueError as error:
        raise SystemExit(str(error)) from error
    write_json_atomically(args.genesis, configured)


if __name__ == "__main__":
    main()
