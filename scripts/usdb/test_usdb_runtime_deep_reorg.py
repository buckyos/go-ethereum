#!/usr/bin/env python3
"""Keep the existing CI entrypoint for runtime acceptance tests."""
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "tests"))
from test_runtime_deep_reorg import RuntimeDeepReorgTest


if __name__ == "__main__":
    unittest.main()
