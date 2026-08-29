#!/usr/bin/env python3
"""Persistently halt one USDB node after an upstream stable-state reorg."""

from __future__ import annotations

import argparse
import json
import os
import tempfile
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


BASELINE_SCHEMA = "usdb-deep-btc-reorg-baseline:v1"
INCIDENT_SCHEMA = "usdb-deep-btc-reorg-incident:v1"
INCIDENT_EXIT_CODE = 42
MONITOR_EXIT_CODE = 43


class GuardError(RuntimeError):
    pass


def strict_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    value: dict[str, Any] = {}
    for key, item in pairs:
        if key in value:
            raise GuardError(f"duplicate JSON key: {key}")
        value[key] = item
    return value


def utc_now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def atomic_write_json(path: Path, value: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(
        "w", encoding="utf-8", dir=path.parent, prefix=f".{path.name}.", delete=False
    ) as output:
        temporary = Path(output.name)
        json.dump(value, output, indent=2, sort_keys=True)
        output.write("\n")
        output.flush()
        os.fsync(output.fileno())
    os.replace(temporary, path)


def load_json(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=strict_object)
    except (OSError, json.JSONDecodeError, GuardError) as exc:
        raise GuardError(f"failed to load strict JSON {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise GuardError(f"expected JSON object in {path}")
    return value


def rpc_call(url: str, method: str, params: list[Any], timeout: float) -> Any:
    payload = json.dumps(
        {"jsonrpc": "2.0", "id": 1, "method": method, "params": params},
        separators=(",", ":"),
    ).encode()
    request = urllib.request.Request(
        url,
        data=payload,
        headers={"content-type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            envelope = json.loads(response.read().decode(), object_pairs_hook=strict_object)
    except (OSError, urllib.error.URLError, json.JSONDecodeError, GuardError) as exc:
        raise GuardError(f"RPC {method} failed at {url}: {exc}") from exc
    if not isinstance(envelope, dict):
        raise GuardError(f"RPC {method} returned a non-object envelope")
    if envelope.get("error") is not None:
        error = json.dumps(envelope["error"], sort_keys=True)
        raise GuardError(f"RPC {method} returned error: {error}")
    if "result" not in envelope:
        raise GuardError(f"RPC {method} response is missing result")
    return envelope["result"]


def read_reorg_epoch(indexer_rpc_url: str, timeout: float) -> tuple[int, dict[str, Any]]:
    readiness = rpc_call(indexer_rpc_url, "get_readiness", [], timeout)
    if not isinstance(readiness, dict):
        raise GuardError("usdb-indexer get_readiness returned a non-object result")
    epoch = readiness.get("upstream_reorg_epoch")
    if isinstance(epoch, bool) or not isinstance(epoch, int) or epoch < 0:
        raise GuardError(f"invalid upstream_reorg_epoch in readiness: {epoch!r}")
    return epoch, readiness


def read_chain_head(chain_rpc_url: str, timeout: float) -> dict[str, Any] | None:
    try:
        block = rpc_call(chain_rpc_url, "eth_getBlockByNumber", ["latest", False], timeout)
    except GuardError:
        return None
    if not isinstance(block, dict):
        return None
    return {key: block.get(key) for key in ("number", "hash", "parentHash", "extraData")}


class DeepReorgGuard:
    def __init__(
        self,
        state_dir: Path,
        indexer_rpc_url: str,
        chain_rpc_url: str,
        request_timeout_secs: float,
    ) -> None:
        self.state_dir = state_dir
        self.baseline_path = state_dir / "baseline.json"
        self.incident_path = state_dir / "halted.json"
        self.indexer_rpc_url = indexer_rpc_url
        self.chain_rpc_url = chain_rpc_url
        self.request_timeout_secs = request_timeout_secs

    def check_once(self) -> int:
        if self.incident_path.exists():
            incident = load_json(self.incident_path)
            raise GuardError(
                "USDB node is halted by a persisted deep BTC reorg incident: "
                f"{self.incident_path} epoch={incident.get('observed_epoch')!r}"
            )

        current_epoch, readiness = read_reorg_epoch(
            self.indexer_rpc_url, self.request_timeout_secs
        )
        if not self.baseline_path.exists():
            atomic_write_json(
                self.baseline_path,
                {
                    "schema_version": BASELINE_SCHEMA,
                    "upstream_reorg_epoch": current_epoch,
                    "indexer_service": readiness.get("service"),
                    "created_at": utc_now(),
                },
            )
            return current_epoch

        baseline = load_json(self.baseline_path)
        if baseline.get("schema_version") != BASELINE_SCHEMA:
            raise GuardError(f"unsupported baseline schema in {self.baseline_path}")
        baseline_epoch = baseline.get("upstream_reorg_epoch")
        if (
            isinstance(baseline_epoch, bool)
            or not isinstance(baseline_epoch, int)
            or baseline_epoch < 0
        ):
            raise GuardError(f"invalid baseline upstream_reorg_epoch: {baseline_epoch!r}")
        if current_epoch == baseline_epoch:
            return current_epoch

        relation = "advanced" if current_epoch > baseline_epoch else "regressed"
        atomic_write_json(
            self.incident_path,
            {
                "schema_version": INCIDENT_SCHEMA,
                "reason": f"upstream_reorg_epoch_{relation}",
                "baseline_epoch": baseline_epoch,
                "observed_epoch": current_epoch,
                "detected_at": utc_now(),
                "indexer_rpc_url": self.indexer_rpc_url,
                "indexer_readiness": readiness,
                "usdb_chain_head": read_chain_head(
                    self.chain_rpc_url, self.request_timeout_secs
                ),
            },
        )
        raise GuardError(
            "upstream stable-state reorg detected: "
            f"baseline_epoch={baseline_epoch} observed_epoch={current_epoch}; "
            f"incident={self.incident_path}"
        )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("check", "watch"))
    parser.add_argument("--state-dir", type=Path, required=True)
    parser.add_argument("--indexer-rpc-url", required=True)
    parser.add_argument("--chain-rpc-url", default="http://127.0.0.1:8545")
    parser.add_argument("--poll-interval-secs", type=float, default=5.0)
    parser.add_argument("--request-timeout-secs", type=float, default=5.0)
    parser.add_argument("--max-consecutive-errors", type=int, default=3)
    args = parser.parse_args()
    if args.poll_interval_secs <= 0:
        parser.error("--poll-interval-secs must be positive")
    if args.request_timeout_secs <= 0:
        parser.error("--request-timeout-secs must be positive")
    if args.max_consecutive_errors <= 0:
        parser.error("--max-consecutive-errors must be positive")
    return args


def main() -> int:
    args = parse_args()
    guard = DeepReorgGuard(
        args.state_dir,
        args.indexer_rpc_url,
        args.chain_rpc_url,
        args.request_timeout_secs,
    )
    if args.mode == "check":
        try:
            epoch = guard.check_once()
        except GuardError as exc:
            print(f"USDB deep-reorg guard check failed: {exc}", flush=True)
            return INCIDENT_EXIT_CODE if guard.incident_path.exists() else MONITOR_EXIT_CODE
        print(f"USDB deep-reorg guard baseline is ready: epoch={epoch}", flush=True)
        return 0

    consecutive_errors = 0
    while True:
        try:
            guard.check_once()
            consecutive_errors = 0
        except GuardError as exc:
            if guard.incident_path.exists():
                print(f"USDB deep-reorg guard halted the node: {exc}", flush=True)
                return INCIDENT_EXIT_CODE
            consecutive_errors += 1
            print(
                "USDB deep-reorg guard monitoring error: "
                f"attempt={consecutive_errors}/{args.max_consecutive_errors} error={exc}",
                flush=True,
            )
            if consecutive_errors >= args.max_consecutive_errors:
                return MONITOR_EXIT_CODE
        time.sleep(args.poll_interval_secs)


if __name__ == "__main__":
    raise SystemExit(main())
