#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT = Path(__file__).resolve().parent / "docker" / "usdb_deep_reorg_guard.py"
SPEC = importlib.util.spec_from_file_location("usdb_deep_reorg_guard", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
GUARD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GUARD)


class DeepReorgGuardTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.state_dir = Path(self.temp.name)
        self.guard = GUARD.DeepReorgGuard(
            self.state_dir,
            "http://indexer.test",
            "http://chain.test",
            1.0,
        )

    def tearDown(self) -> None:
        self.temp.cleanup()

    def readiness(self, epoch: int) -> tuple[int, dict[str, object]]:
        return epoch, {
            "service": "usdb-indexer",
            "upstream_reorg_epoch": epoch,
            "consensus_ready": True,
        }

    @mock.patch.object(GUARD, "read_reorg_epoch")
    def test_initializes_and_reuses_baseline(self, read_epoch: mock.Mock) -> None:
        read_epoch.return_value = self.readiness(7)
        self.assertEqual(self.guard.check_once(), 7)
        self.assertEqual(self.guard.check_once(), 7)
        baseline = GUARD.load_json(self.guard.baseline_path)
        self.assertEqual(baseline["schema_version"], GUARD.BASELINE_SCHEMA)
        self.assertEqual(baseline["upstream_reorg_epoch"], 7)
        self.assertFalse(self.guard.incident_path.exists())

    @mock.patch.object(GUARD, "read_chain_head", return_value={"number": "0x9"})
    @mock.patch.object(GUARD, "read_reorg_epoch")
    def test_epoch_advance_persists_incident(
        self, read_epoch: mock.Mock, _read_head: mock.Mock
    ) -> None:
        read_epoch.side_effect = [self.readiness(2), self.readiness(3)]
        self.guard.check_once()
        with self.assertRaisesRegex(GUARD.GuardError, "reorg detected"):
            self.guard.check_once()
        incident = GUARD.load_json(self.guard.incident_path)
        self.assertEqual(incident["schema_version"], GUARD.INCIDENT_SCHEMA)
        self.assertEqual(incident["baseline_epoch"], 2)
        self.assertEqual(incident["observed_epoch"], 3)
        self.assertEqual(incident["reason"], "upstream_reorg_epoch_advanced")
        self.assertEqual(incident["usdb_chain_head"], {"number": "0x9"})

    @mock.patch.object(GUARD, "read_chain_head", return_value=None)
    @mock.patch.object(GUARD, "read_reorg_epoch")
    def test_epoch_regression_fails_closed(
        self, read_epoch: mock.Mock, _read_head: mock.Mock
    ) -> None:
        read_epoch.side_effect = [self.readiness(8), self.readiness(0)]
        self.guard.check_once()
        with self.assertRaisesRegex(GUARD.GuardError, "baseline_epoch=8 observed_epoch=0"):
            self.guard.check_once()
        incident = GUARD.load_json(self.guard.incident_path)
        self.assertEqual(incident["reason"], "upstream_reorg_epoch_regressed")

    @mock.patch.object(GUARD, "read_reorg_epoch")
    def test_persisted_incident_blocks_restart_without_rpc(self, read_epoch: mock.Mock) -> None:
        GUARD.atomic_write_json(
            self.guard.incident_path,
            {
                "schema_version": GUARD.INCIDENT_SCHEMA,
                "observed_epoch": 9,
            },
        )
        with self.assertRaisesRegex(GUARD.GuardError, "persisted"):
            self.guard.check_once()
        read_epoch.assert_not_called()


if __name__ == "__main__":
    unittest.main()
