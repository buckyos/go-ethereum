#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import pathlib
import subprocess
import sys
import tempfile
import unittest


MODULE_DIR = pathlib.Path(__file__).parent
sys.path.insert(0, str(MODULE_DIR))
MODULE_PATH = MODULE_DIR / "prepare_release.py"
SPEC = importlib.util.spec_from_file_location("prepare_release", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
PREPARE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = PREPARE
SPEC.loader.exec_module(PREPARE)


class PrepareReleaseTests(unittest.TestCase):
    def run_git(self, path: pathlib.Path, *arguments: str) -> str:
        completed = subprocess.run(
            ["git", *arguments],
            cwd=path,
            check=True,
            capture_output=True,
            text=True,
        )
        return completed.stdout.strip()

    def init_repository(
        self, workspace: pathlib.Path, directory: str, branch: str
    ) -> tuple[pathlib.Path, pathlib.Path]:
        repository = workspace / directory
        remote = workspace / "remotes" / f"{directory}.git"
        repository.mkdir(parents=True)
        remote.parent.mkdir(parents=True, exist_ok=True)
        self.run_git(repository, "init", "-b", branch)
        self.run_git(repository, "config", "user.name", "USDB Release Test")
        self.run_git(repository, "config", "user.email", "release-test@example.invalid")
        (repository / "README").write_text(directory + "\n", encoding="utf-8")
        self.run_git(repository, "add", "README")
        self.run_git(repository, "commit", "-m", "Initial fixture")
        subprocess.run(
            ["git", "init", "--bare", str(remote)],
            check=True,
            capture_output=True,
            text=True,
        )
        self.run_git(repository, "remote", "add", "origin", str(remote))
        self.run_git(repository, "push", "-u", "origin", branch)
        return repository, remote

    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory(prefix="usdb-prepare-release-")
        self.workspace = pathlib.Path(self.temp_dir.name)
        self.go, self.go_remote = self.init_repository(
            self.workspace, "go-ethereum", "master"
        )
        self.usdb, self.usdb_remote = self.init_repository(
            self.workspace, "usdb", "master"
        )
        self.source_dao, _ = self.init_repository(
            self.workspace, "SourceDAO", "main"
        )
        scripts = self.go / "scripts/usdb"
        scripts.mkdir(parents=True)
        self.lock_path = scripts / "ci-revisions.json"
        source_revision = self.run_git(self.source_dao, "rev-parse", "HEAD")
        self.lock_path.write_text(
            """{
  \"schema_version\": \"usdb-ci-revisions:v2\",
  \"coordinator\": {
    \"repository\": \"buckyos/go-ethereum\",
    \"directory\": \"go-ethereum\"
  },
  \"dependencies\": {
    \"usdb\": {
      \"repository\": \"buckyos/usdb\",
      \"directory\": \"usdb\",
      \"revision\": \"0000000000000000000000000000000000000000\"
    },
    \"source_dao\": {
      \"repository\": \"buckyos/SourceDAO\",
      \"directory\": \"SourceDAO\",
      \"revision\": \"SOURCE_REVISION\"
    }
  },
  \"toolchains\": {
    \"runner\": \"ubuntu-24.04\",
    \"go_release\": \"1.18.5\",
    \"go_compatibility\": \"1.26.0\",
    \"python\": \"3.13.7\",
    \"rust\": \"1.91.0\",
    \"node\": \"24.12.0\",
    \"npm\": \"11.6.2\"
  }
}
""".replace("SOURCE_REVISION", source_revision),
            encoding="utf-8",
        )
        self.run_git(self.go, "add", "scripts/usdb/ci-revisions.json")
        self.run_git(self.go, "commit", "-m", "Add revision fixture")
        self.run_git(self.go, "push", "origin", "master")
        self.repositories = PREPARE.discover_workspace(
            self.workspace,
            expected_go_root=self.go,
            strict_remotes=False,
        )

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

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

    def test_push_requires_explicit_local_mutation_flag(self) -> None:
        with self.assertRaisesRegex(PREPARE.ReleasePreparationError, "--commit"):
            PREPARE.sync_lock(
                self.repositories,
                commit=False,
                push=True,
                fetch=False,
            )
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
