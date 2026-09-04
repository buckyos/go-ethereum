#!/usr/bin/env python3
"""Prepare the pinned revisions and immutable tags for a USDB release."""

from __future__ import annotations

import argparse
import pathlib
import re
import subprocess
import sys
from dataclasses import dataclass
from typing import Sequence

import ci_revisions


RELEASE_ID_RE = re.compile(r"^usdb-(?:testnet|mainnet)-v[0-9]+-r[1-9][0-9]*$")
SCRIPT_GO_ROOT = pathlib.Path(__file__).resolve().parents[2]
REPOSITORY_SPECS = {
    "go_ethereum": ("go-ethereum", "buckyos/go-ethereum", "master"),
    "usdb": ("usdb", "buckyos/usdb", "master"),
    "source_dao": ("SourceDAO", "buckyos/SourceDAO", "main"),
}


class ReleasePreparationError(RuntimeError):
    """Raised when a release precondition or Git operation fails."""


def default_workspace_root(script_path: pathlib.Path = pathlib.Path(__file__)) -> pathlib.Path:
    """Derive the sibling-repository workspace from this script's Go checkout."""

    return script_path.resolve().parents[2].parent


def _github_repository_from_remote(remote: str) -> str | None:
    value = remote.strip()
    prefixes = (
        "https://github.com/",
        "http://github.com/",
        "ssh://git@github.com/",
        "git@github.com:",
    )
    for prefix in prefixes:
        if value.startswith(prefix):
            repository = value[len(prefix) :]
            if repository.endswith(".git"):
                repository = repository[:-4]
            return repository
    return None


def _run(
    command: Sequence[str],
    *,
    cwd: pathlib.Path,
    check: bool = True,
) -> subprocess.CompletedProcess[str]:
    completed = subprocess.run(
        list(command),
        cwd=cwd,
        capture_output=True,
        text=True,
    )
    if check and completed.returncode != 0:
        detail = completed.stderr.strip() or completed.stdout.strip() or "no output"
        raise ReleasePreparationError(
            f"command failed ({' '.join(command)}): {detail}"
        )
    return completed


@dataclass(frozen=True)
class Repository:
    """A checked-out repository participating in a coordinated release."""

    name: str
    path: pathlib.Path
    github_repository: str
    branch: str

    def git(self, *arguments: str, check: bool = True) -> subprocess.CompletedProcess[str]:
        return _run(("git", *arguments), cwd=self.path, check=check)

    def output(self, *arguments: str) -> str:
        return self.git(*arguments).stdout.strip()

    def head(self) -> str:
        return self.output("rev-parse", "HEAD")

    def ensure_clean(self) -> None:
        status = self.output("status", "--porcelain=v1")
        if status:
            raise ReleasePreparationError(
                f"{self.name} worktree is not clean:\n{status}"
            )

    def fetch(self) -> None:
        self.git("fetch", "--prune", "origin")

    def ensure_published_head(self) -> str:
        branch = self.output("branch", "--show-current")
        if branch != self.branch:
            raise ReleasePreparationError(
                f"{self.name} must be on {self.branch}, have {branch or 'detached HEAD'}"
            )
        head = self.head()
        remote_head = self.output("rev-parse", f"refs/remotes/origin/{self.branch}")
        if head != remote_head:
            raise ReleasePreparationError(
                f"{self.name} HEAD is not origin/{self.branch}: "
                f"local={head}, remote={remote_head}"
            )
        return head

    def local_tag_exists(self, release_id: str) -> bool:
        completed = self.git(
            "show-ref", "--verify", "--quiet", f"refs/tags/{release_id}", check=False
        )
        if completed.returncode not in (0, 1):
            raise ReleasePreparationError(
                f"failed to inspect local tag {release_id} in {self.name}"
            )
        return completed.returncode == 0

    def remote_tag_exists(self, release_id: str) -> bool:
        completed = self.git(
            "ls-remote",
            "--exit-code",
            "--tags",
            "origin",
            f"refs/tags/{release_id}",
            check=False,
        )
        if completed.returncode not in (0, 2):
            detail = completed.stderr.strip() or completed.stdout.strip() or "no output"
            raise ReleasePreparationError(
                f"failed to inspect remote tag {release_id} in {self.name}: {detail}"
            )
        return completed.returncode == 0


def discover_workspace(
    workspace_root: pathlib.Path,
    *,
    expected_go_root: pathlib.Path = SCRIPT_GO_ROOT,
    strict_remotes: bool = True,
) -> dict[str, Repository]:
    """Validate sibling layout and return canonical repository checkouts."""

    root = workspace_root.expanduser().resolve()
    expected_go = expected_go_root.resolve()
    inferred_go = (root / "go-ethereum").resolve()
    if inferred_go != expected_go:
        raise ReleasePreparationError(
            f"workspace root does not contain this go-ethereum checkout: "
            f"expected {expected_go}, resolved {inferred_go}"
        )

    repositories: dict[str, Repository] = {}
    for name, (directory, github_repository, branch) in REPOSITORY_SPECS.items():
        path = (root / directory).resolve()
        if not path.is_dir():
            raise ReleasePreparationError(f"missing {name} checkout: {path}")
        repository = Repository(name, path, github_repository, branch)
        top_level = pathlib.Path(repository.output("rev-parse", "--show-toplevel")).resolve()
        if top_level != path:
            raise ReleasePreparationError(
                f"{name} checkout root mismatch: expected {path}, have {top_level}"
            )
        if strict_remotes:
            remote = repository.output("remote", "get-url", "origin")
            actual_repository = _github_repository_from_remote(remote)
            if actual_repository != github_repository:
                raise ReleasePreparationError(
                    f"{name} origin mismatch: expected {github_repository}, have {remote}"
                )
        repositories[name] = repository
    return repositories


def _fetch_repositories(repositories: dict[str, Repository], enabled: bool) -> None:
    if enabled:
        for repository in repositories.values():
            repository.fetch()


def _load_workspace_lock(repositories: dict[str, Repository]) -> tuple[pathlib.Path, dict]:
    path = repositories["go_ethereum"].path / "scripts/usdb/ci-revisions.json"
    return path, ci_revisions.load_lock(path)


def _ensure_source_dao_matches_lock(
    repositories: dict[str, Repository], lock: dict
) -> str:
    source_dao = repositories["source_dao"]
    source_dao.ensure_clean()
    head = source_dao.ensure_published_head()
    locked = lock["dependencies"]["source_dao"]["revision"]
    if head != locked:
        raise ReleasePreparationError(
            f"SourceDAO HEAD does not match compatibility lock: head={head}, locked={locked}"
        )
    return head


def _push_existing_lock_commit(
    go_repository: Repository,
    lock_path: pathlib.Path,
) -> None:
    """Safely push one previously created, lock-only Go commit."""

    branch = go_repository.output("branch", "--show-current")
    if branch != go_repository.branch:
        raise ReleasePreparationError(
            f"go_ethereum must be on {go_repository.branch}, "
            f"have {branch or 'detached HEAD'}"
        )
    head = go_repository.head()
    remote_ref = f"refs/remotes/origin/{go_repository.branch}"
    remote_head = go_repository.output("rev-parse", remote_ref)
    if head == remote_head:
        print("go_ethereum lock commit is already published")
        return

    counts = go_repository.output(
        "rev-list", "--left-right", "--count", f"{remote_ref}...{head}"
    ).split()
    if len(counts) != 2:
        raise ReleasePreparationError(
            f"cannot determine go_ethereum ahead/behind state: {' '.join(counts)}"
        )
    behind, ahead = (int(value) for value in counts)
    if behind != 0 or ahead != 1:
        raise ReleasePreparationError(
            "resume push requires go_ethereum HEAD to be exactly one commit ahead of "
            f"origin/{go_repository.branch}: behind={behind}, ahead={ahead}"
        )

    relative_lock = str(lock_path.relative_to(go_repository.path))
    changed_paths = go_repository.output(
        "diff", "--name-only", f"{remote_ref}..{head}"
    ).splitlines()
    if changed_paths != [relative_lock]:
        detail = ", ".join(changed_paths) or "no files"
        raise ReleasePreparationError(
            "resume push requires the pending Go commit to change only "
            f"{relative_lock}; changed: {detail}"
        )
    go_repository.git("diff", "--check", f"{remote_ref}..{head}", "--", relative_lock)
    go_repository.git(
        "push", "origin", f"{head}:refs/heads/{go_repository.branch}"
    )
    print(f"pushed_go_ethereum_revision={head}")


def sync_lock(
    repositories: dict[str, Repository],
    *,
    commit: bool,
    push: bool,
    fetch: bool,
) -> None:
    """Pin published dependency HEADs and optionally commit or resume-push the update."""

    _fetch_repositories(repositories, fetch)
    for repository in repositories.values():
        repository.ensure_clean()

    go_repository = repositories["go_ethereum"]
    usdb_repository = repositories["usdb"]
    source_dao_repository = repositories["source_dao"]
    usdb_revision = usdb_repository.ensure_published_head()
    source_dao_revision = source_dao_repository.ensure_published_head()
    lock_path, lock = _load_workspace_lock(repositories)
    previous_usdb = lock["dependencies"]["usdb"]["revision"]
    previous_source_dao = lock["dependencies"]["source_dao"]["revision"]

    print(f"workspace_root={go_repository.path.parent}")
    print(f"go_ethereum_revision={go_repository.head()}")
    print(f"usdb_revision={usdb_revision}")
    print(f"source_dao_revision={source_dao_revision}")
    print(f"locked_usdb_revision={previous_usdb}")
    print(f"locked_source_dao_revision={previous_source_dao}")

    if push and not commit:
        if (
            previous_usdb != usdb_revision
            or previous_source_dao != source_dao_revision
        ):
            raise ReleasePreparationError(
                "--push can only resume an existing lock commit, but the lock does not "
                "pin both published dependency HEADs; use --commit --push"
            )
        _push_existing_lock_commit(go_repository, lock_path)
        return

    go_repository.ensure_published_head()
    if (
        previous_usdb == usdb_revision
        and previous_source_dao == source_dao_revision
    ):
        print("compatibility lock already pins both published dependency HEADs")
        return
    if not commit:
        print("dry run: pass --commit to write and commit the lock update")
        return

    ci_revisions.set_dependency_revision(lock, "usdb", usdb_revision)
    ci_revisions.set_dependency_revision(lock, "source_dao", source_dao_revision)
    ci_revisions.write_lock(lock_path, lock)
    ci_revisions.load_lock(lock_path)
    go_repository.git("diff", "--check")
    relative_lock = str(lock_path.relative_to(go_repository.path))
    go_repository.git("add", "--", relative_lock)
    go_repository.git("commit", "-m", "Update USDB CI revision lock", "--", relative_lock)
    committed_revision = go_repository.head()
    print(f"committed_go_ethereum_revision={committed_revision}")
    if push:
        go_repository.git(
            "push", "origin", f"{committed_revision}:refs/heads/{go_repository.branch}"
        )
        print(f"pushed_go_ethereum_revision={committed_revision}")


def _ensure_release_id(release_id: str) -> None:
    if RELEASE_ID_RE.fullmatch(release_id) is None:
        raise ReleasePreparationError(
            "release ID must use usdb-{testnet|mainnet}-vN-rN"
        )


def _ensure_tag_available(repository: Repository, release_id: str) -> None:
    if repository.local_tag_exists(release_id):
        raise ReleasePreparationError(
            f"{release_id} already exists locally in {repository.name}"
        )
    if repository.remote_tag_exists(release_id):
        raise ReleasePreparationError(
            f"{release_id} already exists on {repository.name} origin"
        )


def create_release_tags(
    repositories: dict[str, Repository],
    *,
    release_id: str,
    create: bool,
    push: bool,
    fetch: bool,
) -> None:
    """Create the same annotated release tag on the frozen USDB and Go commits."""

    if push and not create:
        raise ReleasePreparationError("--push requires --create")
    _ensure_release_id(release_id)
    _fetch_repositories(repositories, fetch)
    for repository in repositories.values():
        repository.ensure_clean()

    go_repository = repositories["go_ethereum"]
    usdb_repository = repositories["usdb"]
    go_revision = go_repository.ensure_published_head()
    usdb_revision = usdb_repository.ensure_published_head()
    lock_path, lock = _load_workspace_lock(repositories)
    source_dao_revision = _ensure_source_dao_matches_lock(repositories, lock)
    locked_usdb_revision = lock["dependencies"]["usdb"]["revision"]
    if locked_usdb_revision != usdb_revision:
        raise ReleasePreparationError(
            f"USDB HEAD does not match compatibility lock: "
            f"head={usdb_revision}, locked={locked_usdb_revision}"
        )

    for repository in (usdb_repository, go_repository):
        _ensure_tag_available(repository, release_id)

    print(f"release_id={release_id}")
    print(f"go_ethereum_revision={go_revision}")
    print(f"usdb_revision={usdb_revision}")
    print(f"source_dao_revision={source_dao_revision}")
    if not create:
        print("dry run: pass --create to create the two local annotated tags")
        return

    usdb_repository.git(
        "tag",
        "-a",
        release_id,
        usdb_revision,
        "-m",
        (
            f"Freeze USDB release {release_id}\n\n"
            f"Go-Ethereum: {go_revision}\nSourceDAO: {source_dao_revision}"
        ),
    )
    go_repository.git(
        "tag",
        "-a",
        release_id,
        go_revision,
        "-m",
        (
            f"Freeze USDB release {release_id}\n\n"
            f"USDB: {usdb_revision}\nSourceDAO: {source_dao_revision}"
        ),
    )
    for repository, revision in (
        (usdb_repository, usdb_revision),
        (go_repository, go_revision),
    ):
        tag_type = repository.output("cat-file", "-t", f"refs/tags/{release_id}")
        target = repository.output("rev-list", "-n", "1", release_id)
        if tag_type != "tag" or target != revision:
            raise ReleasePreparationError(
                f"created tag verification failed in {repository.name}"
            )
    print("created_local_tags=true")

    if push:
        usdb_repository.git("push", "origin", f"refs/tags/{release_id}")
        print("pushed_usdb_tag=true")
        try:
            go_repository.git("push", "origin", f"refs/tags/{release_id}")
        except ReleasePreparationError as error:
            raise ReleasePreparationError(
                "USDB tag was pushed but go-ethereum tag push failed; "
                f"do not move or delete the USDB tag, fix the failure and push the exact "
                f"existing go-ethereum tag: {error}"
            ) from error
        print("pushed_go_ethereum_tag=true")


def show_status(
    repositories: dict[str, Repository],
    *,
    fetch: bool,
) -> None:
    """Show the current release inputs without changing commits or tags."""

    _fetch_repositories(repositories, fetch)
    lock_path, lock = _load_workspace_lock(repositories)
    print(f"workspace_root={repositories['go_ethereum'].path.parent}")
    print(f"compatibility_lock={lock_path}")
    for name, repository in repositories.items():
        print(f"{name}_head={repository.head()}")
        status = "clean" if not repository.output("status", "--porcelain=v1") else "dirty"
        print(f"{name}_status={status}")
    for name, entry in lock["dependencies"].items():
        print(f"locked_{name}_revision={entry['revision']}")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--workspace-root",
        type=pathlib.Path,
        help=(
            "directory containing go-ethereum, usdb, and SourceDAO; defaults to "
            "the parent of this go-ethereum checkout"
        ),
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    status = subparsers.add_parser("status", help="show repository and lock identities")
    status.add_argument("--fetch", action="store_true", help="refresh origin refs first")

    sync = subparsers.add_parser(
        "sync-lock",
        help="pin the published USDB and SourceDAO dependency HEADs",
    )
    sync.add_argument(
        "--commit",
        action="store_true",
        help="write the lock update and create a new Go commit in this invocation",
    )
    sync.add_argument(
        "--push",
        action="store_true",
        help=(
            "push the lock commit created now, or safely resume one prior --commit run"
        ),
    )
    sync.add_argument("--no-fetch", action="store_true", help="use existing origin refs")

    tag = subparsers.add_parser("tag", help="create coordinated immutable release tags")
    tag.add_argument("--release-id", required=True)
    tag.add_argument("--create", action="store_true", help="create both local annotated tags")
    tag.add_argument("--push", action="store_true", help="push both release tags")
    tag.add_argument("--no-fetch", action="store_true", help="use existing origin refs")
    return parser


def main(arguments: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(arguments)
    workspace_root = args.workspace_root or default_workspace_root()
    try:
        repositories = discover_workspace(workspace_root)
        if args.command == "status":
            show_status(repositories, fetch=args.fetch)
        elif args.command == "sync-lock":
            sync_lock(
                repositories,
                commit=args.commit,
                push=args.push,
                fetch=not args.no_fetch,
            )
        else:
            create_release_tags(
                repositories,
                release_id=args.release_id,
                create=args.create,
                push=args.push,
                fetch=not args.no_fetch,
            )
    except (
        OSError,
        ValueError,
        subprocess.SubprocessError,
        ReleasePreparationError,
    ) as error:
        print(f"release preparation error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
