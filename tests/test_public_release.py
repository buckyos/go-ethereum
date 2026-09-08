#!/usr/bin/env python3
"""Exercise the public tag CLI against temporary repositories and local origins."""
from __future__ import annotations

import os
import pathlib
import shutil
import subprocess
import sys
import unittest

from common.release_workspace import PREPARE, ReleaseWorkspaceFixture


class PublicReleaseTests(ReleaseWorkspaceFixture):
    release_id = "usdb-public-v0.1.0"

    def setUp(self) -> None:
        super().setUp()
        self.script = self.go / "scripts/usdb/prepare_release.py"
        for name in ("prepare_release.py", "ci_revisions.py"):
            shutil.copyfile(pathlib.Path(PREPARE.__file__).parent / name, self.script.parent / name)
        self.workflow = self.usdb / ".github/workflows/usdb-public-release.yml"
        self.workflow.parent.mkdir(parents=True)
        self.workflow.write_text('on:\n  push:\n    tags: ["usdb-public-v*"]\n')
        self.packager = self.usdb / "public-services/package_release.py"
        self.packager.parent.mkdir()
        self.packager.write_text(
            'import sys\nassert sys.argv[1:] == ["--check-network"]\n'
            'print("Public network catalog matches the canonical bundle")\n'
        )
        self.publish_inputs()

    def publish_inputs(self) -> None:
        self.run_git(self.usdb, "add", ".")
        self.run_git(self.usdb, "commit", "-m", "Update public release fixture")
        self.run_git(self.usdb, "push", "origin", "master")

    def cli(self, *flags: str, release_id: str | None = None,
            strict_remotes: bool = False) -> subprocess.CompletedProcess[str]:
        # Relax only the GitHub origin spelling for local bare-repository tests.
        # The strict case executes the unmodified entry point as an operator does.
        entry = [str(self.script)] if strict_remotes else ["-c", (
            "import sys; from functools import partial; "
            f"sys.path.insert(0, {str(self.script.parent)!r}); "
            "import prepare_release as p; "
            "p.discover_workspace = partial(p.discover_workspace, strict_remotes=False); "
            "raise SystemExit(p.main())"
        )]
        return subprocess.run(
            [sys.executable, "-B", *entry, "tag", "--release-id",
             release_id or self.release_id, *flags],
            cwd=self.go,
            capture_output=True,
            text=True,
            env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"},
        )

    def assert_no_tags(self) -> None:
        for repository in (self.usdb, self.usdb_remote, self.go, self.go_remote):
            self.assertEqual(self.run_git(repository, "tag", "--list"), "")

    def test_dry_run_needs_no_source_dao_or_valid_go_lock(self) -> None:
        self.source_dao.rename(self.workspace / "unavailable-source-dao")
        self.lock_path.write_text("not a compatibility lock\n")
        before = self.run_git(self.go, "status", "--porcelain")
        result = self.cli()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("dry run", result.stdout)
        self.assertIn("release_family=public", result.stdout)
        self.assertIn("Public network catalog matches", result.stdout)
        self.assertEqual(self.run_git(self.go, "status", "--porcelain"), before)
        self.assert_no_tags()

    def test_create_and_push_only_tags_published_usdb_commit(self) -> None:
        before = self.lock_path.read_bytes()
        revision = self.run_git(self.usdb, "rev-parse", "HEAD")
        result = self.cli("--create", "--push")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("pushed_usdb_tag=true", result.stdout)
        self.assertIn("usdb-public-release.yml", result.stdout)
        ref = f"refs/tags/{self.release_id}"
        for repository in (self.usdb, self.usdb_remote):
            self.assertEqual(self.run_git(repository, "cat-file", "-t", ref), "tag")
            self.assertEqual(self.run_git(repository, "rev-parse", ref + "^{}"), revision)
        for repository in (self.go, self.go_remote, self.source_dao):
            self.assertEqual(self.run_git(repository, "tag", "--list"), "")
        self.assertEqual(self.lock_path.read_bytes(), before)

    def test_create_without_push_keeps_remote_unchanged(self) -> None:
        result = self.cli("--create")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.run_git(self.usdb, "tag", "--list"), self.release_id)
        self.assertEqual(self.run_git(self.usdb_remote, "tag", "--list"), "")

    def test_existing_local_tag_is_not_replaced_or_pushed(self) -> None:
        result = self.cli("--create")
        self.assertEqual(result.returncode, 0, result.stderr)
        ref = f"refs/tags/{self.release_id}"
        before = self.run_git(self.usdb, "rev-parse", ref)
        result = self.cli("--create", "--push")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("already exists locally", result.stderr)
        self.assertEqual(self.run_git(self.usdb, "rev-parse", ref), before)
        self.assertEqual(self.run_git(self.usdb_remote, "tag", "--list"), "")

    def test_existing_remote_tag_is_rejected_even_without_local_copy(self) -> None:
        result = self.cli("--create", "--push")
        self.assertEqual(result.returncode, 0, result.stderr)
        ref = f"refs/tags/{self.release_id}"
        before = self.run_git(self.usdb_remote, "rev-parse", ref)
        self.run_git(self.usdb, "tag", "-d", self.release_id)
        result = self.cli("--no-fetch", "--create", "--push")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("already exists on usdb origin", result.stderr)
        self.assertEqual(self.run_git(self.usdb_remote, "rev-parse", ref), before)
        self.assertEqual(self.run_git(self.usdb, "tag", "--list"), "")

    def test_dirty_usdb_is_rejected(self) -> None:
        (self.usdb / "operator-config").write_text("uncommitted\n")
        result = self.cli("--create", "--push")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("usdb worktree is not clean", result.stderr)
        self.assert_no_tags()

    def test_unpublished_usdb_head_is_rejected(self) -> None:
        (self.usdb / "README").write_text("new revision\n")
        self.run_git(self.usdb, "commit", "-am", "Unpublished fixture")
        result = self.cli("--create", "--push")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("HEAD is not origin/master", result.stderr)
        self.assert_no_tags()

    def test_non_master_branch_and_wrong_origin_are_rejected(self) -> None:
        self.run_git(self.usdb, "checkout", "-b", "unreviewed")
        result = self.cli("--create")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must be on master", result.stderr)
        self.run_git(self.usdb, "checkout", "master")
        result = self.cli("--create", strict_remotes=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("usdb origin mismatch", result.stderr)
        self.assert_no_tags()

    def test_version_formats_and_push_requires_create(self) -> None:
        result = self.cli(release_id="usdb-public-v1.2.3-rc.1")
        self.assertEqual(result.returncode, 0, result.stderr)
        for invalid in ("usdb-public-v1", "usdb-public-v1.2", "usdb-public-v1.2.3-rC",
                        "usdb-public-v1.2.3/branch", "usdb-public-v1.2.3\n", "usdb-public-v1.2.3;id"):
            with self.subTest(invalid=invalid):
                result = self.cli("--create", "--push", release_id=invalid)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("release ID must use", result.stderr)
        result = self.cli("--push")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("--push requires --create", result.stderr)
        self.assert_no_tags()

    def test_missing_release_workflow_is_rejected_before_tagging(self) -> None:
        self.workflow.unlink()
        self.publish_inputs()
        result = self.cli("--create", "--push")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("usdb-public-release.yml", result.stderr)
        self.assert_no_tags()

    def test_failed_network_catalog_check_is_rejected_before_tagging(self) -> None:
        self.packager.write_text('raise SystemExit("public network catalog differs")\n')
        self.publish_inputs()
        result = self.cli("--create", "--push")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("public network catalog differs", result.stderr)
        self.assert_no_tags()

    def test_failed_push_preserves_tag_and_reports_exact_retry(self) -> None:
        hook = self.usdb_remote / "hooks/pre-receive"
        hook.write_text("#!/bin/sh\nexit 1\n")
        hook.chmod(0o755)
        result = self.cli("--create", "--push")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(f"git push origin refs/tags/{self.release_id}", result.stderr)
        self.assertEqual(self.run_git(self.usdb, "tag", "--list"), self.release_id)
        self.assertEqual(self.run_git(self.usdb_remote, "tag", "--list"), "")


if __name__ == "__main__":
    unittest.main()
