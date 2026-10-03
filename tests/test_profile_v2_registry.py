"""Keep the live profile oracle aligned with the independently generated V2 registry."""

import copy
import json
from pathlib import Path
import sys
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts/usdb"))
import verify_usdb_profile_e2e as verifier


class ProfileV2RegistryTests(unittest.TestCase):
    def setUp(self):
        golden = json.loads((ROOT / "internal/usdb/testdata/miner_pass_v2_activation_golden.json").read_text())
        self.registry = golden["registries"][0]
        self.active = self.registry["activations"][0]
        self.selector = dict(btc_height=200, snapshot_id="snapshot", system_state_id="state", pass_id="pass")
        self.profile = dict(
            view_version=verifier.VIEW_VERSION,
            external_state=dict(
                btc_height=200, snapshot_id="snapshot", system_state_id="state", stable_lag=10,
                activation_registry_id=self.registry["activation_registry_id"],
                active_version_set_id=self.active["active_version_set_id"],
                active_version_set=copy.deepcopy(self.active["active_version_set"]),
            ),
            miner_aggregate=dict(total_miner_btc_sats="100000000", active_miner_owner_count=1),
        )
        self.profile["pass"] = dict(pass_id="pass", state="active", pass_kind="standard", raw_energy="0",
                                    collab_contribution="0", effective_energy="0", level=0,
                                    difficulty_factor_bps=10000, usdb_main="0x" + "11" * 20)

    def resolve(self):
        with patch.object(verifier, "rpc_call", return_value=self.profile):
            return verifier.resolve_profile("http://unused", self.selector,
                                            verifier.BTC_REGTEST_ACTIVATION_REGISTRY_ID,
                                            verifier.BTC_V2_ACTIVE_VERSION_SET_ID)

    def test_accepts_scoped_v2_profile_from_golden(self):
        self.assertEqual(self.resolve()["factor"], 10000)

    def test_rejects_missing_or_foreign_scope(self):
        for scope in (None, dict(network_id="btc-regtest", rules_scope="foreign-network")):
            with self.subTest(scope=scope):
                self.profile["external_state"]["active_version_set"]["scope"] = scope
                with self.assertRaisesRegex(SystemExit, "active_version_set mismatch"):
                    self.resolve()

    def test_rejects_legacy_or_mixed_rules(self):
        for family, version in (("inscription_schema_version", "uip-0001-miner-pass-inscription:v1"),
                                ("pass_state_machine_version", "uip-0002-pass-state-machine:v1")):
            with self.subTest(family=family):
                self.profile["external_state"]["active_version_set"] = copy.deepcopy(self.active["active_version_set"])
                self.profile["external_state"]["active_version_set"][family] = version
                with self.assertRaisesRegex(SystemExit, "active_version_set mismatch"):
                    self.resolve()


if __name__ == "__main__":
    unittest.main()
