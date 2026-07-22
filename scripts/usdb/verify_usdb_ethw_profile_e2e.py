#!/usr/bin/env python3

import argparse
import json
import urllib.request


PAYLOAD_SIZE = 107
VIEW_VERSION = "uip-0006-usdb-economic-state-view:v1"
BPS_DENOMINATOR = 10_000
MINIMUM_DIFFICULTY = 8_192
LEGACY_BLOCK_REWARD = 2 * 10**18
UINT128_MAX = 2**128 - 1
LEVEL_THRESHOLDS = (
    0,
    1_000_000,
    2_180_000,
    3_572_400,
    5_215_432,
    7_154_210,
    9_441_968,
    12_141_522,
    15_326_996,
    19_085_855,
    23_521_309,
    28_755_145,
    34_931_071,
    42_218_663,
    50_818_023,
    60_965_267,
    72_939_014,
    87_068_037,
    103_740_283,
    123_413_534,
    146_627_971,
    174_021_005,
    206_344_786,
    244_486_847,
    289_494_480,
    342_603_486,
    405_272_113,
    479_221_094,
    566_480_891,
    669_447_451,
    790_947_992,
    934_318_630,
    1_103_495_984,
    1_303_125_261,
    1_538_687_807,
    1_816_651_613,
    2_144_648_903,
    2_531_685_705,
    2_988_389_132,
    3_527_299_176,
    4_163_213_027,
    4_913_591_372,
    5_799_037_819,
    6_843_864_626,
    8_076_760_259,
    9_531_577_106,
    11_248_260_984,
    13_273_947_962,
    15_664_258_595,
    18_484_825_142,
    21_813_093_667,
)


def rpc_call(url, method, params):
    payload = json.dumps(
        {"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
    ).encode()
    request = urllib.request.Request(
        url,
        data=payload,
        headers={"content-type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=8) as response:
        body = json.loads(response.read().decode())
    if body.get("error") is not None:
        raise SystemExit(f"RPC {method} failed: {body['error']}")
    return body.get("result")


def parse_canonical_energy(field, value):
    if not isinstance(value, str) or not value or (len(value) > 1 and value[0] == "0"):
        raise SystemExit(f"{field} is not canonical decimal: {value!r}")
    if not value.isdigit():
        raise SystemExit(f"{field} is not canonical decimal: {value!r}")
    parsed = int(value)
    if parsed > UINT128_MAX:
        raise SystemExit(f"{field} exceeds uint128: {value}")
    return parsed


def level_for_energy(energy):
    level = 0
    for candidate, threshold in enumerate(LEVEL_THRESHOLDS):
        if energy < threshold:
            break
        level = candidate
    return level


def difficulty_factor_bps(level):
    return max(BPS_DENOMINATOR - level * 100, 5_000)


def decode_selector(block):
    number = int(block["number"], 16)
    extra_hex = (block.get("extraData") or "0x")[2:]
    if len(extra_hex) != PAYLOAD_SIZE * 2:
        raise SystemExit(
            f"unexpected extraData size at block {number}: "
            f"have {len(extra_hex) // 2} want {PAYLOAD_SIZE}"
        )
    payload = bytes.fromhex(extra_hex)
    if payload[0] != 1:
        raise SystemExit(f"unexpected payload version at block {number}: {payload[0]}")
    difficulty_policy_version = int.from_bytes(payload[1:3], "big")
    if difficulty_policy_version != 1:
        raise SystemExit(
            f"unexpected difficulty policy at block {number}: {difficulty_policy_version}"
        )
    btc_height = int.from_bytes(payload[3:7], "big")
    snapshot_id = payload[7:39].hex()
    system_state_id = payload[39:71].hex()
    pass_txid = payload[71:103].hex()
    pass_index = int.from_bytes(payload[103:107], "big")
    return {
        "btc_height": btc_height,
        "snapshot_id": snapshot_id,
        "system_state_id": system_state_id,
        "pass_id": f"{pass_txid}i{pass_index}",
    }


def resolve_profile(usdb_rpc_url, selector):
    context = {
        "requested_height": selector["btc_height"],
        "expected_state": {
            "snapshot_id": selector["snapshot_id"],
            "system_state_id": selector["system_state_id"],
        },
    }
    profile = rpc_call(
        usdb_rpc_url,
        "get_pass_economic_profile",
        [
            {
                "view_version": VIEW_VERSION,
                "pass_id": selector["pass_id"],
                "block_height": selector["btc_height"],
                "context": context,
            }
        ],
    )
    if profile is None:
        raise SystemExit(f"missing profile for pass {selector['pass_id']}")
    if profile.get("view_version") != VIEW_VERSION:
        raise SystemExit(f"unexpected profile view version: {profile.get('view_version')}")
    external = profile.get("external_state") or {}
    for field, expected in (
        ("btc_height", selector["btc_height"]),
        ("snapshot_id", selector["snapshot_id"]),
        ("system_state_id", selector["system_state_id"]),
    ):
        if external.get(field) != expected:
            raise SystemExit(
                f"profile external_state {field} mismatch: "
                f"have {external.get(field)!r} want {expected!r}"
            )
    pass_view = profile.get("pass") or {}
    if pass_view.get("pass_id") != selector["pass_id"]:
        raise SystemExit(f"profile pass id mismatch: {pass_view.get('pass_id')}")
    if pass_view.get("state") != "active" or pass_view.get("pass_kind") != "standard":
        raise SystemExit(
            f"selected pass is not a candidate: "
            f"state={pass_view.get('state')} kind={pass_view.get('pass_kind')}"
        )
    raw = parse_canonical_energy("raw_energy", pass_view.get("raw_energy"))
    contribution = parse_canonical_energy(
        "collab_contribution", pass_view.get("collab_contribution")
    )
    effective = parse_canonical_energy(
        "effective_energy", pass_view.get("effective_energy")
    )
    expected_effective = min(raw + contribution, UINT128_MAX)
    if effective != expected_effective:
        raise SystemExit(
            f"effective energy mismatch: have {effective} want {expected_effective}"
        )
    level = level_for_energy(effective)
    factor = difficulty_factor_bps(level)
    if pass_view.get("level") != level or pass_view.get("difficulty_factor_bps") != factor:
        raise SystemExit(
            "profile derived values mismatch: "
            f"level={pass_view.get('level')}/{level} "
            f"factor={pass_view.get('difficulty_factor_bps')}/{factor}"
        )
    return raw, contribution, effective, level, factor


def expected_real_difficulty(parent, block, factor):
    parent_difficulty = int(parent["difficulty"], 16)
    elapsed = int(block["timestamp"], 16) - int(parent["timestamp"], 16)
    uncle_term = 2 if parent.get("uncles") else 1
    adjustment = max(uncle_term - elapsed // 9, -99)
    base = parent_difficulty + (parent_difficulty // 2_048) * adjustment
    base = max(base, MINIMUM_DIFFICULTY)
    return (base * factor + BPS_DENOMINATOR - 1) // BPS_DENOMINATOR


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--blocks", required=True)
    parser.add_argument("--coinbase", required=True)
    parser.add_argument("--balance-hex", required=True)
    parser.add_argument("--eth-rpc-url", required=True)
    parser.add_argument("--usdb-rpc-url", required=True)
    parser.add_argument("--expected-pass-id")
    parser.add_argument("--stage1-end", type=int)
    parser.add_argument("--initial-raw-energy", type=int)
    parser.add_argument("--boosted-raw-energy", type=int)
    args = parser.parse_args()

    with open(args.blocks, "r", encoding="utf-8") as stream:
        blocks = json.load(stream)
    if not blocks:
        raise SystemExit("no ETHW blocks supplied")
    genesis = rpc_call(args.eth_rpc_url, "eth_getBlockByNumber", ["0x0", False])
    by_number = {0: genesis}
    expected_balance = 0
    all_raw = []
    stage1_raw = []
    stage2_raw = []

    for block in blocks:
        number = int(block["number"], 16)
        if number == 0:
            continue
        parent = by_number.get(number - 1)
        if parent is None:
            raise SystemExit(f"missing parent block {number - 1}")
        by_number[number] = block
        block_coinbase = (block.get("miner") or block.get("author") or "").lower()
        if block_coinbase != args.coinbase.lower():
            raise SystemExit(
                f"unexpected block coinbase at height {number}: "
                f"{block_coinbase} != {args.coinbase}"
            )
        if block.get("uncles"):
            raise SystemExit(f"deterministic E2E does not permit uncles at block {number}")

        selector = decode_selector(block)
        if args.expected_pass_id and selector["pass_id"] != args.expected_pass_id:
            raise SystemExit(
                f"unexpected pass id at block {number}: "
                f"{selector['pass_id']} != {args.expected_pass_id}"
            )
        raw, contribution, effective, level, factor = resolve_profile(
            args.usdb_rpc_url, selector
        )
        all_raw.append(raw)
        expected_difficulty = expected_real_difficulty(parent, block, factor)
        actual_difficulty = int(block["difficulty"], 16)
        if actual_difficulty != expected_difficulty:
            raise SystemExit(
                f"difficulty mismatch at block {number}: "
                f"have {actual_difficulty} want {expected_difficulty}"
            )
        expected_balance += LEGACY_BLOCK_REWARD
        if args.stage1_end is not None:
            (stage1_raw if number <= args.stage1_end else stage2_raw).append(raw)
        print(
            json.dumps(
                {
                    "eth_block": number,
                    "btc_height": selector["btc_height"],
                    "pass_id": selector["pass_id"],
                    "raw_energy": raw,
                    "collab_contribution": contribution,
                    "effective_energy": effective,
                    "level": level,
                    "difficulty_factor_bps": factor,
                    "difficulty": actual_difficulty,
                    "reward": LEGACY_BLOCK_REWARD,
                },
                sort_keys=True,
            )
        )

    actual_balance = int(args.balance_hex, 16)
    if actual_balance != expected_balance:
        raise SystemExit(
            f"unexpected coinbase balance: have {actual_balance} want {expected_balance}"
        )
    if args.stage1_end is not None and (not stage1_raw or not stage2_raw):
        raise SystemExit("missing stage-1 or stage-2 profile samples")
    if args.initial_raw_energy is not None and args.boosted_raw_energy is not None:
        if args.boosted_raw_energy <= args.initial_raw_energy:
            raise SystemExit(
                "expected boosted raw energy to increase: "
                f"{args.boosted_raw_energy} <= {args.initial_raw_energy}"
            )
        if any(value != args.initial_raw_energy for value in stage1_raw):
            raise SystemExit(
                "stage-1 selector replay did not preserve initial raw energy: "
                f"samples={stage1_raw} expected={args.initial_raw_energy}"
            )
        if any(value != args.boosted_raw_energy for value in stage2_raw):
            raise SystemExit(
                "stage-2 selector replay did not use boosted raw energy: "
                f"samples={stage2_raw} expected={args.boosted_raw_energy}"
            )
        if args.stage1_end is None and any(
            value != args.initial_raw_energy for value in all_raw
        ):
            raise SystemExit(
                "historical selector replay changed after current energy growth: "
                f"samples={all_raw} expected={args.initial_raw_energy}"
            )
    print(
        json.dumps(
            {
                "status": "ok",
                "blocks": len(blocks),
                "expected_balance": expected_balance,
                "actual_balance": actual_balance,
                "raw_energy": all_raw,
                "stage1_raw_energy": stage1_raw,
                "stage2_raw_energy": stage2_raw,
            },
            sort_keys=True,
        )
    )


if __name__ == "__main__":
    main()
