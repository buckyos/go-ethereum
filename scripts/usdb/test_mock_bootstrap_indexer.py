#!/usr/bin/env python3

import json
import sys
import threading
import unittest
import urllib.request
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
REPO_ROOT = SCRIPT_DIR.parent.parent
sys.path.insert(0, str(SCRIPT_DIR))

from mock_bootstrap_indexer import (  # noqa: E402
    ACTIVE_VERSION_SET,
    ACTIVE_VERSION_SET_ID,
    BTC_HEIGHT,
    BootstrapIndexerServer,
    DEFAULT_COLLAB_CONTRIBUTION,
    DEFAULT_TOTAL_MINER_BTC_SATS,
    DEFAULT_USDB_MAIN,
    LOCAL_STATE_COMMIT,
    REGISTRY_ID,
    SNAPSHOT_ID,
    SYSTEM_STATE_ID,
    build_miner_candidate,
    build_pass_profile,
    build_state_ref,
    build_system_state,
)


PASS_ID = "33" * 32 + "i0"


def profile_params() -> list[dict]:
    return [
        {
            "view_version": "uip-0006-usdb-economic-state-view:v1",
            "pass_id": PASS_ID,
            "block_height": BTC_HEIGHT,
            "context": {
                "requested_height": BTC_HEIGHT,
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
            (REPO_ROOT / "internal/usdb/testdata/miner_pass_v2_activation_golden.json").read_text(encoding="utf-8")
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

    def test_state_ref_rejects_wrong_height_and_mixed_identity(self) -> None:
        for height in (0, BTC_HEIGHT + 1, True):
            params = profile_params()
            params[0]["block_height"] = height
            params[0]["context"]["requested_height"] = height
            for build in (build_state_ref, lambda p: build_miner_candidate(PASS_ID, p)):
                params[0]["usdb_main"] = DEFAULT_USDB_MAIN
                with self.subTest(height=height, build=build), self.assertRaises(ValueError):
                    build(params)
        for field in ("snapshot_id", "system_state_id", "stable_block_hash", "local_state_commit",
                      "activation_registry_id", "active_version_set_id"):
            params = profile_params()
            params[0]["context"]["expected_state"][field] = "ff" * 32
            with self.subTest(field=field), self.assertRaisesRegex(ValueError, f"unexpected {field}"):
                build_state_ref(params)
            with self.subTest(profile_field=field), self.assertRaisesRegex(ValueError, f"unexpected {field}"):
                build_pass_profile(PASS_ID, params)

    def test_rpc_miner_query_sequence_preserves_state_identity(self) -> None:
        # Exercise the HTTP dispatch as well as the response builders. The
        # miner first resolves a state reference, then pins candidate/profile.
        with BootstrapIndexerServer(("127.0.0.1", 0), PASS_ID, DEFAULT_USDB_MAIN,
                                    DEFAULT_TOTAL_MINER_BTC_SATS, DEFAULT_COLLAB_CONTRIBUTION) as server:
            worker = threading.Thread(target=server.serve_forever, daemon=True)
            worker.start()
            try:
                def rpc(method, params):
                    payload = json.dumps(dict(jsonrpc="2.0", id=1, method=method, params=params)).encode()
                    req = urllib.request.Request(f"http://127.0.0.1:{server.server_port}", data=payload,
                                                 headers={"Content-Type": "application/json"})
                    with urllib.request.urlopen(req, timeout=5) as response:
                        body = json.load(response)
                    self.assertNotIn("error", body)
                    return body["result"]

                state = rpc("get_system_state_info", [])
                self.assertGreaterEqual(state["local_synced_block_height"], 1)
                params = profile_params()
                params[0].pop("pass_id")
                params[0]["context"]["expected_state"] = {
                    "activation_registry_id": REGISTRY_ID,
                    "active_version_set_id": ACTIVE_VERSION_SET_ID,
                }
                ref = rpc("get_state_ref_at_height", params)
                self.assertEqual(ref["system_state_info"], state)
                self.assertEqual(ref["block_height"], BTC_HEIGHT)
                self.assertEqual(ref["snapshot_info"]["balance_history_stable_height"], BTC_HEIGHT)
                self.assertEqual(ref["local_state_commit_info"]["local_state_commit"], LOCAL_STATE_COMMIT)
                params[0]["context"]["expected_state"].update(
                    snapshot_id=ref["snapshot_info"]["snapshot_id"],
                    system_state_id=state["system_state_id"],
                    local_state_commit=LOCAL_STATE_COMMIT,
                    stable_block_hash=ref["snapshot_info"]["stable_block_hash"],
                )
                params[0]["usdb_main"] = DEFAULT_USDB_MAIN
                candidate = rpc("resolve_miner_candidate", params)
                params[0]["pass_id"] = PASS_ID
                profile = rpc("get_pass_economic_profile", params)
                self.assertEqual(candidate["external_state"], profile["external_state"])
                self.assertEqual(profile["external_state"]["btc_height"], BTC_HEIGHT)
            finally:
                server.shutdown()
                worker.join(timeout=5)


if __name__ == "__main__":
    unittest.main()
