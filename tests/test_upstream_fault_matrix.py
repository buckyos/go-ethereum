#!/usr/bin/env python3
"""Regression tests for the independent-upstream matrix's non-vacuous gates."""

import copy
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "scripts/usdb"))
from upstream_fault_matrix import (Matrix, Node, RECOVERY_ENV, RPCError, compare_chain, ports, validate_fault,
                                   validate_ord_independence, validate_interrupted_recovery,
                                   validate_transfer_event, reject_orphan_selector, validate_auto_recovery)


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


class AutomaticRecoveryTests(unittest.TestCase):
    def fixture(self):
        identity = {"pid": 123, "start_ticks": 456, "starts": 1}
        evidence = {"validator_before": identity, "validator_after": dict(identity), "elapsed_seconds": 12,
                    "budget_seconds": 90, "blocks": 4, "stalled_height": 2,
                    "head_hash": "canonical", "target_hash": "canonical", "target_anchor": 153}
        failed = {"params": [{"block_height": 150, "context": {"state_id": "parent"}}],
                  "error": {"code": -32098}, "transport_closed": True, "connection_id": 1}
        retry = {"params": copy.deepcopy(failed["params"]), "connection_id": 2}
        head = {"params": [{"block_height": 153, "context": {"state_id": "head"}}], "connection_id": 2}
        return evidence, [failed], [retry, head]

    def test_same_process_retries_identical_selector_over_new_connection(self):
        retries = validate_auto_recovery(*self.fixture())
        self.assertEqual(len(retries), 1)
        self.assertEqual(retries[0]["block_height"], 150)
        self.assertEqual(retries[0]["previous_error_code"], -32098)

    def test_rejects_restart_pid_reuse_or_extended_deadline(self):
        for field, value in (("pid", 124), ("start_ticks", 457), ("starts", 2)):
            with self.subTest(field=field):
                evidence, failed, success = self.fixture()
                evidence["validator_after"][field] = value
                with self.assertRaisesRegex(ValueError, "replaced"):
                    validate_auto_recovery(evidence, failed, success)
        for field, value in (("elapsed_seconds", 90.1), ("budget_seconds", 91), ("budget_seconds", 0),
                             ("blocks", 2), ("head_hash", "fork")):
            with self.subTest(field=field, value=value):
                evidence, failed, success = self.fixture()
                evidence[field] = value
                with self.assertRaises(ValueError):
                    validate_auto_recovery(evidence, failed, success)

    def test_equal_height_does_not_replace_failed_selector_or_transport_evidence(self):
        for mutation in ("selector", "still-fails", "old-anchor", "closed-connection", "no-failure"):
            with self.subTest(mutation=mutation):
                evidence, failed, success = self.fixture()
                if mutation == "selector":
                    success[0]["params"][0]["context"]["state_id"] = "different"
                elif mutation == "still-fails":
                    success[0]["error"] = {"code": -32041}
                elif mutation == "old-anchor":
                    success.pop()
                elif mutation == "closed-connection":
                    success[0]["connection_id"] = 1
                else:
                    failed.clear()
                with self.assertRaises(ValueError):
                    validate_auto_recovery(evidence, failed, success)

    def test_process_pin_prevents_stop_or_manual_peer_reconnection(self):
        node = Node(SimpleNamespace(args=SimpleNamespace(work_dir=Path("/tmp/unused"), port_base=22400)), "b", 1)
        process = SimpleNamespace(pid=123, poll=lambda: None)
        node.processes["geth"] = node.pinned_geth = process
        with self.assertRaisesRegex(ValueError, "restart is forbidden"):
            node.stop("geth")
        self.assertIs(node.processes["geth"], process)
        with self.assertRaisesRegex(ValueError, "manual validator reconnection"):
            Matrix.__new__(Matrix).connect_geth(node)
        node.processes["geth"] = SimpleNamespace(pid=123, poll=lambda: None)
        with self.assertRaisesRegex(ValueError, "changed or exited"):
            node.validator_identity()

    def test_upstream_waits_share_the_recovery_deadline(self):
        matrix = Matrix.__new__(Matrix)
        matrix.deadline = time.monotonic() + 60
        matrix.recovery_deadline = time.monotonic() - 1
        matrix.check_alive = lambda: self.fail("expired recovery should not keep polling")
        with self.assertRaisesRegex(ValueError, "timeout"):
            matrix.wait("upstream recovery", lambda: self.fail("must not extend recovery budget"))


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


class TransferEventTests(unittest.TestCase):
    def fixture(self):
        mint = {"event_type": "mint", "block_height": 133, "owner": "old", "satpoint": "mint:0:0"}
        before = {"height": 180, "passes": {"pass": {
            "snapshot": {"owner": "old", "state": "active", "satpoint": "mint:0:0"}, "profile": {"pass": {"raw_energy": "45000"}}}},
            "owners": {"old": {"balance": [{"balance": 100010000}]}},
            "ledgers": {"pass": {"history": {"items": [mint]}}}}
        transferred = {"height": 181, "passes": {"pass": {
            "snapshot": {"owner": "new", "mint_owner": "old", "state": "dormant", "satpoint": "tx:0:0"},
            "profile": {"pass": {"owner_script_hash": "new", "state": "dormant", "raw_energy": "46000", "effective_energy": "0"}}}},
            "owners": {"old": {"active": None, "balance": [{"balance": 100000000}]},
                       "new": {"active": None, "balance": [{"balance": 9000}]}},
            "ledgers": {"pass": {"history": {"items": [mint,
                {"event_type": "state_update", "block_height": 181, "state": "dormant", "owner": "old", "satpoint": "mint:0:0"},
                {"event_type": "owner_transfer", "block_height": 181, "state": "dormant", "owner": "new", "satpoint": "tx:0:0"}]},
                                  "energy": {"items": [{"record_block_height": 181, "energy": "46000"}]}}},
            "candidates": {"items": []}}
        later = copy.deepcopy(transferred)
        later["height"] = 183
        return before, transferred, later, {"height": 181, "txid": "tx"}

    def check(self, fixture):
        return validate_transfer_event(*fixture, "pass", "old", "new")

    def test_transfer_settles_once_and_freezes_energy(self):
        self.assertEqual(self.check(self.fixture()), 46000)

    def test_rejects_wrong_owner_energy_or_retained_economic_capability(self):
        mutations = (
            lambda s: s["passes"]["pass"]["snapshot"].update(owner="old"),
            lambda s: s["passes"]["pass"]["snapshot"].update(satpoint="wrong:0:0"),
            lambda s: s["passes"]["pass"]["profile"]["pass"].update(raw_energy="45999"),
            lambda s: s["passes"]["pass"]["profile"]["pass"].update(effective_energy="46000"),
            lambda s: s["owners"]["old"].update(active={"inscription_id": "pass"}),
            lambda s: s["owners"]["new"]["balance"][0].update(balance=0),
            lambda s: s["candidates"]["items"].append({"pass_id": "pass"}),
        )
        for i, mutate in enumerate(mutations):
            with self.subTest(mutation=i):
                fixture = self.fixture()
                mutate(fixture[1])
                with self.assertRaises(ValueError):
                    self.check(fixture)

    def test_rejects_missing_duplicate_or_reapplied_transfer(self):
        for mode in ("missing", "duplicate", "reordered", "growth", "ledger"):
            with self.subTest(mode=mode):
                fixture = self.fixture()
                later = fixture[2]
                rows = later["ledgers"]["pass"]["history"]["items"]
                if mode == "missing":
                    rows.pop()
                elif mode == "duplicate":
                    rows.append(copy.deepcopy(rows[-1]))
                elif mode == "reordered":
                    rows[-1], rows[-2] = rows[-2], rows[-1]
                elif mode == "growth":
                    later["passes"]["pass"]["profile"]["pass"]["raw_energy"] = "48000"
                else:
                    later["ledgers"]["pass"]["energy"]["items"].append({"record_block_height": 183, "energy": "46000"})
                with self.assertRaises(ValueError):
                    self.check(fixture)

    def test_orphan_selector_needs_permanent_mismatch_not_unavailability(self):
        for code in (-32041, -32049, -32098):
            with self.subTest(code=code):
                def rpc(_method, _params):
                    raise RPCError("get_pass_economic_profile", {"code": code})
                with self.assertRaisesRegex(ValueError, "unexpected orphan selector error"):
                    reject_orphan_selector(rpc, {})
        with self.assertRaisesRegex(ValueError, "was accepted"):
            reject_orphan_selector(lambda *_: {}, {})
        def mismatch(_method, _params):
            raise RPCError("get_pass_economic_profile", {"code": -32042})
        self.assertEqual(reject_orphan_selector(mismatch, {}), -32042)


class RecoveryLifecycleTests(unittest.TestCase):
    def test_startup_retries_only_the_known_readiness_lock(self):
        matrix = Matrix.__new__(Matrix)
        matrix.deadline = time.monotonic() + 3
        matrix.check_alive = lambda: None
        matrix.log = lambda _: None
        busy = "Snapshot history storage failed: action=btc_synced_block_height, error=database is locked"
        for method, message, retry in (("get_readiness", busy, True), ("get_pass_economic_profile", busy, False),
                                       ("get_readiness", "database disk image is malformed", False)):
            with self.subTest(method=method, message=message):
                calls = []
                def ready():
                    calls.append(True)
                    if len(calls) == 1:
                        raise RPCError(method, {"code": -32603, "message": message})
                    return True
                if retry:
                    matrix.wait("readiness", ready, seconds=1, interval=0)
                    self.assertEqual(len(calls), 2)
                else:
                    with self.assertRaises(RPCError):
                        matrix.wait("readiness", ready, seconds=1, interval=0)
                    self.assertEqual(len(calls), 1)

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
