#!/usr/bin/env python3
"""Build a replayable USDB PoW difficulty calibration report from block headers."""

from __future__ import annotations

import argparse
import json
import math
import os
import re
import sys
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any


REPORT_SCHEMA_VERSION = 1
DEFAULT_USDB_CHAIN_ID = 20260323
HASH_PATTERN = re.compile(r"^0x[0-9a-fA-F]{64}$")


class CalibrationError(ValueError):
    pass


@dataclass(frozen=True)
class BlockHeader:
    number: int
    block_hash: str
    parent_hash: str
    timestamp: int
    difficulty: int


class JsonRpcClient:
    def __init__(self, url: str, timeout_seconds: int) -> None:
        self.url = url
        self.timeout_seconds = timeout_seconds
        self.request_id = 0

    def call(self, method: str, params: list[Any]) -> Any:
        self.request_id += 1
        payload = json.dumps(
            {
                "jsonrpc": "2.0",
                "id": self.request_id,
                "method": method,
                "params": params,
            }
        ).encode("utf-8")
        request = urllib.request.Request(
            self.url,
            data=payload,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        try:
            with urllib.request.urlopen(request, timeout=self.timeout_seconds) as response:
                result = json.load(response)
        except (OSError, urllib.error.URLError, json.JSONDecodeError) as error:
            raise CalibrationError(f"JSON-RPC {method} failed: {error}") from error
        if not isinstance(result, dict):
            raise CalibrationError(f"JSON-RPC {method} returned a non-object response")
        if result.get("error") is not None:
            raise CalibrationError(f"JSON-RPC {method} returned error: {result['error']}")
        if "result" not in result:
            raise CalibrationError(f"JSON-RPC {method} response has no result")
        return result["result"]


def parse_rpc_quantity(field: str, value: Any) -> int:
    if not isinstance(value, str) or not re.fullmatch(r"0x(?:0|[1-9a-fA-F][0-9a-fA-F]*)", value):
        raise CalibrationError(f"{field} must be a canonical JSON-RPC quantity, have {value!r}")
    return int(value, 16)


def parse_hash(field: str, value: Any) -> str:
    if not isinstance(value, str) or HASH_PATTERN.fullmatch(value) is None:
        raise CalibrationError(f"{field} must be a 0x-prefixed 32-byte hash, have {value!r}")
    return value.lower()


def parse_rpc_header(raw: Any) -> BlockHeader:
    if not isinstance(raw, dict):
        raise CalibrationError("eth_getBlockByNumber returned a non-object block")
    header = BlockHeader(
        number=parse_rpc_quantity("block.number", raw.get("number")),
        block_hash=parse_hash("block.hash", raw.get("hash")),
        parent_hash=parse_hash("block.parentHash", raw.get("parentHash")),
        timestamp=parse_rpc_quantity("block.timestamp", raw.get("timestamp")),
        difficulty=parse_rpc_quantity("block.difficulty", raw.get("difficulty")),
    )
    if header.difficulty <= 0:
        raise CalibrationError(f"block {header.number} has non-positive difficulty")
    return header


def parse_report_header(raw: Any) -> BlockHeader:
    if not isinstance(raw, dict):
        raise CalibrationError("report header must be an object")
    try:
        header = BlockHeader(
            number=int(raw["number"]),
            block_hash=parse_hash("header.hash", raw["hash"]),
            parent_hash=parse_hash("header.parentHash", raw["parentHash"]),
            timestamp=int(raw["timestamp"]),
            difficulty=int(raw["difficulty"]),
        )
    except (KeyError, TypeError, ValueError) as error:
        raise CalibrationError(f"invalid report header: {error}") from error
    if min(header.number, header.timestamp) < 0 or header.difficulty <= 0:
        raise CalibrationError(f"invalid numeric value in report header {header.number}")
    return header


def collect_headers(
    client: JsonRpcClient,
    sample_blocks: int,
    confirmations: int,
    expected_chain_id: int,
) -> tuple[int, list[BlockHeader]]:
    chain_id = parse_rpc_quantity("eth_chainId", client.call("eth_chainId", []))
    if chain_id != expected_chain_id:
        raise CalibrationError(f"chain id mismatch: have {chain_id}, expected {expected_chain_id}")
    latest = parse_rpc_quantity("eth_blockNumber", client.call("eth_blockNumber", []))
    if latest < confirmations:
        raise CalibrationError(f"latest block {latest} is below confirmation depth {confirmations}")
    end = latest - confirmations
    if end < sample_blocks:
        raise CalibrationError(
            f"confirmed tip {end} does not contain {sample_blocks} complete sample intervals"
        )
    start = end - sample_blocks
    headers = []
    for number in range(start, end + 1):
        raw = client.call("eth_getBlockByNumber", [hex(number), False])
        if raw is None:
            raise CalibrationError(f"block {number} is unavailable")
        header = parse_rpc_header(raw)
        if header.number != number:
            raise CalibrationError(f"requested block {number}, received block {header.number}")
        headers.append(header)

    stable_tip = parse_rpc_header(client.call("eth_getBlockByNumber", [hex(end), False]))
    if stable_tip.block_hash != headers[-1].block_hash:
        raise CalibrationError(
            f"sample tip changed from {headers[-1].block_hash} to {stable_tip.block_hash}; retry after reorg"
        )
    return chain_id, headers


def validate_headers(headers: list[BlockHeader]) -> list[int]:
    if len(headers) < 2:
        raise CalibrationError("at least two consecutive headers are required")
    intervals = []
    for parent, child in zip(headers, headers[1:]):
        if child.number != parent.number + 1:
            raise CalibrationError(f"non-consecutive block numbers {parent.number} and {child.number}")
        if child.parent_hash != parent.block_hash:
            raise CalibrationError(
                f"parent hash mismatch at block {child.number}: have {child.parent_hash}, "
                f"expected {parent.block_hash}"
            )
        interval = child.timestamp - parent.timestamp
        if interval <= 0:
            raise CalibrationError(
                f"non-positive timestamp interval at block {child.number}: {interval}"
            )
        if child.difficulty <= 0:
            raise CalibrationError(f"block {child.number} has non-positive difficulty")
        intervals.append(interval)
    return intervals


def nearest_rank(values: list[int], percentile: int) -> int:
    if not values or percentile <= 0 or percentile > 100:
        raise CalibrationError("percentile requires non-empty values and a range of 1..100")
    ordered = sorted(values)
    index = math.ceil(percentile * len(ordered) / 100) - 1
    return ordered[index]


def round_ratio(numerator: int, denominator: int) -> int:
    if numerator <= 0 or denominator <= 0:
        raise CalibrationError("difficulty ratio must be positive")
    return (numerator + denominator // 2) // denominator


def header_to_json(header: BlockHeader) -> dict[str, Any]:
    return {
        "number": header.number,
        "hash": header.block_hash,
        "parentHash": header.parent_hash,
        "timestamp": str(header.timestamp),
        "difficulty": str(header.difficulty),
    }


def build_report(
    chain_id: int,
    profile: str,
    target_block_seconds: int,
    headers: list[BlockHeader],
) -> dict[str, Any]:
    profile = profile.strip()
    if not profile:
        raise CalibrationError("profile must not be empty")
    if target_block_seconds <= 0:
        raise CalibrationError("target block interval must be positive")
    intervals = validate_headers(headers)
    elapsed_seconds = sum(intervals)
    total_work = sum(header.difficulty for header in headers[1:])
    candidate_difficulty = round_ratio(total_work * target_block_seconds, elapsed_seconds)
    return {
        "schemaVersion": REPORT_SCHEMA_VERSION,
        "chainId": chain_id,
        "profile": profile,
        "targetBlockSeconds": target_block_seconds,
        "sample": {
            "startBlock": headers[0].number,
            "startHash": headers[0].block_hash,
            "endBlock": headers[-1].number,
            "endHash": headers[-1].block_hash,
            "intervalCount": len(intervals),
            "elapsedSeconds": str(elapsed_seconds),
            "totalWork": str(total_work),
            "headers": [header_to_json(header) for header in headers],
        },
        "observed": {
            "blockIntervalSeconds": {
                "meanNumerator": str(elapsed_seconds),
                "meanDenominator": len(intervals),
                "p50": nearest_rank(intervals, 50),
                "p95": nearest_rank(intervals, 95),
                "p99": nearest_rank(intervals, 99),
                "maximum": max(intervals),
            },
            "effectiveHashrate": {
                "workNumerator": str(total_work),
                "secondsDenominator": str(elapsed_seconds),
            },
        },
        "candidateDifficulty": {
            "decimal": str(candidate_difficulty),
            "hex": hex(candidate_difficulty),
        },
    }


def reject_duplicate_fields(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise CalibrationError(f"duplicate JSON field {key!r}")
        result[key] = value
    return result


def load_and_verify_report(path: Path, expected_chain_id: int) -> dict[str, Any]:
    try:
        raw = json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=reject_duplicate_fields)
    except (OSError, json.JSONDecodeError) as error:
        raise CalibrationError(f"read report {path}: {error}") from error
    if not isinstance(raw, dict) or raw.get("schemaVersion") != REPORT_SCHEMA_VERSION:
        raise CalibrationError(f"report {path} has an unsupported schemaVersion")
    if raw.get("chainId") != expected_chain_id:
        raise CalibrationError(
            f"report chain id mismatch: have {raw.get('chainId')}, expected {expected_chain_id}"
        )
    sample = raw.get("sample")
    if not isinstance(sample, dict) or not isinstance(sample.get("headers"), list):
        raise CalibrationError(f"report {path} has no sample headers")
    headers = [parse_report_header(header) for header in sample["headers"]]
    try:
        profile = str(raw["profile"])
        target_block_seconds = int(raw["targetBlockSeconds"])
    except (KeyError, TypeError, ValueError) as error:
        raise CalibrationError(f"report {path} has invalid calibration inputs") from error
    calculated = build_report(expected_chain_id, profile, target_block_seconds, headers)
    if raw != calculated:
        raise CalibrationError(f"report {path} does not match its embedded headers")
    return calculated


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--rpc-url", help="USDB JSON-RPC endpoint; defaults to USDB_RPC_URL")
    parser.add_argument("--input-report", type=Path, help="replay and verify an existing report")
    parser.add_argument("--profile", help="measured scenario name, for example nominal or minimum-viable")
    parser.add_argument("--target-block-seconds", type=int, help="explicit release target block interval")
    parser.add_argument("--sample-blocks", type=int, default=256, help="number of consecutive intervals")
    parser.add_argument("--confirmations", type=int, default=6, help="blocks excluded above the sample tip")
    parser.add_argument("--expected-chain-id", type=int, default=DEFAULT_USDB_CHAIN_ID)
    parser.add_argument("--timeout-seconds", type=int, default=10)
    parser.add_argument("--output", type=Path, help="write the canonical report to this path")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        if args.input_report is not None:
            if args.rpc_url is not None or args.profile is not None or args.target_block_seconds is not None:
                raise CalibrationError(
                    "--input-report cannot be combined with --rpc-url, --profile, or --target-block-seconds"
                )
            report = load_and_verify_report(args.input_report, args.expected_chain_id)
        else:
            if not args.profile:
                raise CalibrationError("--profile is required for live sampling")
            if args.target_block_seconds is None or args.target_block_seconds <= 0:
                raise CalibrationError("--target-block-seconds must be positive for live sampling")
            if args.sample_blocks <= 0:
                raise CalibrationError("--sample-blocks must be positive")
            if args.confirmations < 0:
                raise CalibrationError("--confirmations must not be negative")
            if args.timeout_seconds <= 0:
                raise CalibrationError("--timeout-seconds must be positive")
            rpc_url = args.rpc_url or os.environ.get("USDB_RPC_URL", "http://127.0.0.1:8545")
            client = JsonRpcClient(rpc_url, args.timeout_seconds)
            chain_id, headers = collect_headers(
                client,
                args.sample_blocks,
                args.confirmations,
                args.expected_chain_id,
            )
            report = build_report(chain_id, args.profile, args.target_block_seconds, headers)
        encoded = json.dumps(report, indent=2, sort_keys=True) + "\n"
        if args.output is not None:
            args.output.write_text(encoded, encoding="utf-8")
        else:
            sys.stdout.write(encoded)
        return 0
    except CalibrationError as error:
        print(f"calibration failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
