#!/usr/bin/env python3

import argparse
import json
import os
import tempfile


BTC_REGTEST_ACTIVATION_REGISTRY_REVISION_2_ID = (
    "25a39e8022e8351a40f59736b86cf81321c08042121cdb74b85a8f3918a2b973"
)
ACTIVATION_CONFORMANCE_DIFFICULTY_POLICY_VERSION = 65_535


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--genesis", required=True)
    parser.add_argument("--activation-block", required=True, type=int)
    args = parser.parse_args()

    if args.activation_block < 2:
        raise SystemExit("activation block must be at least 2")
    with open(args.genesis, "r", encoding="utf-8") as stream:
        genesis = json.load(stream)

    try:
        activations = genesis["config"]["usdb"]["activations"]
    except (KeyError, TypeError) as error:
        raise SystemExit(f"genesis has no USDB activation schedule: {error}") from error
    if len(activations) != 1 or activations[0].get("block") != 0:
        raise SystemExit("conformance genesis requires one block-0 USDB activation checkpoint")
    if args.activation_block <= activations[0]["block"]:
        raise SystemExit("activation block must follow the existing activation")

    versions = dict(activations[0]["versions"])
    versions["difficultyPolicyVersion"] = (
        ACTIVATION_CONFORMANCE_DIFFICULTY_POLICY_VERSION
    )
    activations.append(
        {
            "block": args.activation_block,
            "btcActivationRegistryId": BTC_REGTEST_ACTIVATION_REGISTRY_REVISION_2_ID,
            "versions": versions,
        }
    )

    directory = os.path.dirname(os.path.abspath(args.genesis))
    descriptor, temporary_path = tempfile.mkstemp(
        dir=directory,
        prefix=".usdb-activation-conformance-",
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
