#!/usr/bin/env python3
"""Prefetch locked Go modules with bounded retries for transient transport errors."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
RETRY_DELAYS = (10, 30)
MAX_ATTEMPTS = 3
PERMANENT = re.compile(
    r"checksum mismatch|security error|unknown revision|unknown version|"
    r"authentication failed|terminal prompts disabled|permission denied|"
    r"\b(?:401 unauthorized|403 forbidden|404 not found|410 gone)\b|"
    r"certificate|malformed (?:module|go\.sum)|invalid module path",
    re.IGNORECASE,
)
HTTP2 = re.compile(r"stream error:.*\b(?:INTERNAL_ERROR|REFUSED_STREAM)\b", re.IGNORECASE)
TRANSIENT = re.compile(
    r"connection reset by peer|connection refused|i/o timeout|TLS handshake timeout|"
    r"context deadline exceeded|Client\.Timeout|temporary failure in name resolution|"
    r"network is unreachable|\bunexpected EOF\b|(?:^|[\s:])EOF\b|"
    r"\b429 Too Many Requests\b|\b5\d\d (?:[a-z]+[ -])+[a-z]+\b",
    re.IGNORECASE,
)


class Cancelled(KeyboardInterrupt):
    """Retain the signal so cancellation never becomes a download retry."""

    def __init__(self, signum=signal.SIGINT):
        self.signum = signum


def log(message: str) -> None:
    print(f"[usdb-go-modules] {message}", flush=True)


def classify(error: str) -> str:
    """Keep permanent errors ahead of transport hints, including mixed failures."""
    if PERMANENT.search(error):
        return "permanent"
    if HTTP2.search(error):
        return "http2"
    if TRANSIENT.search(error):
        return "transient"
    return "unknown"


def module_errors(raw: str) -> tuple[list[str], bool]:
    """Retain completed errors when a timeout truncates Go's JSON object stream."""
    errors = []
    decoder = json.JSONDecoder()
    try:
        while raw.strip():
            raw = raw.lstrip()
            value, end = decoder.raw_decode(raw)
            raw = raw[end:]
            if not isinstance(value, dict) or not isinstance(value.get("Path"), str):
                raise ValueError("invalid Go module report")
            error = value.get("Error", "")
            if not isinstance(error, str):
                raise ValueError("invalid Go module error")
            if error:
                errors.append(f"{value['Path']}@{value.get('Version', '')}: {error}")
    except ValueError:
        return errors, True
    return errors, False


def stop_process(process: subprocess.Popen) -> None:
    """Stop the download process group, including any direct-fetch VCS children."""
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        pass
    finally:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()


def download(go_binary: str, repo: Path, env: dict, output: Path, attempt: int,
             timeout: int) -> tuple[int, bool]:
    """Run only module acquisition; write complete output even on timeout/cancel."""
    with (output / f"attempt-{attempt}.json").open("wb") as stdout, \
            (output / f"attempt-{attempt}.stderr.log").open("wb") as stderr:
        process = subprocess.Popen([go_binary, "mod", "download", "-json"], cwd=repo,
                                   env=env, stdout=stdout, stderr=stderr, start_new_session=True)
        try:
            return process.wait(timeout=timeout), False
        except subprocess.TimeoutExpired:
            stop_process(process)
            return 124, True
        except BaseException:
            stop_process(process)
            raise


def http1_environment(env: dict) -> dict:
    """Override only the downloader's HTTP/2 setting, preserving other Go debug flags."""
    result = env.copy()
    flags = [flag for flag in result.get("GODEBUG", "").split(",")
             if flag and not flag.startswith("http2client=")]
    result["GODEBUG"] = ",".join(flags + ["http2client=0"])
    return result


def finish(report: dict, output: Path, status: str, reason: str, exit_code: int) -> int:
    """Persist the outcome and expose recovered downloads in the job summary."""
    report.update(status=status, reason=reason, exit_code=exit_code)
    (output / "result.json").write_text(json.dumps(report, indent=2) + "\n")
    count = len(report["attempts"])
    message = (f"status={status} attempts={count} reason={reason} "
               f"http1_fallback={report['http1_fallback']} logs={output}")
    log(message)
    if os.environ.get("GITHUB_ACTIONS") == "true" and exit_code:
        print("::error title=Go module preparation failed::" + message, flush=True)
    if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(summary, "a") as stream:
            stream.write(f"\n### Go module preparation\n\n- {message}\n")
    return exit_code


def prepare(repo: Path, go_binary: str, output: Path, timeout: int = 180) -> int:
    """Prefetch the go.mod build/test requirements without changing dependency policy."""
    if not 1 <= timeout <= 180:
        raise ValueError("attempt timeout must be between 1 and 180 seconds")
    output.mkdir(parents=True, exist_ok=True)
    report = {"schema_version": "usdb-go-modules:v1", "go_binary": go_binary,
              "attempt_timeout_secs": timeout, "attempts": [], "http1_fallback": False}
    env = os.environ.copy()
    # Workflows select the canonical Go binary. Preserve GOPROXY/GOSUMDB and caches.
    # The module's go 1.17+ requirements cover its build and test dependencies;
    # avoid 'all', which also fetches unrelated dependencies' test modules.
    try:
        locked = {name: (repo / name).read_bytes() for name in ("go.mod", "go.sum")}
        for attempt in range(1, MAX_ATTEMPTS + 1):
            log(f"attempt={attempt}/{MAX_ATTEMPTS} timeout_secs={timeout} "
                f"http1_fallback={report['http1_fallback']} go={go_binary}")
            entry = {"attempt": attempt, "http1_fallback": report["http1_fallback"]}
            report["attempts"].append(entry)
            started = time.monotonic()
            code, timed_out = download(go_binary, repo, env, output, attempt, timeout)
            entry.update(exit_code=code, elapsed_seconds=round(time.monotonic() - started, 3))
            raw = (output / f"attempt-{attempt}.json").read_text(errors="replace")
            stderr = (output / f"attempt-{attempt}.stderr.log").read_text(errors="replace").strip()
            if stderr:
                print(stderr, file=sys.stderr, flush=True)
            errors, malformed = module_errors(raw)
            for error in errors:
                print(error, file=sys.stderr, flush=True)
            if any((repo / name).read_bytes() != value for name, value in locked.items()):
                entry["classification"] = "lockfile_changed"
                return finish(report, output, "failed", "lockfile_changed", 1)
            if code < 0:
                entry["classification"] = "cancelled"
                return finish(report, output, "cancelled", "download_signal", 128 - code)
            if code == 0 and not errors and not malformed:
                entry["classification"] = "success"
                return finish(report, output, "recovered" if attempt > 1 else "success", "complete", 0)
            reasons = [classify(error) for error in errors]
            # Dependency graph errors can precede the JSON stream. Keep separate
            # 'go:' diagnostics separate so a transient failure cannot hide another error.
            reasons.extend(classify(error) for error in re.split(r"(?m)(?=^go: )", stderr) if error.strip())
            if "permanent" in reasons:
                kind = "permanent"
            elif malformed and not timed_out:
                kind = "invalid_report"
            elif "unknown" in reasons:
                kind = "unknown"
            elif timed_out:
                kind = "timeout"
            elif reasons:
                kind = "http2" if "http2" in reasons else "transient"
            else:
                kind = "unknown"
            entry["classification"] = kind
            log(f"attempt={attempt} exit_code={code} classification={kind}")
            if kind not in {"http2", "transient", "timeout"} or attempt == MAX_ATTEMPTS:
                return finish(report, output, "failed", kind, code if code > 0 else 1)
            if kind == "http2":
                env = http1_environment(env)
                report["http1_fallback"] = True
            delay = RETRY_DELAYS[attempt - 1]
            log(f"retry_in_seconds={delay}")
            time.sleep(delay)
    except KeyboardInterrupt as error:
        return finish(report, output, "cancelled", "interrupted", 128 + getattr(error, "signum", signal.SIGINT))
    except (OSError, ValueError) as error:
        log(f"preparation error: {error}")
        return finish(report, output, "failed", "local_error", 1)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=ROOT, help="Go module root")
    parser.add_argument("--go-binary", default=os.environ.get("USDB_GO_BIN", "go"),
                        help="Go binary selected by the CI toolchain setup")
    parser.add_argument("--output-dir", type=Path, required=True, help="Directory for attempt logs and result.json")
    parser.add_argument("--attempt-timeout-secs", type=int, choices=range(1, 181), metavar="1..180", default=180)
    args = parser.parse_args()

    def cancel(signum, _frame):
        raise Cancelled(signum)

    signal.signal(signal.SIGTERM, cancel)
    return prepare(args.repo.resolve(), args.go_binary, args.output_dir.resolve(), args.attempt_timeout_secs)


if __name__ == "__main__":
    sys.exit(main())
