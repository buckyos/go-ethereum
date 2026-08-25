#!/usr/bin/env python3

import pathlib
import re
import unittest


ROOT_DIR = pathlib.Path(__file__).resolve().parents[2]


class P2PDefaultsTests(unittest.TestCase):
    def assert_default(self, relative_path: str, variable: str, port: int) -> None:
        content = (ROOT_DIR / relative_path).read_text(encoding="utf-8")
        pattern = rf"^{re.escape(variable)}=\$\{{{re.escape(variable)}:-{port}\}}$"
        self.assertRegex(content, re.compile(pattern, re.MULTILINE))

    def test_usdb_code_and_single_node_scripts_use_canonical_port(self) -> None:
        flags = (ROOT_DIR / "cmd/utils/flags.go").read_text(encoding="utf-8")
        self.assertRegex(flags, r"(?m)^const usdbDefaultP2PPort = 31303$")

        self.assert_default("scripts/usdb/run_devnet_node.sh", "P2P_PORT", 31303)
        self.assert_default("scripts/usdb/run_local_bootstrap_smoke.sh", "P2P_PORT", 31303)
        self.assert_default(
            "scripts/usdb/run_local_two_node_network.sh", "NODE1_P2P_PORT", 31303
        )

    def test_second_local_node_uses_explicit_non_conflicting_port(self) -> None:
        self.assert_default(
            "scripts/usdb/run_local_two_node_network.sh", "NODE2_P2P_PORT", 31304
        )


if __name__ == "__main__":
    unittest.main()
