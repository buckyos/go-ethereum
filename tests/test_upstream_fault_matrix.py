#!/usr/bin/env python3
"""Regression tests for the independent-upstream matrix's non-vacuous gates."""

import copy
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "scripts/usdb"))
from upstream_fault_matrix import (Matrix, Node, RECOVERY_ENV, compare_chain, ports, validate_fault,
                                   validate_ord_independence, validate_interrupted_recovery)


class FaultCoverageTests(unittest.TestCase):
    def evidence(self):
        return {"before": 2, "healthy_after": 4, "validator_after": 2,
                "old_anchor": 150, "new_anchor": 153, "observed_seconds": 5,
                "profile_errors": 1, "profile_error_codes": [-32098, -32042],
                "fork_depth": 23, "canonical_hash": "a", "fork_hash": "b",
                "canonical_balance": 200000000, "fork_balance": 100000000}

    def test_rejects_quiet_but_unexercised_faults(self):
        for field, value in (("healthy_after", 2), ("validator_after", 3), ("new_anchor", 150),
                             ("observed_seconds", 0), ("profile_errors", 0), ("profile_error_codes", [-32049])):
            with self.subTest(field=field):
                evidence = self.evidence()
                evidence[field] = value
                with self.assertRaises(ValueError):
                    validate_fault(evidence, "indexer-crash")

    def test_requires_real_stable_fork_and_economic_change(self):
        for field, value in (("fork_depth", 10), ("fork_hash", "a"), ("fork_balance", 200000000)):
            with self.subTest(field=field):
                evidence = self.evidence()
                evidence[field] = value
                with self.assertRaises(ValueError):
                    validate_fault(evidence, "stable-fork")

    def test_accepts_exercised_fault(self):
        validate_fault(self.evidence(), "indexer-crash")
        validate_fault(self.evidence(), "stable-fork")

    def test_node_ports_and_implicit_bitcoin_listeners_are_disjoint(self):
        allocations = [ports(22400, index) for index in range(3)]
        all_ports = [port for allocation in allocations for port in allocation.values()]
        self.assertEqual(len(all_ports), len(set(all_ports)))
        for allocation in allocations:
            self.assertEqual(allocation["btc-onion"], allocation["btc-p2p"] + 1)
        self.assertLess(max(all_ports), 32768)

    def test_upstream_fault_requires_live_indexer_and_specific_blocker(self):
        for kind, blocker in (("balance-crash", "UpstreamReadinessUnknown"), ("ord-source-outage", "CatchingUp")):
            evidence = self.evidence()
            evidence["profile_error_codes"] = [-32041]
            evidence["readiness"] = {"rpc_alive": True, "consensus_ready": False, "blockers": [blocker]}
            validate_fault(evidence, kind)
            for field, bad in (("rpc_alive", False), ("consensus_ready", True), ("blockers", [])):
                with self.subTest(kind=kind, field=field):
                    broken = copy.deepcopy(evidence)
                    broken["readiness"][field] = bad
                    with self.assertRaises(ValueError):
                        validate_fault(broken, kind)

    def test_optional_ord_outage_requires_successful_new_validation(self):
        evidence = {"source": "bitcoind", "ord_exit_code": -9, "btc_before": 150, "btc_after": 153,
                    "before": 4, "after": 6, "validator_after": 6, "profile_successes": 1,
                    "readiness": {"consensus_ready": True}}
        validate_ord_independence(evidence)
        for field, bad in (("source", "ord"), ("ord_exit_code", 0), ("btc_after", 150),
                           ("after", 4), ("validator_after", 4), ("profile_successes", 0)):
            with self.subTest(field=field):
                broken = {**evidence, field: bad}
                with self.assertRaises(ValueError):
                    validate_ord_independence(broken)

    def test_interruption_requires_durable_recovery_and_real_rejection(self):
        evidence = {"readiness": {"rpc_alive": True, "consensus_ready": False, "query_ready": False,
                                  "blockers": ["ReorgRecoveryPending"], "synced_block_height": 147},
                    "pending_before": 147, "pending_after": 147, "epoch": 2, "fork_epoch": 1,
                    "hook_hits": 1, "profile_errors": 1, "validator_before": 4, "validator_after": 4, "exit_code": -9}
        validate_interrupted_recovery(evidence)
        for field, bad in (("pending_before", None), ("pending_after", None), ("epoch", 1),
                           ("hook_hits", 0), ("profile_errors", 0), ("validator_after", 5), ("exit_code", 0)):
            with self.subTest(field=field):
                broken = {**evidence, field: bad}
                with self.assertRaises(ValueError):
                    validate_interrupted_recovery(broken)
        broken = copy.deepcopy(evidence)
        broken["readiness"]["query_ready"] = True
        with self.assertRaises(ValueError):
            validate_interrupted_recovery(broken)


class FullReplayTests(unittest.TestCase):
    def blocks(self):
        return [{"number": hex(i), "hash": str(i), "parentHash": str(i - 1), "stateRoot": "state",
                 "receiptsRoot": "receipts", "transactionsRoot": "transactions", "extraData": "selectors"}
                for i in range(3)]

    def test_equal_height_does_not_hide_historical_state_mismatch(self):
        for field in ("hash", "stateRoot", "receiptsRoot", "transactionsRoot", "extraData"):
            with self.subTest(field=field):
                original = self.blocks()
                replay = copy.deepcopy(original)
                replay[1][field] = "corrupt"
                with self.assertRaisesRegex(ValueError, f"block 1 {field} mismatch"):
                    compare_chain(original, replay)

    def test_rejects_missing_or_reordered_history(self):
        original = self.blocks()
        for replay in ([], original[:2], [original[1], original[0], original[2]]):
            with self.assertRaises(ValueError):
                compare_chain(original, replay)
        with self.assertRaises(ValueError):
            compare_chain(original[:1], original[:1])

    def test_accepts_complete_replay(self):
        compare_chain(self.blocks(), self.blocks())

    def test_fresh_upstream_refuses_inherited_state_before_starting_services(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            (root / "c").mkdir()
            sentinel = root / "c/previous-state"
            sentinel.write_text("keep")
            node = Node(SimpleNamespace(args=SimpleNamespace(work_dir=root, port_base=22400)), "c", 2)
            with self.assertRaises(FileExistsError):
                node.fresh_upstream()
            self.assertEqual(node.processes, {})
            self.assertEqual(sentinel.read_text(), "keep")

    def test_run_only_rejects_missing_preparation_before_starting_services(self):
        runner = Path(__file__).resolve().parents[1] / "scripts/usdb/run_usdb_upstream_fault_matrix.sh"
        with tempfile.TemporaryDirectory() as work:
            env = {**os.environ, "MATRIX_WORK_ROOT": work, "MATRIX_SKIP_BUILD": "1", "GETH_BIN": "/bin/true"}
            result = subprocess.run(["bash", str(runner)], env=env, capture_output=True, text=True, timeout=5)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Missing prepared service binary", result.stderr)
            self.assertFalse(list(Path(work).glob("run-*")))


class RecoveryLifecycleTests(unittest.TestCase):
    def test_fault_hooks_do_not_leak_into_later_restarts(self):
        args = SimpleNamespace(work_dir=Path("/tmp/unused-matrix-test"), port_base=22400,
                               balance_history=Path("/unused/balance-history"), indexer=Path("/unused/indexer"))
        node = Node(SimpleNamespace(args=args), "b", 1)
        with patch.dict(os.environ, {key: "99" for key in RECOVERY_ENV}), patch.object(node, "start") as start:
            for stage, expected in (("energy", {RECOVERY_ENV[0]}), ("transfer", {RECOVERY_ENV[1]}), (None, set())):
                node.start_indexer(recovery_stage=stage)
                env = start.call_args.kwargs["env"]
                self.assertEqual(set(RECOVERY_ENV).intersection(env), expected)
            node.start_service("balance-history")
            self.assertTrue(set(RECOVERY_ENV).isdisjoint(start.call_args.kwargs["env"]))

    def test_pending_marker_read_is_independent_and_read_only(self):
        with tempfile.TemporaryDirectory() as work:
            matrix = Matrix.__new__(Matrix)
            matrix.b = SimpleNamespace(root=Path(work))
            path = Path(work) / "usdb-indexer/data/miner_pass.db"
            path.parent.mkdir(parents=True)
            with self.assertRaises(sqlite3.OperationalError):
                matrix.pending_recovery_height()
            self.assertFalse(path.exists())
            conn = sqlite3.connect(path)
            try:
                conn.execute("CREATE TABLE state (name TEXT PRIMARY KEY, value TEXT)")
                conn.commit()
                self.assertIsNone(matrix.pending_recovery_height())
                conn.execute("INSERT INTO state VALUES ('upstream_reorg_recovery_pending_height', '156')")
                conn.commit()
                before = path.read_bytes()
                self.assertEqual(matrix.pending_recovery_height(), 156)
                self.assertEqual(path.read_bytes(), before)
                conn.execute("DELETE FROM state")
                conn.commit()
                self.assertIsNone(matrix.pending_recovery_height())
            finally:
                conn.close()


if __name__ == "__main__":
    unittest.main()
