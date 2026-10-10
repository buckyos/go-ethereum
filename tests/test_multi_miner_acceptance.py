#!/usr/bin/env python3
"""Fail-closed evidence gates and transparent fault injection regressions."""

from concurrent.futures import ThreadPoolExecutor
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import sys
import tempfile
import threading
import time
from types import SimpleNamespace
import unittest
from urllib import request

sys.path.insert(0, str(Path(__file__).resolve().parent))
from multi_miner_acceptance import (validate_chain, validate_lag, required_cases, common_head,
                                    expiry_log_evidence, validate_retry_expiry,
                                    MultiMinerMatrix, MINER, SECOND_MINER, ANCHOR_MAX_AGE)
from common.rpc_delay import BlockDelayProxy


class EvidenceTests(unittest.TestCase):
    def test_convergence_tracks_a_later_common_tip_without_accepting_equal_height_forks(self):
        heads = [{"number": "0x5", "hash": "a"}, {"number": "0x5", "hash": "fork"}]
        nodes = [SimpleNamespace(block=lambda i=i: heads[i]) for i in range(2)]
        self.assertIsNone(common_head(nodes))
        heads[:] = [{"number": "0x6", "hash": "descendant"}] * 2
        self.assertEqual(common_head(nodes), heads[0])

    def test_coverage_includes_every_fault_recovery_and_both_end_conditions(self):
        for cycles in (1, 3):
            cases = required_cases(cycles)
            self.assertEqual(len(cases), 4 + 10 * cycles)
            self.assertEqual(len(cases), len(set(cases)))
            self.assertIn(f"competing-delay-{cycles}-fork-choice", cases)
            self.assertEqual(cases[-3:], ["anchor-exhaustion", "invalid-block", "final-state"])
            for mode, expected in (("downloader", ["usdb-indexer-delay-1-expiry"]),
                                   ("all", ["balance-history-delay-1-expiry", "usdb-indexer-delay-1-expiry"])):
                expanded = required_cases(cycles, mode)
                self.assertEqual([c for c in expanded if c.endswith("-expiry")], expected)
                for name in expected:
                    self.assertEqual(expanded[expanded.index(name) + 1], name.removesuffix("-expiry") + "-recovery")

    def test_retry_expiry_requires_new_session_and_the_actual_gossip_tip_eviction(self):
        log = "\n".join([
            'Chain validation waiting for external state operation="blocks 7..12"',
            'External state wait budget exhausted operation="blocks 7..12"',
            'Chain validation waiting for external state operation="blocks 7..12"',
            'Expired propagated block awaiting validation peer=x number=12 hash=abc',
            'Expired propagated block awaiting external state peer=x number=7 hash=def',
        ])
        parsed = expiry_log_evidence(log)
        self.assertEqual(parsed["expired_block_numbers"], [7, 12])
        evidence = {**parsed, "path": "fetcher", "observed_seconds": 1801,
                    "target_hash_before": "abc", "target_hash_after": "abc", "target_height": 12,
                    "validator_height": 11, "profile_errors": 4}
        validate_retry_expiry(evidence)
        for key, value in (("observed_seconds", 1799), ("target_hash_after", "new-block"),
                           ("validator_height", 12), ("validator_height", 6), ("profile_errors", 0),
                           ("expired_block_numbers", [7])):
            with self.subTest(key=key), self.assertRaises(ValueError):
                validate_retry_expiry({**evidence, key: value})
        # With only the tip missing, the peer's advertised parent can equal the
        # local head; no downloader session is guaranteed before recovery.
        validate_retry_expiry({**evidence, "exhausted_sessions": 0, "new_session_after_exhaustion": False})
        downloader = {**evidence, "path": "downloader", "observed_seconds": 121,
                      "expired_block_numbers": []}
        validate_retry_expiry(downloader)
        with self.assertRaises(ValueError):
            validate_retry_expiry({**downloader, "observed_seconds": 119})
        for key, value in (("exhausted_sessions", 0), ("new_session_after_exhaustion", False)):
            with self.subTest(key=key), self.assertRaises(ValueError):
                validate_retry_expiry({**downloader, key: value})
        # A wait earlier in the same session cannot establish a new session.
        self.assertFalse(expiry_log_evidence(log.splitlines()[0] + "\n" + log.splitlines()[1])[
            "new_session_after_exhaustion"])

    def test_lag_requires_live_committed_history_and_independent_progress(self):
        evidence = {"held_reads": 1, "old_anchor": 100, "target_anchor": 102, "indexer_height": 100,
                    "balance_height": 102, "historical_query_succeeded": True,
                    "historical_profile_matches": True,
                    "readiness": {"consensus_ready": False}, "kind": "usdb-indexer"}
        validate_lag(evidence)
        for key, value in (("held_reads", 0), ("old_anchor", 102), ("indexer_height", 102),
                           ("balance_height", 100), ("historical_query_succeeded", False),
                           ("historical_profile_matches", False),
                           ("readiness", {"consensus_ready": True})):
            with self.subTest(key=key), self.assertRaises(ValueError):
                validate_lag({**evidence, key: value})
        validate_lag({**evidence, "kind": "balance-history", "balance_height": 100})
        with self.assertRaises(ValueError):
            validate_lag({**evidence, "kind": "balance-history"})

    def test_failed_recovery_preserves_ready_dependency_and_stalled_head(self):
        matrix = MultiMinerMatrix.__new__(MultiMinerMatrix)
        matrix.args = SimpleNamespace(output_dir=Path('/unused'))
        matrix.a = SimpleNamespace(block=lambda: self.block(2, 102, 0))
        matrix.phase = lambda name: None
        saved = []
        matrix.report = {}
        matrix.write_json = lambda path, value: saved.append((path, value))

        def timeout(*args, **kwargs):
            raise ValueError('automatic import timeout')

        matrix.wait = timeout
        replies = {'get_snapshot_info': {'stable_height': 102},
                   'get_readiness': {'consensus_ready': True, 'synced_block_height': 102},
                   'eth_blockNumber': '0x1', 'eth_syncing': False, 'admin_peers': [{'id': 'same-peer'}]}
        node = SimpleNamespace(name='b', validator_identity=lambda: {'pid': 1, 'start_ticks': 2, 'starts': 1},
                               rpc=lambda service: lambda method: replies[method])
        with self.assertRaisesRegex(ValueError, 'automatic import timeout'):
            matrix.recover(node, 'expiry', [], lambda: None, 1)
        self.assertEqual(saved[0][0].name, 'expiry-recovery-failure.json')
        self.assertEqual(saved[0][1]['observation'], replies)
        self.assertEqual(matrix.report['recovery_failure']['target_height'], 2)

    @staticmethod
    def block(number, height, age, author=MINER, pass_byte=1, snapshot_byte=3):
        payload = (bytes([1]) + (1).to_bytes(2, "big") + height.to_bytes(4, "big") + age.to_bytes(4, "big")
                   + bytes([snapshot_byte]) * 32 + bytes([4]) * 32 + bytes([pass_byte]) * 32 + bytes(4))
        return {"number": hex(number), "hash": f"hash-{number}", "parentHash": f"hash-{number - 1}",
                "miner": author, "extraData": "0x" + payload.hex()}

    def test_chain_checks_anchors_and_distinct_beneficiaries(self):
        passes = {MINER: "01" * 32 + "i0", SECOND_MINER: "02" * 32 + "i0"}
        blocks = [{"number": "0x0", "hash": "hash-0"}, self.block(1, 100, 0),
                  self.block(2, 100, 1, SECOND_MINER, 2), self.block(3, 102, 0)]
        self.assertEqual(validate_chain(blocks, passes), sorted(passes))
        for invalid in (self.block(3, 99, 0), self.block(3, 100, 3), self.block(3, 102, 1),
                        self.block(3, 100, 2, snapshot_byte=5), self.block(3, 102, 0, pass_byte=2)):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                validate_chain(blocks[:-1] + [invalid], passes)
        too_old = [blocks[0]] + [self.block(i + 1, 100, i) for i in range(ANCHOR_MAX_AGE + 2)]
        with self.assertRaises(ValueError):
            validate_chain(too_old, passes)
        with self.assertRaises(ValueError):
            validate_chain(blocks[:1], passes)


class DelayProxyTests(unittest.TestCase):
    def test_delay_preserves_batch_auth_and_does_not_block_historical_reads(self):
        seen = []

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                body = self.rfile.read(int(self.headers["Content-Length"]))
                seen.append((body, self.headers.get("Authorization")))
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with tempfile.TemporaryDirectory() as directory:
                proxy = BlockDelayProxy(0, f"http://127.0.0.1:{server.server_port}", Path(directory) / "audit.jsonl")
                url = f"http://127.0.0.1:{proxy.server.server_port}"

                def call(value):
                    body = json.dumps(value).encode()
                    req = request.Request(url, data=body, headers={"Authorization": "Basic fixture-only"})
                    with request.urlopen(req, timeout=5) as response:
                        return json.load(response)

                try:
                    proxy.hold(["future"])
                    with ThreadPoolExecutor() as pool:
                        batch = [{"id": 7, "method": "getblock", "params": ["future", 0]},
                                 {"id": 8, "method": "getblockhash", "params": [102]}]
                        future = pool.submit(call, batch)
                        deadline = time.monotonic() + 3
                        while not proxy.events and time.monotonic() < deadline:
                            time.sleep(0.01)
                        try:
                            self.assertTrue(proxy.events)
                            self.assertFalse(future.done())
                            historic = {"id": 2, "method": "getblock", "params": ["committed", 0]}
                            self.assertEqual(call(historic), historic)
                        finally:
                            proxy.restore()
                        self.assertEqual(future.result(timeout=3), batch)
                    self.assertEqual(len(seen), 2)
                    self.assertTrue(all(auth == "Basic fixture-only" for _, auth in seen))
                    self.assertEqual(json.loads(seen[-1][0]), batch)
                finally:
                    proxy.close()
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=3)


if __name__ == "__main__":
    unittest.main()
