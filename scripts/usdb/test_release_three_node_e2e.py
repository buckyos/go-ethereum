#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("release_three_node_e2e.py")
SPEC = importlib.util.spec_from_file_location("release_three_node_e2e", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
E2E = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(E2E)


class ReleaseThreeNodeE2ETests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory(prefix="usdb-three-node-e2e-test-")
        self.root = Path(self.temp_dir.name)
        self.manifest_path = self.root / "manifest.json"
        self.manifest_path.write_text(json.dumps(self.manifest()), encoding="utf-8")

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

    @staticmethod
    def manifest() -> dict:
        go_revision = "a" * 40
        usdb_revision = "b" * 40
        return {
            "schema_version": "usdb-release-manifest:v6",
            "release_id": "usdb-testnet-v0-r1",
            "stage": "candidate",
            "qualification": {
                "schema_version": "usdb-ci-qualification:v1",
                "level": "fast",
                "evidence": [{"run_id": 1}],
            },
            "repositories": {
                "go_ethereum": {"revision": go_revision},
                "usdb": {"revision": usdb_revision},
                "source_dao": {"revision": "c" * 40},
            },
            "images": {
                "usdb_services": {
                    "reference": "ghcr.io/buckyos/usdb-services@sha256:" + "d" * 64,
                    "source_revision": usdb_revision,
                },
                "usdb_chain": {
                    "reference": "ghcr.io/buckyos/usdb-chain@sha256:" + "e" * 64,
                    "source_revision": go_revision,
                },
                "bitcoin_core": {
                    "reference": "ghcr.io/buckyos/usdb-bitcoin-core@sha256:" + "f" * 64,
                    "source_revision": usdb_revision,
                },
            },
            "network_bundle": {
                "bundle_id": "usdb-testnet-v0",
                "chain_id": 202608250,
                "network_id": 202608250,
                "genesis_block_hash": "0x" + "1" * 64,
            },
            "snapshot": {
                "status": "available",
                "record": {
                    "url": "https://snapshots.example.test/snapshot-records/v2/"
                    + "2" * 64
                    + ".json",
                    "sha256": "2" * 64,
                },
                "snapshot_release_id": "balance-history-bitcoin-h963800-0123456789abcdef",
                "height": 963800,
            },
        }

    def test_plan_preserves_canonical_refs_without_mirror(self) -> None:
        plan = E2E.build_plan(self.manifest_path, None)
        image = plan["images"]["usdb_chain"]
        self.assertEqual(image["canonical_reference"], image["execution_reference"])
        self.assertEqual(plan["network"]["chain_id"], 202608250)
        self.assertEqual(plan["snapshot"]["height"], 963800)
        self.assertEqual(plan["qualification_level"], "fast")

    def test_local_mirror_changes_only_transport_registry(self) -> None:
        plan = E2E.build_plan(self.manifest_path, "127.0.0.1:5000")
        image = plan["images"]["usdb_services"]
        self.assertEqual(
            image["execution_reference"],
            "127.0.0.1:5000/buckyos/usdb-services@sha256:" + "d" * 64,
        )
        self.assertEqual(image["digest"], "sha256:" + "d" * 64)

    def test_mutable_image_and_source_mismatch_are_rejected(self) -> None:
        manifest = self.manifest()
        manifest["images"]["usdb_chain"]["reference"] = "ghcr.io/buckyos/usdb-chain:latest"
        self.manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "must use ghcr.io"):
            E2E.build_plan(self.manifest_path, None)

        manifest = self.manifest()
        manifest["images"]["usdb_chain"]["source_revision"] = "9" * 40
        self.manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "source revision mismatch"):
            E2E.build_plan(self.manifest_path, None)

    def test_snapshot_record_digest_mismatch_is_rejected(self) -> None:
        manifest = self.manifest()
        manifest["snapshot"]["record"]["sha256"] = "3" * 64
        self.manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "record URL is invalid"):
            E2E.build_plan(self.manifest_path, None)

    def test_duplicate_manifest_key_is_rejected(self) -> None:
        self.manifest_path.write_text(
            '{"schema_version":"a","schema_version":"b"}', encoding="utf-8"
        )
        with self.assertRaisesRegex(ValueError, "duplicate JSON key"):
            E2E.build_plan(self.manifest_path, None)

    def test_missing_qualification_is_rejected(self) -> None:
        manifest = self.manifest()
        del manifest["qualification"]
        self.manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "qualification is required"):
            E2E.build_plan(self.manifest_path, None)

    def test_node_env_must_use_manifest_canonical_refs(self) -> None:
        plan = E2E.build_plan(self.manifest_path, "localhost:5000")
        env_path = self.root / "node.env"
        env_path.write_text(
            "\n".join(
                f"{entry['env_key']}={entry['canonical_reference']}"
                for entry in plan["images"].values()
            )
            + "\n",
            encoding="utf-8",
        )
        E2E.validate_node_env(plan, env_path)
        env_path.write_text(
            env_path.read_text(encoding="utf-8").replace(
                "ghcr.io/buckyos/usdb-chain@", "ghcr.io/buckyos/usdb-chain:latest#"
            ),
            encoding="utf-8",
        )
        with self.assertRaisesRegex(ValueError, "USDB_CHAIN_IMAGE"):
            E2E.validate_node_env(plan, env_path)

    def test_rendered_node_env_is_isolated_and_secret_preserving(self) -> None:
        plan = E2E.build_plan(self.manifest_path, None)
        base = self.root / "base.env"
        base.write_text(
            "BTC_RPC_PASSWORD=private-value\nUSDB_MINER_ADDRESS=0x1234\n",
            encoding="utf-8",
        )
        output = self.root / "node2.env"
        E2E.render_node_env(plan, base, output, 2, "full")
        rendered = E2E.load_env(output)
        self.assertEqual(rendered["BTC_RPC_PASSWORD"], "private-value")
        self.assertEqual(rendered["USDB_NODE_ROLE"], "full")
        self.assertEqual(rendered["USDB_HTTP_BIND_PORT"], "28545")
        self.assertEqual(rendered["BTC_P2P_BIND_PORT"], "38334")
        self.assertEqual(output.stat().st_mode & 0o777, 0o600)

    def test_image_inspect_binds_digest_and_revision(self) -> None:
        plan = E2E.build_plan(self.manifest_path, "localhost:5000")
        image = plan["images"]["usdb_chain"]
        inspect_path = self.root / "inspect.json"
        inspect_path.write_text(
            json.dumps(
                [
                    {
                        "RepoDigests": [image["execution_reference"]],
                        "Config": {"Labels": {"commit": image["source_revision"]}},
                    }
                ]
            ),
            encoding="utf-8",
        )
        E2E.verify_image_inspect(plan, "usdb_chain", inspect_path)
        value = json.loads(inspect_path.read_text(encoding="utf-8"))
        value[0]["Config"]["Labels"]["commit"] = "0" * 40
        inspect_path.write_text(json.dumps(value), encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "source revision label mismatch"):
            E2E.verify_image_inspect(plan, "usdb_chain", inspect_path)

    def test_local_lock_updates_only_dependency_revisions(self) -> None:
        base = self.root / "ci-revisions.json"
        base.write_text(
            json.dumps(
                {
                    "schema_version": "usdb-ci-revisions:v2",
                    "coordinator": {"repository": "buckyos/go-ethereum"},
                    "dependencies": {
                        "usdb": {"revision": "1" * 40},
                        "source_dao": {"revision": "2" * 40},
                    },
                    "toolchains": {"go": "1.18.5"},
                }
            ),
            encoding="utf-8",
        )
        output = self.root / "local-lock.json"
        E2E.write_local_compatibility_lock(base, output, "3" * 40, "4" * 40)
        value = E2E.load_json(output)
        self.assertEqual(value["dependencies"]["usdb"]["revision"], "3" * 40)
        self.assertEqual(value["dependencies"]["source_dao"]["revision"], "4" * 40)
        self.assertEqual(value["toolchains"], {"go": "1.18.5"})


if __name__ == "__main__":
    unittest.main()
