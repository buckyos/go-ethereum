"""Isolated release checkout and fake Docker transport for tool preparation tests."""
import json
import os
from pathlib import Path
import shutil
import subprocess


ROOT = Path(__file__).resolve().parents[2]


class ReleaseFixture:
    def __init__(self, root):
        self.root = Path(root)
        self.usdb = self.root / "usdb"
        self.scripts = self.root / "go-ethereum/scripts/usdb"
        self.scripts.mkdir(parents=True)
        for name in ("prepare_regtest_tools.sh", "regtest_tools.py", "ci_revisions.py", "ci-revisions.json"):
            shutil.copyfile(ROOT / "scripts/usdb" / name, self.scripts / name)
        self.lock = self.scripts / "ci-revisions.json"
        self.tools = self.root / "binaries"
        self.output = self.root / "output"
        self.calls = self.root / "docker-calls.jsonl"
        self.write("docker/scripts/tools/ord_release.py",
                   'VERSION = "0.29.0"\nREVISION = "' + "a" * 40 + '"\nINDEX_SCHEMA = 34\n')
        self.write("docker/scripts/tools/bitcoin_release.py", 'CORE_VERSION = "31.1"\nCORE_SHA256 = "' + "b" * 64 + '"\n')
        for name in ("usdb-services", "world-sim-tools"):
            self.write(f"docker/Dockerfile.{name}",
                       'FROM rust:1.91.0-bookworm AS ord-builder\nARG ORD_VERSION=0.29.0\nARG ORD_REVISION=' + "a" * 40 +
                       '\nCOPY docker/locks/ord-0.29.0.Cargo.lock /tmp/ord-release.Cargo.lock\n')
        self.write("docker/Dockerfile.bitcoin-core", "ARG BITCOIN_VERSION=31.1\n")
        self.write("docker/locks/ord-0.29.0.Cargo.lock", 'version = 4\n[[package]]\nname = "ord"\nversion = "0.29.0"\n')
        self.git("init", "-q")
        self.commit()
        for name, version in (("ord/ord", "ord 0.29.0"),
                              ("bitcoin/bin/bitcoind", "Bitcoin Core daemon version v31.1.0 bitcoind"),
                              ("bitcoin/bin/bitcoin-cli", "Bitcoin Core RPC client version v31.1.0")):
            path = self.tools / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(f"#!/bin/sh\nprintf '%s\\n' '{version}'\n")
            path.chmod(0o755)
        (self.tools / "bitcoin/core-artifact-provenance.json").write_text('{}\n')
        fake_bin = self.root / "bin"
        fake_bin.mkdir()
        docker = fake_bin / "docker"
        docker.write_text('''#!/usr/bin/env python3
import json, os, pathlib, shutil, sys
args = sys.argv[1:]
with open(os.environ['FAKE_DOCKER_CALLS'], 'a') as stream:
    stream.write(json.dumps(args) + '\\n')
if args[0] == os.environ.get('FAKE_DOCKER_FAIL'):
    sys.exit(41)
if args[0] == 'build':
    pathlib.Path(args[args.index('--iidfile') + 1]).write_text(args[args.index('--target') + 1])
elif args[0] == 'create':
    print(args[1])
elif args[0] == 'cp':
    paths = {'/opt/ord/bin/ord': 'ord/ord', '/opt/bitcoin/bin/bitcoind': 'bitcoin/bin/bitcoind',
             '/opt/bitcoin/bin/bitcoin-cli': 'bitcoin/bin/bitcoin-cli',
             '/opt/bitcoin/share/core-artifact-provenance.json': 'bitcoin/core-artifact-provenance.json'}
    shutil.copyfile(pathlib.Path(os.environ['FAKE_DOCKER_TOOLS']) / paths[args[1].split(':', 1)[1]], args[2])
elif args[0] != 'rm':
    sys.exit('Unexpected Docker command')
''')
        docker.chmod(0o755)
        self.env = {**os.environ, "PATH": f"{fake_bin}:{os.environ['PATH']}",
                    "FAKE_DOCKER_CALLS": str(self.calls), "FAKE_DOCKER_TOOLS": str(self.tools),
                    "PYTHONDONTWRITEBYTECODE": "1"}

    def write(self, name, value):
        path = self.usdb / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(value)

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.usdb), *args], text=True).strip()

    def commit(self):
        self.git("add", ".")
        self.git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                 "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-qm", "Fixture")
        lock = json.loads(self.lock.read_text())
        lock["dependencies"]["usdb"]["revision"] = self.git("rev-parse", "HEAD")
        self.lock.write_text(json.dumps(lock))

    def build(self):
        return subprocess.run(["bash", str(self.scripts / "prepare_regtest_tools.sh"),
                               "--usdb-repo", str(self.usdb), "--output", str(self.output)],
                              env=self.env, capture_output=True, text=True, timeout=30)

    def docker_calls(self):
        return [json.loads(line) for line in self.calls.read_text().splitlines()] if self.calls.exists() else []
