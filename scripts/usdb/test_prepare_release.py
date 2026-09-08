#!/usr/bin/env python3

from __future__ import annotations

import pathlib
import sys
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[2] / "tests"))
from common.release_workspace import PREPARE, ReleaseWorkspaceFixture


class PrepareReleaseTests(ReleaseWorkspaceFixture):
    def test_default_workspace_is_parent_of_go_checkout(self) -> None:
        script = self.go / "scripts/usdb/prepare_release.py"
        self.assertEqual(PREPARE.default_workspace_root(script), self.workspace)

    def test_sync_lock_dry_run_does_not_modify_repository(self) -> None:
        before = self.lock_path.read_text(encoding="utf-8")
        PREPARE.sync_lock(
            self.repositories,
            commit=False,
            push=False,
            fetch=False,
        )
        self.assertEqual(self.lock_path.read_text(encoding="utf-8"), before)
        self.assertEqual(self.run_git(self.go, "status", "--porcelain"), "")

    def test_sync_lock_commit_then_create_coordinated_tags(self) -> None:
        usdb_revision = self.run_git(self.usdb, "rev-parse", "HEAD")
        PREPARE.sync_lock(
            self.repositories,
            commit=True,
            push=True,
            fetch=False,
        )
        lock = PREPARE.ci_revisions.load_lock(self.lock_path)
        self.assertEqual(lock["dependencies"]["usdb"]["revision"], usdb_revision)
        self.assertEqual(
            self.run_git(self.go, "log", "-1", "--pretty=%s"),
            "Update USDB CI revision lock",
        )
        release_id = "usdb-testnet-v0-r1"
        PREPARE.create_release_tags(
            self.repositories,
            release_id=release_id,
            create=True,
            push=True,
            fetch=False,
        )
        self.assertEqual(
            self.run_git(self.usdb, "cat-file", "-t", f"refs/tags/{release_id}"),
            "tag",
        )
        self.assertEqual(
            self.run_git(self.go, "cat-file", "-t", f"refs/tags/{release_id}"),
            "tag",
        )
        self.assertIn(
            f"refs/tags/{release_id}",
            self.run_git(self.usdb_remote, "show-ref", "--tags"),
        )
        self.assertIn(
            f"refs/tags/{release_id}",
            self.run_git(self.go_remote, "show-ref", "--tags"),
        )

    def test_sync_lock_updates_published_source_dao_head(self) -> None:
        usdb_revision = self.run_git(self.usdb, "rev-parse", "HEAD")
        initial_lock = PREPARE.ci_revisions.load_lock(self.lock_path)
        PREPARE.ci_revisions.set_dependency_revision(
            initial_lock,
            "usdb",
            usdb_revision,
        )
        PREPARE.ci_revisions.write_lock(self.lock_path, initial_lock)
        self.run_git(self.go, "add", "scripts/usdb/ci-revisions.json")
        self.run_git(self.go, "commit", "-m", "Pin USDB fixture")
        self.run_git(self.go, "push", "origin", "master")

        (self.source_dao / "CHANGELOG").write_text(
            "SourceDAO release change\n",
            encoding="utf-8",
        )
        self.run_git(self.source_dao, "add", "CHANGELOG")
        self.run_git(self.source_dao, "commit", "-m", "Update SourceDAO fixture")
        self.run_git(self.source_dao, "push", "origin", "main")
        source_dao_revision = self.run_git(self.source_dao, "rev-parse", "HEAD")

        PREPARE.sync_lock(
            self.repositories,
            commit=True,
            push=False,
            fetch=False,
        )

        lock = PREPARE.ci_revisions.load_lock(self.lock_path)
        self.assertEqual(lock["dependencies"]["usdb"]["revision"], usdb_revision)
        self.assertEqual(
            lock["dependencies"]["source_dao"]["revision"],
            source_dao_revision,
        )

    def test_resume_push_rejects_newer_source_dao_head(self) -> None:
        PREPARE.sync_lock(
            self.repositories,
            commit=True,
            push=False,
            fetch=False,
        )
        (self.source_dao / "CHANGELOG").write_text(
            "SourceDAO changed after lock\n",
            encoding="utf-8",
        )
        self.run_git(self.source_dao, "add", "CHANGELOG")
        self.run_git(self.source_dao, "commit", "-m", "Advance SourceDAO fixture")
        self.run_git(self.source_dao, "push", "origin", "main")

        with self.assertRaisesRegex(
            PREPARE.ReleasePreparationError,
            "both published dependency HEADs",
        ):
            PREPARE.sync_lock(
                self.repositories,
                commit=False,
                push=True,
                fetch=False,
            )

    def test_sync_lock_commit_can_resume_push(self) -> None:
        remote_before = self.run_git(self.go, "rev-parse", "origin/master")
        PREPARE.sync_lock(
            self.repositories,
            commit=True,
            push=False,
            fetch=False,
        )
        local_head = self.run_git(self.go, "rev-parse", "HEAD")
        self.assertNotEqual(local_head, remote_before)
        self.assertEqual(
            self.run_git(self.go_remote, "rev-parse", "refs/heads/master"),
            remote_before,
        )

        PREPARE.sync_lock(
            self.repositories,
            commit=False,
            push=True,
            fetch=False,
        )
        self.assertEqual(
            self.run_git(self.go_remote, "rev-parse", "refs/heads/master"),
            local_head,
        )

    def test_resume_push_rejects_additional_changes(self) -> None:
        PREPARE.sync_lock(
            self.repositories,
            commit=True,
            push=False,
            fetch=False,
        )
        (self.go / "unrelated.txt").write_text("unrelated\n", encoding="utf-8")
        self.run_git(self.go, "add", "unrelated.txt")
        self.run_git(self.go, "commit", "--amend", "--no-edit")

        with self.assertRaisesRegex(PREPARE.ReleasePreparationError, "change only"):
            PREPARE.sync_lock(
                self.repositories,
                commit=False,
                push=True,
                fetch=False,
            )

    def test_resume_push_rejects_stale_lock(self) -> None:
        with self.assertRaisesRegex(PREPARE.ReleasePreparationError, "does not pin"):
            PREPARE.sync_lock(
                self.repositories,
                commit=False,
                push=True,
                fetch=False,
            )

    def test_invalid_release_id_and_workspace_are_rejected(self) -> None:
        with self.assertRaisesRegex(PREPARE.ReleasePreparationError, "release ID"):
            PREPARE.create_release_tags(
                self.repositories,
                release_id="latest",
                create=False,
                push=False,
                fetch=False,
            )
        with self.assertRaisesRegex(
            PREPARE.ReleasePreparationError, "does not contain this go-ethereum"
        ):
            PREPARE.discover_workspace(
                self.workspace / "missing",
                expected_go_root=self.go,
                strict_remotes=False,
            )

    def test_tag_push_requires_explicit_local_mutation_flag(self) -> None:
        with self.assertRaisesRegex(PREPARE.ReleasePreparationError, "--create"):
            PREPARE.create_release_tags(
                self.repositories,
                release_id="usdb-testnet-v0-r1",
                create=False,
                push=True,
                fetch=False,
            )


if __name__ == "__main__":
    unittest.main()
