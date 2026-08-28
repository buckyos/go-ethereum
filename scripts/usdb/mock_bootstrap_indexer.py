#!/usr/bin/env python3
"""Test-only UIP-0006 RPC fixture for the SourceDAO bootstrap smoke."""

from __future__ import annotations

import argparse
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any


REGISTRY_ID = "bfd8c7e41ab4035db64e52eb9ea55050c08211c2ae4c2a88d8b2fc17ae1718b0"
ACTIVE_VERSION_SET_ID = "01d1d45f342994690d8ae27ac3d8538ad31e5f81f8e948c838067b3b52f94691"
SNAPSHOT_ID = "11" * 32
SYSTEM_STATE_ID = "22" * 32
DEFAULT_USDB_MAIN = "0x1111111111111111111111111111111111111111"
DEFAULT_TOTAL_MINER_BTC_SATS = "100000000"
DEFAULT_COLLAB_CONTRIBUTION = "100"
ACTIVE_VERSION_SET = {
    "inscription_schema_version": "uip-0001-miner-pass-inscription:v1",
    "pass_state_machine_version": "uip-0002-pass-state-machine:v1",
    "energy_formula_version": "uip-0003-pass-energy-formula:v1",
    "effective_energy_formula_version": "uip-0004-collab-leader-effective-energy:v1",
    "level_formula_version": "uip-0005-level-and-real-difficulty:v1",
    "query_semantics_version": "uip-0006-economic-query-semantics:v1",
    "state_view_version": "uip-0006-usdb-economic-state-view:v1",
    "commit_protocol_version": "uip-0008-usdb-local-state-commit:v1",
    "balance_history_semantics_version": "balance-snapshot-at-or-before:v1",
}


def build_system_state() -> dict[str, Any]:
    return {
        "activation_registry_id": REGISTRY_ID,
        "active_version_set": ACTIVE_VERSION_SET,
        "active_version_set_id": ACTIVE_VERSION_SET_ID,
        "local_synced_block_height": 0,
        "upstream_snapshot_id": SNAPSHOT_ID,
        "system_state_id": SYSTEM_STATE_ID,
    }


def build_pass_profile(
    pass_id: str,
    raw_params: Any,
    usdb_main: str = DEFAULT_USDB_MAIN,
    total_miner_btc_sats: str = DEFAULT_TOTAL_MINER_BTC_SATS,
    collab_contribution: str = DEFAULT_COLLAB_CONTRIBUTION,
) -> dict[str, Any]:
    if not isinstance(raw_params, list) or len(raw_params) != 1:
        raise ValueError("get_pass_economic_profile requires one parameter object")
    params = raw_params[0]
    if not isinstance(params, dict):
        raise ValueError("profile parameter must be an object")
    if params.get("pass_id") != pass_id:
        raise ValueError(f"unexpected pass_id {params.get('pass_id')!r}")
    context = params.get("context")
    if not isinstance(context, dict):
        raise ValueError("profile context is required")
    expected = context.get("expected_state")
    if not isinstance(expected, dict):
        raise ValueError("profile expected_state is required")
    height = context.get("requested_height")
    if not isinstance(height, int) or height != params.get("block_height"):
        raise ValueError("profile height mismatch")
    if height != 0:
        raise ValueError(f"fixture only supports BTC height 0, have {height}")
    expected_values = {
        "snapshot_id": SNAPSHOT_ID,
        "system_state_id": SYSTEM_STATE_ID,
        "activation_registry_id": REGISTRY_ID,
        "active_version_set_id": ACTIVE_VERSION_SET_ID,
    }
    for field, value in expected_values.items():
        if expected.get(field) != value:
            raise ValueError(f"unexpected {field}")
    return {
        "view_version": "uip-0006-usdb-economic-state-view:v1",
        "external_state": {
            "btc_height": 0,
            "snapshot_id": SNAPSHOT_ID,
            "stable_block_hash": "44" * 32,
            "stable_lag": 10,
            "local_state_commit": "55" * 32,
            "system_state_id": SYSTEM_STATE_ID,
            "balance_history_api_version": "1.0.0",
            "balance_history_semantics_version": "balance-snapshot-at-or-before:v1",
            "activation_registry_id": REGISTRY_ID,
            "active_version_set": ACTIVE_VERSION_SET,
            "active_version_set_id": ACTIVE_VERSION_SET_ID,
        },
        "pass": {
            "pass_id": pass_id,
            "owner_script_hash": "66" * 32,
            "owner_btc_addr": None,
            "state": "active",
            "pass_kind": "standard",
            "usdb_main": usdb_main,
            "raw_energy": "0",
            "collab_contribution": collab_contribution,
            "effective_energy": collab_contribution,
            "level": 0,
            "difficulty_factor_bps": 10000,
            "collab_breakdown_count": 0,
        },
        "miner_aggregate": {
            "total_miner_btc_sats": total_miner_btc_sats,
            "active_miner_owner_count": 1,
        },
    }


def build_miner_candidate(
    pass_id: str,
    raw_params: Any,
    usdb_main: str = DEFAULT_USDB_MAIN,
    total_miner_btc_sats: str = DEFAULT_TOTAL_MINER_BTC_SATS,
    collab_contribution: str = DEFAULT_COLLAB_CONTRIBUTION,
) -> dict[str, Any]:
    if not isinstance(raw_params, list) or len(raw_params) != 1:
        raise ValueError("resolve_miner_candidate requires one parameter object")
    params = raw_params[0]
    if not isinstance(params, dict):
        raise ValueError("candidate parameter must be an object")
    requested_usdb_main = params.get("usdb_main")
    if not isinstance(requested_usdb_main, str) or requested_usdb_main.lower() != usdb_main.lower():
        raise ValueError(f"unexpected usdb_main {requested_usdb_main!r}")
    profile_params = dict(params)
    profile_params["pass_id"] = pass_id
    profile = build_pass_profile(
        pass_id,
        [profile_params],
        usdb_main,
        total_miner_btc_sats,
        collab_contribution,
    )
    profile["selection_rule"] = "uip-0006:effective-energy-desc-pass-id-asc:v1"
    profile["matching_candidate_count"] = 1
    return profile


class BootstrapIndexerHandler(BaseHTTPRequestHandler):
    server: "BootstrapIndexerServer"

    def do_GET(self) -> None:
        if self.path != "/health":
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.end_headers()
        self.wfile.write(b"ok\n")

    def do_POST(self) -> None:
        request_id: Any = None
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length <= 0 or length > 1024 * 1024:
                raise ValueError("invalid request size")
            request = json.loads(self.rfile.read(length))
            if not isinstance(request, dict):
                raise ValueError("request must be an object")
            request_id = request.get("id")
            method = request.get("method")
            if method == "get_system_state_info":
                result = self.server.system_state()
            elif method == "get_pass_economic_profile":
                result = self.server.pass_profile(request.get("params"))
            elif method == "resolve_miner_candidate":
                result = self.server.miner_candidate(request.get("params"))
            else:
                self.write_response(
                    {
                        "jsonrpc": "2.0",
                        "id": request_id,
                        "error": {"code": -32601, "message": f"method not found: {method}"},
                    }
                )
                return
            self.write_response({"jsonrpc": "2.0", "id": request_id, "result": result})
        except (json.JSONDecodeError, KeyError, TypeError, ValueError) as error:
            self.write_response(
                {
                    "jsonrpc": "2.0",
                    "id": request_id,
                    "error": {"code": -32602, "message": f"invalid request: {error}"},
                }
            )

    def write_response(self, response: dict[str, Any]) -> None:
        encoded = json.dumps(response, separators=(",", ":")).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def log_message(self, format: str, *args: Any) -> None:
        return


class BootstrapIndexerServer(ThreadingHTTPServer):
    def __init__(
        self,
        address: tuple[str, int],
        pass_id: str,
        usdb_main: str,
        total_miner_btc_sats: str,
        collab_contribution: str,
    ) -> None:
        super().__init__(address, BootstrapIndexerHandler)
        self.pass_id = pass_id
        self.usdb_main = usdb_main
        self.total_miner_btc_sats = total_miner_btc_sats
        self.collab_contribution = collab_contribution

    @staticmethod
    def system_state() -> dict[str, Any]:
        return build_system_state()

    def pass_profile(self, raw_params: Any) -> dict[str, Any]:
        return build_pass_profile(
            self.pass_id,
            raw_params,
            self.usdb_main,
            self.total_miner_btc_sats,
            self.collab_contribution,
        )

    def miner_candidate(self, raw_params: Any) -> dict[str, Any]:
        return build_miner_candidate(
            self.pass_id,
            raw_params,
            self.usdb_main,
            self.total_miner_btc_sats,
            self.collab_contribution,
        )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--listen", default="127.0.0.1")
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--pass-id", required=True)
    parser.add_argument("--usdb-main", default=DEFAULT_USDB_MAIN)
    parser.add_argument(
        "--total-miner-btc-sats",
        default=DEFAULT_TOTAL_MINER_BTC_SATS,
    )
    parser.add_argument(
        "--collab-contribution",
        default=DEFAULT_COLLAB_CONTRIBUTION,
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    server = BootstrapIndexerServer(
        (args.listen, args.port),
        args.pass_id,
        args.usdb_main,
        args.total_miner_btc_sats,
        args.collab_contribution,
    )
    print(f"mock bootstrap indexer listening on {args.listen}:{args.port}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
