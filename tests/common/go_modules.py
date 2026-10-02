"""Create an isolated fake Go downloader with scripted process-level failures."""
from contextlib import ExitStack, redirect_stdout, redirect_stderr
import io
import json
import os
from pathlib import Path
import shlex
import sys
import tempfile
import time
from types import SimpleNamespace
from unittest.mock import Mock, patch

FAKE_GO = '''import json, os, pathlib, signal, subprocess, sys, time
root = pathlib.Path(__file__).resolve().parent
calls = root / "calls.jsonl"
number = len(calls.read_text().splitlines()) if calls.exists() else 0
with calls.open("a") as f:
    f.write(json.dumps({"args": sys.argv[1:], "pid": os.getpid(), "debug": os.environ.get("GODEBUG", ""),
                       "proxy": os.environ.get("GOPROXY"), "sumdb": os.environ.get("GOSUMDB")}) + "\\n")
plan = json.loads((root / "plan.json").read_text())
step = plan[min(number, len(plan) - 1)]
if step.get("mutate"):
    pathlib.Path(step["mutate"]).write_text("changed")
if step.get("child"):
    child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"])
    (root / "child.pid").write_text(str(child.pid))
if step.get("signal"):
    os.kill(os.getpid(), step["signal"])
if step.get("raw") is not None:
    print(step["raw"], flush=True)
else:
    for value in step.get("modules", [{"Path": "example.com/module", "Version": "v1.0.0"}]):
        print(json.dumps(value), flush=True)
print(step.get("stderr", ""), file=sys.stderr, flush=True)
if step.get("sleep"):
    time.sleep(step["sleep"])
sys.exit(step.get("exit", 0))
'''


def failure(error, **kwargs):
    """Describe a module-level failure with a nonzero Go exit status."""
    return {"exit": 1, "modules": [{"Path": "example.com/module", "Version": "v1.0.0", "Error": error}], **kwargs}


class DownloadFixture:
    """Keep module files, downloader state and logs outside the real repository/cache."""

    def __init__(self, module, plan):
        self.module, self.plan = module, plan
        self.stack = ExitStack()

    def __enter__(self):
        self.root = Path(self.stack.enter_context(tempfile.TemporaryDirectory()))
        self.repo = self.root / "repo"
        self.repo.mkdir()
        (self.repo / "go.mod").write_text("module fixture\n\ngo 1.17\n")
        (self.repo / "go.sum").write_text("fixture checksum\n")
        self.output = self.root / "logs"
        self.go = self.root / "go"
        source = self.root / "fake_go.py"
        source.write_text(FAKE_GO)
        self.go.write_text("#!/bin/sh\nexec " + shlex.quote(sys.executable) + " " + shlex.quote(str(source)) + ' "$@"\n')
        self.go.chmod(0o755)
        (self.root / "plan.json").write_text(json.dumps(self.plan))
        self.summary = self.root / "summary.md"
        self.stack.enter_context(patch.dict(os.environ, {"GODEBUG": "gctrace=1,http2client=1", "GITHUB_ACTIONS": "true",
            "GITHUB_STEP_SUMMARY": str(self.summary), "GOPROXY": "https://proxy.example,direct", "GOSUMDB": "sum.golang.org"}))
        self.sleeper = Mock()
        self.stack.enter_context(patch.object(self.module, "time", SimpleNamespace(sleep=self.sleeper, monotonic=time.monotonic)))
        self.stack.enter_context(redirect_stdout(io.StringIO()))
        self.stack.enter_context(redirect_stderr(io.StringIO()))
        return self

    def __exit__(self, *args):
        return self.stack.__exit__(*args)

    def run(self, timeout=180):
        return self.module.prepare(self.repo, str(self.go), self.output, timeout)

    def calls(self):
        path = self.root / "calls.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def report(self):
        return json.loads((self.output / "result.json").read_text())
