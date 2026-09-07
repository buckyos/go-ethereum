#!/usr/bin/env python3
"""Capture both nodes' local and advertised heads without changing sync state."""

import argparse
from datetime import datetime, timezone
from http.client import HTTPException
import json
from pathlib import Path
import sys
import urllib.request


QUERIES = (
    ("head", "eth_getBlockByNumber", ["latest", False]),
    ("syncing", "eth_syncing", []),
    ("peers", "admin_peers", []),
    ("node_info", "admin_nodeInfo", []),
    ("peer_count", "net_peerCount", []),
)


def timestamp() -> str:
    return datetime.now(timezone.utc).isoformat()


def query(url: str, method: str, params: list) -> dict:
    """Keep RPC failures explicit so unavailable state cannot resemble genesis."""
    result = {"method": method, "collected_at": timestamp()}
    try:
        request = urllib.request.Request(url, data=json.dumps({
            "jsonrpc": "2.0", "id": 1, "method": method, "params": params,
        }).encode(), headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=1) as response:
            body = json.load(response)
        if not isinstance(body, dict):
            raise ValueError(f"invalid RPC response: {body!r}")
        if body.get("error") is not None:
            result["error"] = body["error"]
        elif "result" not in body:
            raise ValueError("RPC response is missing result")
        else:
            result["result"] = body["result"]
    except (OSError, ValueError, HTTPException) as error:
        result["error"] = f"{type(error).__name__}: {error}"
    return result


def save_report(output: Path, report: dict) -> None:
    # Persist each query before starting the next one. The shell's hard timeout
    # can then leave a valid partial report, even for a server that trickles data.
    temporary = output.with_suffix(".tmp")
    temporary.write_text(json.dumps(report, indent=2) + "\n")
    temporary.replace(output)


def collect(output: Path, nodes: list, reason: str, expected_height=None, expected_hash=None) -> dict:
    report = {
        "schema": "usdb-sync-diagnostics:v1", "status": "collecting",
        "started_at": timestamp(), "reason": reason,
        "expected_height": expected_height, "expected_hash": expected_hash,
        "nodes": {name: {"rpc": url} for name, url in nodes},
    }
    output.parent.mkdir(parents=True, exist_ok=True)
    save_report(output, report)
    print(f"[usdb-sync-diagnostics] reason={reason}, output={output}", file=sys.stderr, flush=True)
    # Alternate nodes so both local heads are captured before potentially slow
    # peer/admin RPCs. These are successive observations, not an atomic snapshot.
    for field, method, params in QUERIES:
        for name, url in nodes:
            observation = query(url, method, params)
            report["nodes"][name][field] = observation
            print("[usdb-sync-diagnostics] " + json.dumps({
                "node": name, "field": field, **observation,
            }), file=sys.stderr, flush=True)
            save_report(output, report)
    report["status"] = "complete"
    report["completed_at"] = timestamp()
    save_report(output, report)
    return report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--node", nargs=2, action="append", required=True, metavar=("NAME", "RPC"))
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--reason", required=True)
    parser.add_argument("--expected-height", type=int)
    parser.add_argument("--expected-hash")
    args = parser.parse_args()
    if len(args.node) != 2 or len({name for name, _ in args.node}) != 2:
        parser.error("exactly two distinct node names are required")
    collect(args.output, args.node, args.reason, args.expected_height, args.expected_hash)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
