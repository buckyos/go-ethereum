#!/usr/bin/env python3
"""Exercise query policy through the actual container entrypoint."""
import json
import unittest

from common.runtime_guard import RuntimeGuardFixture


class RuntimeQueryModeTest(RuntimeGuardFixture):
    def test_role_and_query_policy_are_independent(self) -> None:
        cases = [
            ({}, "full", False, False),
            ({"USDB_NODE_ROLE": "miner", "USDB_MINER_ADDRESS": "0x" + "12" * 20}, "full", False, True),
            ({"USDB_CHAIN_GCMODE": "archive", "USDB_CHAIN_TRACING": "1"}, "archive", True, False),
            ({"USDB_CHAIN_GCMODE": "full", "USDB_CHAIN_TRACING": "1"}, "full", True, False),
            ({"USDB_CHAIN_EXTRA_ARGS": "--gcmode archive --cache 512"}, "archive", False, False),
            ({"USDB_CHAIN_GCMODE": "archive", "USDB_CHAIN_EXTRA_ARGS": "--gcmode=archive"}, "archive", False, False),
        ]
        for index, (settings, mode, debug, mining) in enumerate(cases):
            with self.subTest(settings=settings):
                data = self.prepare_data_dir(str(index))
                marker = (data / "bootstrap/usdb-init.done.json").read_bytes()
                runtime = self.start_runtime(data, USDB_DEEP_REORG_GUARD_ENABLED="0", **settings)
                self.wait_for(lambda: self.line_count(self.starts) == index + 1, "geth did not start")
                argv = json.loads(self.events.read_text().splitlines()[-1])["argv"]
                self.assertEqual(argv.count("--gcmode"), 1)
                self.assertEqual(argv[argv.index("--gcmode") + 1], mode)
                self.assertEqual("debug" in argv[argv.index("--http.api") + 1].split(","), debug)
                self.assertNotIn("debug", argv[argv.index("--ws.api") + 1].split(","))
                self.assertEqual("--mine" in argv, mining)
                self.assertEqual((data / "bootstrap/usdb-init.done.json").read_bytes(), marker)
                self.assert_group_stopped(runtime)

    def test_invalid_policy_and_namespace_bypasses_fail_before_geth(self) -> None:
        cases = [
            {"USDB_CHAIN_GCMODE": "snap"}, {"USDB_CHAIN_TRACING": "true"},
            {"USDB_CHAIN_EXTRA_ARGS": "--gcmode"},
            {"USDB_CHAIN_EXTRA_ARGS": "--gcmode=archive --gcmode full"},
            {"USDB_CHAIN_GCMODE": "full", "USDB_CHAIN_EXTRA_ARGS": "--gcmode archive"},
            {"USDB_HTTP_APIS": "eth, debug"}, {"USDB_WS_APIS": "eth,debug", "USDB_CHAIN_TRACING": "1"},
            {"USDB_HTTP_APIS": " , "}, {"USDB_WS_APIS": " "},
            {"USDB_CHAIN_EXTRA_ARGS": "--ws.api=eth,debug"},
            {"USDB_CHAIN_EXTRA_ARGS": "--http.api eth,debug"},
        ]
        for index, settings in enumerate(cases):
            with self.subTest(settings=settings):
                runtime = self.start_runtime(self.prepare_data_dir(str(index)), **settings)
                self.assertNotEqual(runtime.wait(timeout=3), 0)
                self.assertEqual(self.line_count(self.starts), 0)


if __name__ == "__main__":
    unittest.main()
