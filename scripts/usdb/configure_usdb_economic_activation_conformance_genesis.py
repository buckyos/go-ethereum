#!/usr/bin/env python3

import argparse
import json
import os
import tempfile


ECONOMIC_CONFORMANCE_V2 = 65_534
ECONOMIC_CONFORMANCE_V3 = 65_535


def checkpoint(base, block, version):
    versions = dict(base["versions"])
    versions["quotePolicyVersion"] = version
    versions["auxPoolPolicyVersion"] = version
    return {
        "block": block,
        "btcActivationRegistryId": base["btcActivationRegistryId"],
        "versions": versions,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--genesis", required=True)
    parser.add_argument("--v2-activation-block", required=True, type=int)
    parser.add_argument("--v3-activation-block", required=True, type=int)
    args = parser.parse_args()

    if args.v2_activation_block < 2:
        raise SystemExit("fake v2 activation block must be at least 2")
    if args.v3_activation_block <= args.v2_activation_block:
        raise SystemExit("fake v3 activation block must follow fake v2")
    with open(args.genesis, "r", encoding="utf-8") as stream:
        genesis = json.load(stream)

    try:
        activations = genesis["config"]["usdb"]["activations"]
    except (KeyError, TypeError) as error:
        raise SystemExit(f"genesis has no USDB activation schedule: {error}") from error
    if len(activations) != 1 or activations[0].get("block") != 0:
        raise SystemExit(
            "economic conformance genesis requires one block-0 USDB activation checkpoint"
        )
    base = activations[0]
    versions = base.get("versions") or {}
    if versions.get("quotePolicyVersion") != 0 or versions.get("auxPoolPolicyVersion") != 0:
        raise SystemExit("economic conformance requires quote and aux policy 0 at genesis")

    activations.extend(
        [
            checkpoint(
                base,
                args.v2_activation_block,
                ECONOMIC_CONFORMANCE_V2,
            ),
            checkpoint(
                base,
                args.v3_activation_block,
                ECONOMIC_CONFORMANCE_V3,
            ),
        ]
    )

    directory = os.path.dirname(os.path.abspath(args.genesis))
    descriptor, temporary_path = tempfile.mkstemp(
        dir=directory,
        prefix=".usdb-economic-activation-conformance-",
        suffix=".json",
        text=True,
    )
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
            json.dump(genesis, stream, indent=2)
            stream.write("\n")
        os.replace(temporary_path, args.genesis)
    finally:
        if os.path.exists(temporary_path):
            os.unlink(temporary_path)


if __name__ == "__main__":
    main()
