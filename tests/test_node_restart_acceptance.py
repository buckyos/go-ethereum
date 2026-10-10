#!/usr/bin/env python3
"""Negative evidence and isolation tests for the real deployment acceptance."""

import copy
import json
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import unittest

from common.deployment_fixture import read_fixture, validate_progress, validate_recovery, verify_mount_isolation


class RestartAcceptanceTests(unittest.TestCase):
    def evidence(self):
        before = dict(chain_id="0x123", genesis_hash="genesis", historical_chain_hash="old-block",
                      balance_ref={"commit": "bh"}, indexer_ref={"commit": "indexer"}, epoch=0,
                      identities=["nodekey-sha"], stable_height=100, chain_height=20,
                      containers={"bitcoin": "old-btc", "chain": "old-chain"})
        after = copy.deepcopy(before)
        after.update(stable_height=101, chain_height=21, containers={"bitcoin": "new-btc", "chain": "new-chain"})
        return before, after

    def test_recovery_requires_history_epoch_identity_and_new_work(self):
        before, after = self.evidence()
        validate_recovery(before, after)
        mutations = {"epoch": 1, "identities": ["new-key"], "balance_ref": {"commit": "changed"},
                     "indexer_ref": {"commit": "changed"}, "chain_id": "wrong", "genesis_hash": "wrong",
                     "historical_chain_hash": "wrong", "stable_height": 100, "chain_height": 20,
                     "containers": before["containers"]}
        for field, value in mutations.items():
            with self.subTest(field=field), self.assertRaises(ValueError):
                validate_recovery(before, {**after, field: value})

    def test_missing_service_cannot_pass_recovery(self):
        before, after = self.evidence()
        after["containers"].pop("bitcoin")
        with self.assertRaisesRegex(ValueError, "service set"):
            validate_recovery(before, after)

    def test_cached_or_incomplete_progress_is_not_recovery(self):
        from datetime import datetime, timezone
        progress = {"observed_at": datetime.fromtimestamp(2000, timezone.utc).isoformat(),
                    "controller_state": "idle", "components": [dict(id=name, state="READY") for name in
                    ("bitcoin", "balance_history", "usdb_indexer", "usdb_chain")]}
        validate_progress(progress, 1000)
        with self.assertRaisesRegex(ValueError, "predates"):
            validate_progress(progress, 3000)
        progress["components"][0]["last_observed_at"] = "old"
        with self.assertRaisesRegex(ValueError, "freshly ready"):
            validate_progress(progress, 1000)
        progress["components"] = []
        with self.assertRaisesRegex(ValueError, "missing"):
            validate_progress(progress, 1000)

    def test_shared_data_and_external_writable_mount_are_rejected(self):
        fixture = {"data_root": "/tmp/fixture/data", "node_env": "/tmp/fixture/config/node.env"}
        node = {"Config": {"Labels": {"com.docker.compose.project": "test"}},
                "Mounts": [{"Source": "/tmp/fixture/data/btc", "RW": True, "Type": "bind"}]}
        self.assertEqual(verify_mount_isolation([node], {"test"}, fixture), [node])
        foreign = copy.deepcopy(node)
        foreign["Config"]["Labels"]["com.docker.compose.project"] = "production"
        with self.assertRaisesRegex(ValueError, "another Docker project"):
            verify_mount_isolation([node, foreign], {"test"}, fixture)
        node["Mounts"][0]["Source"] = "/data/production"
        with self.assertRaisesRegex(ValueError, "outside declared"):
            verify_mount_isolation([node], {"test"}, fixture)

    def test_fixture_rejects_wrong_host_and_symlink_escape(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            data = root / "data"
            data.mkdir()
            key = data / "nodekey"
            key.write_text("fixture-key")
            env = root / "node.env"
            env.touch()
            fixture = dict(schema="usdb-node-deployment-fixture:v1", dedicated_test_node=True,
                           hostname=socket.gethostname(), launcher=str(env), kit_root=str(root),
                           node_env=str(env), data_root=str(data), identity_files=[str(key)],
                           balance_history_rpc="http://127.0.0.1:19110", indexer_rpc="http://127.0.0.1:19120",
                           chain_rpc="http://127.0.0.1:18545")
            path = root / "fixture.json"
            path.write_text(json.dumps(fixture))
            read_fixture(path)
            fixture["hostname"] = "wrong-host"
            path.write_text(json.dumps(fixture))
            with self.assertRaisesRegex(ValueError, "another host"):
                read_fixture(path)
            fixture["hostname"] = socket.gethostname()
            key.unlink()
            key.symlink_to(env)
            path.write_text(json.dumps(fixture))
            with self.assertRaisesRegex(ValueError, "identity"):
                read_fixture(path)

    def test_default_plan_never_invokes_the_node_launcher(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            data = root / "data"
            data.mkdir()
            (data / "nodekey").write_text("test-key")
            env = root / "node.env"
            env.touch()
            marker = root / "unexpected-launch"
            launcher = root / "usdb-node"
            launcher.write_text(f"#!{sys.executable}\nfrom pathlib import Path\nPath({str(marker)!r}).touch()\n")
            launcher.chmod(0o700)
            path = root / "fixture.json"
            path.write_text(json.dumps(dict(schema="usdb-node-deployment-fixture:v1", dedicated_test_node=True,
                hostname=socket.gethostname(), launcher=str(launcher), kit_root=str(root), node_env=str(env),
                data_root=str(data), identity_files=[str(data / "nodekey")],
                balance_history_rpc="http://127.0.0.1:19110", indexer_rpc="http://127.0.0.1:19120",
                chain_rpc="http://127.0.0.1:18545")))
            result = subprocess.run([sys.executable, str(Path(__file__).with_name("node_deployment_restart.py")),
                                     "--fixture", str(path), "--output", str(root / "result")],
                                    capture_output=True, text=True, timeout=5)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads(result.stdout)["mode"], "plan")
            self.assertFalse(marker.exists())
            self.assertFalse((root / "result").exists())


if __name__ == "__main__":
    unittest.main()
