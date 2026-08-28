#!/usr/bin/env python3

import json
import sys
import unittest
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
REPO_ROOT = SCRIPT_DIR.parent.parent
sys.path.insert(0, str(SCRIPT_DIR))

from mock_bootstrap_indexer import (  # noqa: E402
    ACTIVE_VERSION_SET,
    ACTIVE_VERSION_SET_ID,
    DEFAULT_COLLAB_CONTRIBUTION,
    DEFAULT_TOTAL_MINER_BTC_SATS,
    DEFAULT_USDB_MAIN,
    REGISTRY_ID,
    SNAPSHOT_ID,
    SYSTEM_STATE_ID,
    build_miner_candidate,
    build_pass_profile,
    build_system_state,
)


PASS_ID = "33" * 32 + "i0"


def profile_params() -> list[dict]:
    return [
        {
            "view_version": "uip-0006-usdb-economic-state-view:v1",
            "pass_id": PASS_ID,
            "block_height": 0,
            "context": {
                "requested_height": 0,
                "expected_state": {
                    "snapshot_id": SNAPSHOT_ID,
                    "system_state_id": SYSTEM_STATE_ID,
                    "activation_registry_id": REGISTRY_ID,
                    "active_version_set_id": ACTIVE_VERSION_SET_ID,
                },
            },
        }
    ]


def candidate_params() -> list[dict]:
    params = profile_params()
    params[0].pop("pass_id")
    params[0]["usdb_main"] = DEFAULT_USDB_MAIN
    return params


class MockBootstrapIndexerTest(unittest.TestCase):
    def test_fixture_matches_current_regtest_revision(self) -> None:
        golden = json.loads(
            (REPO_ROOT / "internal/usdb/btc_activation_golden.json").read_text(encoding="utf-8")
        )
        registry = next(
            item
            for item in golden["registries"]
            if item["network_id"] == "btc-regtest" and item["revision"] == 1
        )
        activation = registry["activations"][0]

        self.assertEqual(registry["activation_registry_id"], REGISTRY_ID)
        self.assertEqual(activation["btc_height"], 0)
        self.assertEqual(activation["active_version_set"], ACTIVE_VERSION_SET)
        self.assertEqual(activation["active_version_set_id"], ACTIVE_VERSION_SET_ID)
        self.assertEqual(build_system_state()["active_version_set_id"], ACTIVE_VERSION_SET_ID)

    def test_profile_echoes_frozen_query_identity(self) -> None:
        profile = build_pass_profile(PASS_ID, profile_params())

        self.assertEqual(profile["external_state"]["snapshot_id"], SNAPSHOT_ID)
        self.assertEqual(profile["pass"]["pass_id"], PASS_ID)
        self.assertEqual(profile["pass"]["difficulty_factor_bps"], 10000)
        self.assertEqual(profile["pass"]["usdb_main"], DEFAULT_USDB_MAIN)
        self.assertEqual(
            profile["pass"]["collab_contribution"],
            DEFAULT_COLLAB_CONTRIBUTION,
        )
        self.assertEqual(
            profile["pass"]["effective_energy"],
            DEFAULT_COLLAB_CONTRIBUTION,
        )
        self.assertEqual(
            profile["miner_aggregate"]["total_miner_btc_sats"],
            DEFAULT_TOTAL_MINER_BTC_SATS,
        )
        self.assertEqual(
            profile["miner_aggregate"]["active_miner_owner_count"],
            1,
        )

    def test_profile_rejects_tampered_expected_state(self) -> None:
        params = profile_params()
        params[0]["context"]["expected_state"]["system_state_id"] = "ff" * 32

        with self.assertRaisesRegex(ValueError, "unexpected system_state_id"):
            build_pass_profile(PASS_ID, params)

    def test_candidate_resolves_profile_by_usdb_main(self) -> None:
        candidate = build_miner_candidate(PASS_ID, candidate_params())

        self.assertEqual(candidate["pass"]["pass_id"], PASS_ID)
        self.assertEqual(candidate["pass"]["usdb_main"], DEFAULT_USDB_MAIN)
        self.assertEqual(candidate["matching_candidate_count"], 1)
        self.assertEqual(
            candidate["selection_rule"],
            "uip-0006:effective-energy-desc-pass-id-asc:v1",
        )


if __name__ == "__main__":
    unittest.main()
