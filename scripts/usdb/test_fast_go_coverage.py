#!/usr/bin/env python3

from __future__ import annotations

import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest

from check_fast_go_coverage import REQUIRED_TESTS, check_coverage


ROOT = Path(__file__).resolve().parents[2]


class FastGoCoverageTests(unittest.TestCase):
    def setUp(self):
        self.required = {"example/consensus": ["TestEnergy", "TestPrice"]}

    def events(self, names=("TestEnergy", "TestPrice"), package="example/consensus"):
        events = []
        for name in names:
            events.extend([
                {"Action": "run", "Package": package, "Test": name},
                {"Action": "pass", "Package": package, "Test": name},
            ])
        events.append({"Action": "pass", "Package": package})
        return events

    def check(self, events):
        return check_coverage(self.required, (json.dumps(event) for event in events))

    def test_accepts_completed_tests_and_package(self):
        self.assertEqual(self.check(self.events()), 2)

    def test_filtered_or_renamed_tests_do_not_satisfy_manifest(self):
        for names in ((), ("TestEnergy",), ("TestEnergy", "TestPriceRenamed")):
            with self.subTest(names=names), self.assertRaisesRegex(ValueError, "TestPrice"):
                self.check(self.events(names))

    def test_skip_or_failure_cannot_count_as_success_even_after_a_pass(self):
        for action in ("skip", "fail"):
            events = self.events()
            events.insert(-1, {"Package": "example/consensus", "Test": "TestPrice", "Action": action})
            with self.subTest(action=action), self.assertRaisesRegex(ValueError, "TestPrice"):
                self.check(events)

    def test_subtests_and_other_packages_cannot_satisfy_top_level_test(self):
        for events in (
            self.events(("TestEnergy", "TestPrice/subtest")),
            self.events(package="another/consensus"),
        ):
            with self.subTest(events=events), self.assertRaises(ValueError):
                self.check(events)

    def test_pass_without_run_and_truncated_report_are_rejected(self):
        for events in (
            [event for event in self.events() if event["Action"] != "run"],
            self.events()[:-1],
            [],
        ):
            with self.subTest(events=events), self.assertRaises(ValueError):
                self.check(events)

    def test_failed_or_skipped_package_is_rejected(self):
        for action in ("skip", "fail"):
            with self.subTest(action=action), self.assertRaisesRegex(ValueError, "packages="):
                self.check(self.events() + [{"Package": "example/consensus", "Action": action}])

    def test_invalid_manifest_and_report_fail_closed(self):
        for required in ({}, [], {"p": []}, {"p": ["TestA", "TestA"]}, {"p": [None]}):
            with self.subTest(required=required), self.assertRaises(ValueError):
                check_coverage(required, [])
        for line in ("not json", "[]", '{"Action":"pass","Package":[]}'):
            with self.subTest(line=line), self.assertRaises(ValueError):
                check_coverage(self.required, [line])

    def test_cli_returns_failure_for_missing_test(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            manifest = root / "required.json"
            report = root / "report.jsonl"
            manifest.write_text(json.dumps(self.required))
            report.write_text("\n".join(json.dumps(e) for e in self.events(("TestEnergy",))))
            result = subprocess.run([
                sys.executable, str(Path(__file__).with_name("check_fast_go_coverage.py")),
                "--required", str(manifest), "--report", str(report),
            ], capture_output=True, text=True)
            self.assertEqual(result.returncode, 1)
            self.assertIn("TestPrice", result.stderr)

    def test_critical_test_families_are_registered_and_selected(self):
        package = "github.com/ethereum/go-ethereum/consensus/ethash"
        required = set(json.loads(REQUIRED_TESTS.read_text())[package])
        source_names = set()
        for source in (ROOT / "consensus/ethash").glob("*_test.go"):
            source_names.update(re.findall(r"^func (Test\w+)\(t \*testing\.T\)", source.read_text(), re.MULTILINE))
        families = {name for name in source_names if name.startswith((
            "TestPrepareKTransition", "TestPrepareFixedPriceTransition",
            "TestVerifyHeaderUsesExpectedVersionAtActivationBoundary",
        ))}
        self.assertTrue(families)
        self.assertLessEqual(families, required, "register new critical tests in fast_go_required_tests.json")
        self.assertLessEqual(required, source_names, "update the manifest when renaming critical tests")
        runner = (ROOT / "scripts/usdb/run_fast_ci.sh").read_text()
        pattern = re.search(r"local consensus_tests='([^']+)'", runner).group(1)
        self.assertEqual([name for name in required if not re.search(pattern, name)], [])
        self.assertEqual(re.findall(r'^\s+run_consensus_checks "\$consensus_tests"(.*)$', runner, re.MULTILINE), [
            "", " usdb_activation_conformance", " usdb_economic_conformance_v2",
            " usdb_economic_conformance_v3",
        ])


if __name__ == "__main__":
    unittest.main()
