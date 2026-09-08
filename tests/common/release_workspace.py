#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import pathlib
import subprocess
import sys
import tempfile
import unittest


MODULE_DIR = pathlib.Path(__file__).resolve().parents[2] / "scripts/usdb"
sys.path.insert(0, str(MODULE_DIR))
MODULE_PATH = MODULE_DIR / "prepare_release.py"
SPEC = importlib.util.spec_from_file_location("prepare_release", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
PREPARE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = PREPARE
SPEC.loader.exec_module(PREPARE)


class ReleaseWorkspaceFixture(unittest.TestCase):
    """Isolated sibling checkouts with local bare origins for release operations."""

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

