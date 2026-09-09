#!/usr/bin/env python3
"""Retired public tags fail before discovering or modifying node repositories."""
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / "scripts/usdb/prepare_release.py"


class RetiredPublicReleaseTests(unittest.TestCase):
    def test_old_public_and_explorer_tags_report_the_new_repository_without_discovery(self):
        with tempfile.TemporaryDirectory() as temporary:
            for tag in ("usdb-public-v0.1.0", "usdb-public-v0.2.0-rc.1", "usdb-explorer-v0.2.0"):
                result = subprocess.run([sys.executable, "-B", str(SCRIPT), "--workspace-root", temporary,
                                         "tag", "--release-id", tag, "--create", "--push"],
                                        text=True, capture_output=True, timeout=10)
                with self.subTest(tag=tag):
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("Explorer releases moved to buckyos/usdb-explorer", result.stderr)
                    self.assertEqual(list(Path(temporary).iterdir()), [])


if __name__ == "__main__":
    unittest.main()
