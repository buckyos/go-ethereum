#!/usr/bin/env python3
"""Require a live validator to fail a real profile query before upstream recovery."""

import argparse
import json
from pathlib import Path
import re
import sys
import time
import urllib.request


def is_profile_transport_failure(line: str, endpoint: str) -> bool:
    """Accept validation errors from the configured upstream, not startup/miner logs."""
    if not any(marker in line for marker in (
        "Invalid header encountered", "Synchronisation failed", "Propagated block verification failed",
    )):
        return False
    # Geth's logfmt writer escapes the quotes surrounding the RPC endpoint.
    message = line.replace('\\"', '"')
    prefix = f'failed to call get_pass_economic_profile: usdb-indexer rpc get_pass_economic_profile failed for "{endpoint}": '
    return any(prefix + category in message for category in (
        "connection refused", "connection reset", "connection closed", "request timed out",
    ))


def process_identity(pid: int) -> dict:
    fields = Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
    if fields[0] in {"Z", "X", "x"}:
        raise ValueError(f"validator exited: pid={pid}, state={fields[0]}")
    return {"pid": pid, "start_ticks": int(fields[19])}


def validator_height(url: str, timeout: float) -> int:
    request = urllib.request.Request(url, data=json.dumps({
        "jsonrpc": "2.0", "id": 1, "method": "eth_blockNumber", "params": [],
    }).encode(), headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        body = json.load(response)
    if not isinstance(body, dict) or body.get("error") is not None:
        raise ValueError(f"validator eth_blockNumber failed: {body!r}")
    height = body.get("result")
    if not isinstance(height, str) or re.fullmatch(r"0x[0-9a-fA-F]+", height) is None:
        raise ValueError(f"validator returned an invalid height: {height!r}")
    return int(height, 16)


def wait_for_failure(*, log: Path, offset: int, pid: int, rpc: str, upstream: str,
                     timeout: float, observe_seconds: float) -> dict:
    if pid <= 0 or offset < 0 or not 0 < observe_seconds < timeout:
        raise ValueError("require positive pid, nonnegative log offset and 0 < observation < timeout")
    identity = process_identity(pid)
    started = time.monotonic()
    deadline = started + timeout
    first_failure = None
    evidence_line = None
    # The caller captures this offset after starting the fresh validator and
    # before connecting its peer, so stale failures cannot satisfy this gate.
    with log.open("rb") as stream:
        stream.seek(offset)
        while time.monotonic() < deadline:
            if process_identity(pid) != identity:
                raise ValueError("validator process changed during the outage")
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            height = validator_height(rpc, min(2.0, remaining))
            if height != 0:
                raise ValueError(f"validator imported blocks during the outage: height={height}")
            if log.stat().st_size < stream.tell():
                raise ValueError("validator log was truncated during the outage")
            while True:
                position = stream.tell()
                line = stream.readline()
                if not line.endswith(b"\n"):
                    # A concurrent logger may not have completed the last line.
                    stream.seek(position)
                    break
                text = line.decode("utf-8", errors="replace").rstrip()
                if first_failure is None and is_profile_transport_failure(text, upstream):
                    first_failure = time.monotonic()
                    evidence_line = text
            now = time.monotonic()
            if first_failure is not None and now - first_failure >= observe_seconds and now < deadline:
                return {
                    "schema": "usdb-profile-validator-outage:v1", "status": "ok",
                    "validator": identity, "height": height, "upstream": upstream,
                    "log_offset": offset, "failure_evidence": evidence_line,
                    "failure_observed_after_seconds": round(first_failure - started, 3),
                    "observed_seconds": round(now - first_failure, 3),
                    "elapsed_seconds": round(now - started, 3),
                }
            time.sleep(min(0.2, max(0, deadline - now)))
    raise ValueError("timed out waiting for a real validator profile query failure and the full observation window")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--log", type=Path, required=True)
    parser.add_argument("--offset", type=int, required=True)
    parser.add_argument("--pid", type=int, required=True)
    parser.add_argument("--rpc", required=True)
    parser.add_argument("--upstream", required=True)
    parser.add_argument("--timeout", type=float, required=True)
    parser.add_argument("--observe-seconds", type=float, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        evidence = wait_for_failure(log=args.log, offset=args.offset, pid=args.pid, rpc=args.rpc,
                                    upstream=args.upstream, timeout=args.timeout, observe_seconds=args.observe_seconds)
        args.output.write_text(json.dumps(evidence, indent=2) + "\n")
    except (OSError, ValueError) as error:
        parser.exit(1, f"[usdb-profile-validator-outage] {error}\n")
    print("[usdb-profile-validator-outage] " + json.dumps(evidence, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
