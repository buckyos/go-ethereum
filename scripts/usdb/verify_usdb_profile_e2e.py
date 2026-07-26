#!/usr/bin/env python3

import argparse
import json
import urllib.request


PAYLOAD_SIZE = 107
VIEW_VERSION = "uip-0006-usdb-economic-state-view:v1"
BTC_REGTEST_ACTIVATION_REGISTRY_ID = (
    "22d820e6ec242b61f63473f279c41a4103af5cff13206b1925fd415cceaaf83d"
)
BTC_REGTEST_ACTIVATION_REGISTRY_REVISION_2_ID = (
    "25a39e8022e8351a40f59736b86cf81321c08042121cdb74b85a8f3918a2b973"
)
BTC_V1_ACTIVE_VERSION_SET_ID = (
    "01d1d45f342994690d8ae27ac3d8538ad31e5f81f8e948c838067b3b52f94691"
)
BTC_V1_ACTIVE_VERSION_SET = {
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
BPS_DENOMINATOR = 10_000
MINIMUM_DIFFICULTY = 8_192
BTC_SATS_PER_BTC = 100_000_000
EMISSION_BLOCKS = 157_680
K_BPS_BASE = 10_000
K_BPS_MIN = 8_001
K_BPS_MAX = 20_000
K_WINDOW_BLOCKS = 50_400
FIXED_PRICE_ATOMS_PER_BTC = 100_000_000_000_000_000_000_000
UINT128_MAX = 2**128 - 1
UINT64_MAX = 2**64 - 1
USDB_SYSTEM_STATE_ADDRESS = "0x0000000000000000000000000000000000001000"
EMPTY_UNCLE_HASH = "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347"
SYSTEM_STATE_SLOTS = {
    "schema": "0x173335b1b35d7ee82b9e595fe4798c8e777ab08ec72cb8ea8a8035ee1fade3b1",
    "issued": "0xdd1651483272028cad87b8ab291a694a9deb1d7f6b60efe175f823c406233da2",
    "price": "0xdf22b00cd5b1ebfe143e44347701e86394b9867790d8d631d43ef36dd099f884",
    "real_price": "0xca9c2c48cf84f8c36afc338940b0e06484e790e7190e255a57245056399bb792",
    "price_policy": "0xc65fb6e80dc7887c39c44824450f50076c21ffb398bc3abc8ec122d277f7ce03",
    "price_source": "0x93fbc84343f98a946b33b6067ae017273d92029de3e58c3b3c6d37fb033cac9a",
    "price_range": "0xc3faa41e87f1db8d882f1a24fd36bf5f7f873e141845019088d03d0e2f487697",
    "k_sum": "0xa05125c861ef555402b28fe982e4e36ddd9572a49576081d06ad23fbdcd9a3ae",
    "k_count": "0x40db96c2e761efb468bcae40739cb9d71d15e53f4b46a977d213476493a0ecea",
    "k_cursor": "0xc71798c59dae3ab826f28ffa3db501face181bd2d88225baadcb87ea950c53b2",
    "k_ring_base": "0x0c0b1b7c7641949e2f45575f48d889a70298842709e50c1070010b910fb3bc31",
    "k_last_ce": "0x1d2465ef2bfb872650e27eeb6a1327cb569d58e4fd2c4867eb4b8f38b922905c",
    "k_last_ae": "0xb4d89df049af3068c7073e80bf4918d5606bffb9df517e96c1f996f942c38f58",
    "k_last_bps": "0x53264b8f3aab69de54c5a4ecadabdbff09c07064034e8fcfdb79056a55dd9954",
}
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


def storage_word(url, slot, block_number):
    value = rpc_call(
        url,
        "eth_getStorageAt",
        [USDB_SYSTEM_STATE_ADDRESS, slot, hex(block_number)],
    )
    if not isinstance(value, str) or not value.startswith("0x"):
        raise SystemExit(
            f"invalid storage word for slot {slot} at block {block_number}: {value!r}"
        )
    return value.lower()


def storage_uint(url, slot, block_number):
    return int(storage_word(url, slot, block_number), 16)


def rpc_keccak(url, payload):
    result = rpc_call(url, "web3_sha3", ["0x" + payload.hex()])
    if not isinstance(result, str) or len(result) != 66:
        raise SystemExit(f"web3_sha3 returned invalid hash: {result!r}")
    return result.lower()


def k_ring_slot(url, index):
    if index < 0 or index >= K_WINDOW_BLOCKS:
        raise SystemExit(f"K ring index is out of range: {index}")
    key = index.to_bytes(32, "big")
    base = bytes.fromhex(SYSTEM_STATE_SLOTS["k_ring_base"][2:])
    return rpc_keccak(url, key + base)


def fixed_price_range_id(url, chain_id, start_block):
    encoded = (
        b"usdb.price.policy.range:v1"
        + b"\x00"
        + chain_id.to_bytes(32, "big")
        + start_block.to_bytes(8, "big")
        + (1).to_bytes(4, "big")
        + (1).to_bytes(4, "big")
        + FIXED_PRICE_ATOMS_PER_BTC.to_bytes(32, "big")
    )
    return rpc_keccak(url, encoded)


def parse_canonical_energy(field, value):
    if not isinstance(value, str) or not value or (len(value) > 1 and value[0] == "0"):
        raise SystemExit(f"{field} is not canonical decimal: {value!r}")
    if not value.isdigit():
        raise SystemExit(f"{field} is not canonical decimal: {value!r}")
    parsed = int(value)
    if parsed > UINT128_MAX:
        raise SystemExit(f"{field} exceeds uint128: {value}")
    return parsed


def parse_canonical_uint64(field, value):
    if not isinstance(value, str) or not value or (len(value) > 1 and value[0] == "0"):
        raise SystemExit(f"{field} is not canonical decimal: {value!r}")
    if not value.isdigit():
        raise SystemExit(f"{field} is not canonical decimal: {value!r}")
    parsed = int(value)
    if parsed > UINT64_MAX:
        raise SystemExit(f"{field} exceeds uint64: {value}")
    return parsed


def calculate_k_bps(current_energy, average_energy):
    if average_energy == 0:
        return K_BPS_BASE
    if current_energy < average_energy:
        numerator = 60_000 * average_energy
        denominator = current_energy + 5 * average_energy
        penalty = (numerator + denominator - 1) // denominator
        return max(K_BPS_MIN, K_BPS_MAX - penalty)
    return min(K_BPS_MAX, 10_000 * current_energy // average_energy)


def calculate_emission(total_miner_btc_sats, price_atoms_per_btc, issued, k_bps):
    target = total_miner_btc_sats * price_atoms_per_btc // BTC_SATS_PER_BTC
    remaining = max(0, target - issued)
    emission = remaining * k_bps // (EMISSION_BLOCKS * BPS_DENOMINATOR)
    return min(remaining, emission)


def require_equal(label, actual, expected, block_number):
    if actual != expected:
        raise SystemExit(
            f"{label} mismatch at block {block_number}: "
            f"have {actual!r} want {expected!r}"
        )


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
        "difficulty_policy_version": difficulty_policy_version,
    }


def resolve_profile(
    usdb_indexer_rpc_url,
    selector,
    expected_activation_registry_id,
    expected_active_version_set_id,
):
    context = {
        "requested_height": selector["btc_height"],
        "expected_state": {
            "snapshot_id": selector["snapshot_id"],
            "activation_registry_id": expected_activation_registry_id,
            "active_version_set_id": expected_active_version_set_id,
            "system_state_id": selector["system_state_id"],
        },
    }
    profile = rpc_call(
        usdb_indexer_rpc_url,
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
        ("activation_registry_id", expected_activation_registry_id),
        ("active_version_set_id", expected_active_version_set_id),
        ("system_state_id", selector["system_state_id"]),
    ):
        if external.get(field) != expected:
            raise SystemExit(
                f"profile external_state {field} mismatch: "
                f"have {external.get(field)!r} want {expected!r}"
            )
    if external.get("active_version_set") != BTC_V1_ACTIVE_VERSION_SET:
        raise SystemExit(
            "profile external_state active_version_set mismatch: "
            f"have {external.get('active_version_set')!r} "
            f"want {BTC_V1_ACTIVE_VERSION_SET!r}"
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
    reward_recipient = pass_view.get("usdb_main")
    if (
        not isinstance(reward_recipient, str)
        or len(reward_recipient) != 42
        or not reward_recipient.startswith("0x")
    ):
        raise SystemExit(f"profile has invalid usdb_main: {reward_recipient!r}")
    try:
        reward_recipient_value = int(reward_recipient[2:], 16)
    except ValueError as error:
        raise SystemExit(f"profile has invalid usdb_main: {reward_recipient!r}") from error
    if reward_recipient_value == 0:
        raise SystemExit("profile usdb_main is the zero address")

    aggregate = profile.get("miner_aggregate") or {}
    total_miner_btc_sats = parse_canonical_uint64(
        "miner_aggregate.total_miner_btc_sats",
        aggregate.get("total_miner_btc_sats"),
    )
    active_miner_owner_count = aggregate.get("active_miner_owner_count")
    if (
        not isinstance(active_miner_owner_count, int)
        or isinstance(active_miner_owner_count, bool)
        or active_miner_owner_count <= 0
        or active_miner_owner_count > UINT64_MAX
    ):
        raise SystemExit(
            "profile has invalid active_miner_owner_count: "
            f"{active_miner_owner_count!r}"
        )
    return {
        "raw": raw,
        "contribution": contribution,
        "effective": effective,
        "level": level,
        "factor": factor,
        "reward_recipient": reward_recipient.lower(),
        "total_miner_btc_sats": total_miner_btc_sats,
        "active_miner_owner_count": active_miner_owner_count,
    }


def expected_real_difficulty(parent, block, factor):
    parent_difficulty = int(parent["difficulty"], 16)
    elapsed = int(block["timestamp"], 16) - int(parent["timestamp"], 16)
    uncle_term = 2 if parent.get("uncles") else 1
    adjustment = max(uncle_term - elapsed // 9, -99)
    base = parent_difficulty + (parent_difficulty // 2_048) * adjustment
    base = max(base, MINIMUM_DIFFICULTY)
    return (base * factor + BPS_DENOMINATOR - 1) // BPS_DENOMINATOR


def expected_policy_difficulty(parent, block, factor, policy_version):
    difficulty = expected_real_difficulty(parent, block, factor)
    if policy_version == 1:
        return difficulty
    if policy_version == 65_535:
        # The build-tagged conformance policy is intentionally v1 + 1. It has
        # no production protocol meaning and exists only for activation tests.
        return difficulty + 1
    raise SystemExit(f"unsupported expected difficulty policy: {policy_version}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--blocks", required=True)
    parser.add_argument("--coinbase", required=True)
    parser.add_argument("--balance-hex", required=True)
    parser.add_argument("--usdb-chain-rpc-url", required=True)
    parser.add_argument("--usdb-indexer-rpc-url", required=True)
    parser.add_argument(
        "--expected-activation-registry-id",
        default=BTC_REGTEST_ACTIVATION_REGISTRY_ID,
    )
    parser.add_argument(
        "--expected-active-version-set-id",
        default=BTC_V1_ACTIVE_VERSION_SET_ID,
    )
    parser.add_argument("--activation-conformance-block", type=int)
    parser.add_argument(
        "--post-activation-registry-id",
        default=BTC_REGTEST_ACTIVATION_REGISTRY_REVISION_2_ID,
    )
    parser.add_argument("--expected-pass-id")
    parser.add_argument("--stage1-end", type=int)
    parser.add_argument("--initial-raw-energy", type=int)
    parser.add_argument("--boosted-raw-energy", type=int)
    args = parser.parse_args()

    with open(args.blocks, "r", encoding="utf-8") as stream:
        blocks = json.load(stream)
    if not blocks:
        raise SystemExit("no USDB blocks supplied")
    genesis = rpc_call(args.usdb_chain_rpc_url, "eth_getBlockByNumber", ["0x0", False])
    by_number = {0: genesis}
    chain_id = int(rpc_call(args.usdb_chain_rpc_url, "eth_chainId", []), 16)
    expected_balance = int(
        rpc_call(
            args.usdb_chain_rpc_url,
            "eth_getBalance",
            [args.coinbase, "0x0"],
        ),
        16,
    )
    expected_issued = storage_uint(
        args.usdb_chain_rpc_url,
        SYSTEM_STATE_SLOTS["issued"],
        0,
    )
    expected_k_sum = storage_uint(
        args.usdb_chain_rpc_url,
        SYSTEM_STATE_SLOTS["k_sum"],
        0,
    )
    expected_k_count = storage_uint(
        args.usdb_chain_rpc_url,
        SYSTEM_STATE_SLOTS["k_count"],
        0,
    )
    expected_k_cursor = storage_uint(
        args.usdb_chain_rpc_url,
        SYSTEM_STATE_SLOTS["k_cursor"],
        0,
    )
    require_equal(
        "system schema",
        storage_uint(args.usdb_chain_rpc_url, SYSTEM_STATE_SLOTS["schema"], 0),
        1,
        0,
    )
    for label, slot, expected in (
        ("price", "price", FIXED_PRICE_ATOMS_PER_BTC),
        ("real price", "real_price", FIXED_PRICE_ATOMS_PER_BTC),
        ("price policy", "price_policy", 1),
        ("price source", "price_source", 1),
    ):
        require_equal(
            label,
            storage_uint(args.usdb_chain_rpc_url, SYSTEM_STATE_SLOTS[slot], 0),
            expected,
            0,
        )
    expected_price_range = fixed_price_range_id(
        args.usdb_chain_rpc_url,
        chain_id,
        0,
    )
    require_equal(
        "price range",
        storage_word(args.usdb_chain_rpc_url, SYSTEM_STATE_SLOTS["price_range"], 0),
        expected_price_range,
        0,
    )
    if (
        expected_k_sum != 0
        or expected_k_count != 0
        or expected_k_cursor != 0
    ):
        raise SystemExit(
            "genesis K window is not empty: "
            f"sum={expected_k_sum} count={expected_k_count} cursor={expected_k_cursor}"
        )
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
        if block.get("transactions"):
            raise SystemExit(
                f"deterministic reward E2E does not permit transactions at block {number}"
            )
        if block.get("uncles") or block.get("sha3Uncles", "").lower() != EMPTY_UNCLE_HASH:
            raise SystemExit(f"USDB reward v1 requires empty uncles at block {number}")

        selector = decode_selector(block)
        expected_policy_version = 1
        expected_registry_id = args.expected_activation_registry_id
        if (
            args.activation_conformance_block is not None
            and number >= args.activation_conformance_block
        ):
            expected_policy_version = 65_535
            expected_registry_id = args.post_activation_registry_id
        if selector["difficulty_policy_version"] != expected_policy_version:
            raise SystemExit(
                f"unexpected difficulty policy at block {number}: "
                f"have {selector['difficulty_policy_version']} "
                f"want {expected_policy_version}"
            )
        if args.expected_pass_id and selector["pass_id"] != args.expected_pass_id:
            raise SystemExit(
                f"unexpected pass id at block {number}: "
                f"{selector['pass_id']} != {args.expected_pass_id}"
            )
        profile = resolve_profile(
            args.usdb_indexer_rpc_url,
            selector,
            expected_registry_id,
            args.expected_active_version_set_id,
        )
        raw = profile["raw"]
        contribution = profile["contribution"]
        effective = profile["effective"]
        level = profile["level"]
        factor = profile["factor"]
        all_raw.append(raw)
        block_coinbase = (block.get("miner") or block.get("author") or "").lower()
        require_equal("block coinbase", block_coinbase, args.coinbase.lower(), number)
        require_equal(
            "profile reward recipient",
            profile["reward_recipient"],
            block_coinbase,
            number,
        )
        expected_difficulty = expected_policy_difficulty(
            parent,
            block,
            factor,
            expected_policy_version,
        )
        actual_difficulty = int(block["difficulty"], 16)
        if actual_difficulty != expected_difficulty:
            raise SystemExit(
                f"difficulty mismatch at block {number}: "
                f"have {actual_difficulty} want {expected_difficulty}"
            )

        parent_number = number - 1
        require_equal(
            "parent issued supply",
            storage_uint(
                args.usdb_chain_rpc_url,
                SYSTEM_STATE_SLOTS["issued"],
                parent_number,
            ),
            expected_issued,
            parent_number,
        )
        require_equal(
            "parent K sum",
            storage_uint(
                args.usdb_chain_rpc_url,
                SYSTEM_STATE_SLOTS["k_sum"],
                parent_number,
            ),
            expected_k_sum,
            parent_number,
        )
        require_equal(
            "parent K count",
            storage_uint(
                args.usdb_chain_rpc_url,
                SYSTEM_STATE_SLOTS["k_count"],
                parent_number,
            ),
            expected_k_count,
            parent_number,
        )
        require_equal(
            "parent K cursor",
            storage_uint(
                args.usdb_chain_rpc_url,
                SYSTEM_STATE_SLOTS["k_cursor"],
                parent_number,
            ),
            expected_k_cursor,
            parent_number,
        )
        parent_price = storage_uint(
            args.usdb_chain_rpc_url,
            SYSTEM_STATE_SLOTS["price"],
            parent_number,
        )
        require_equal(
            "parent fixed price",
            parent_price,
            FIXED_PRICE_ATOMS_PER_BTC,
            parent_number,
        )
        require_equal(
            "parent fixed price range",
            storage_word(
                args.usdb_chain_rpc_url,
                SYSTEM_STATE_SLOTS["price_range"],
                parent_number,
            ),
            expected_price_range,
            parent_number,
        )

        average_energy = (
            expected_k_sum // K_WINDOW_BLOCKS
            if expected_k_count == K_WINDOW_BLOCKS
            else 0
        )
        k_bps = (
            calculate_k_bps(contribution, average_energy)
            if expected_k_count == K_WINDOW_BLOCKS
            else K_BPS_BASE
        )
        ring_slot = k_ring_slot(args.usdb_chain_rpc_url, expected_k_cursor)
        if expected_k_count == K_WINDOW_BLOCKS:
            old_sample = storage_uint(
                args.usdb_chain_rpc_url,
                ring_slot,
                parent_number,
            )
        else:
            old_sample = 0
            require_equal(
                "uninitialized warmup K ring slot",
                storage_uint(
                    args.usdb_chain_rpc_url,
                    ring_slot,
                    parent_number,
                ),
                0,
                parent_number,
            )
        emission = calculate_emission(
            profile["total_miner_btc_sats"],
            parent_price,
            expected_issued,
            k_bps,
        )
        expected_issued += emission
        expected_balance += emission
        expected_k_sum = expected_k_sum - old_sample + contribution
        expected_k_count = min(expected_k_count + 1, K_WINDOW_BLOCKS)
        expected_k_cursor = (expected_k_cursor + 1) % K_WINDOW_BLOCKS

        require_equal(
            "issued supply",
            storage_uint(
                args.usdb_chain_rpc_url,
                SYSTEM_STATE_SLOTS["issued"],
                number,
            ),
            expected_issued,
            number,
        )
        require_equal(
            "K ring sample",
            storage_uint(args.usdb_chain_rpc_url, ring_slot, number),
            contribution,
            number,
        )
        for label, slot, expected in (
            ("K sum", "k_sum", expected_k_sum),
            ("K count", "k_count", expected_k_count),
            ("K cursor", "k_cursor", expected_k_cursor),
            ("K last CE", "k_last_ce", contribution),
            ("K last AE", "k_last_ae", average_energy),
            ("K last bps", "k_last_bps", k_bps),
            ("price", "price", FIXED_PRICE_ATOMS_PER_BTC),
            ("real price", "real_price", FIXED_PRICE_ATOMS_PER_BTC),
            ("price policy", "price_policy", 1),
            ("price source", "price_source", 1),
        ):
            require_equal(
                label,
                storage_uint(
                    args.usdb_chain_rpc_url,
                    SYSTEM_STATE_SLOTS[slot],
                    number,
                ),
                expected,
                number,
            )
        active_price_start = (
            args.activation_conformance_block
            if args.activation_conformance_block is not None
            and number >= args.activation_conformance_block
            else 0
        )
        expected_price_range = fixed_price_range_id(
            args.usdb_chain_rpc_url,
            chain_id,
            active_price_start,
        )
        require_equal(
            "price range",
            storage_word(
                args.usdb_chain_rpc_url,
                SYSTEM_STATE_SLOTS["price_range"],
                number,
            ),
            expected_price_range,
            number,
        )
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
                    "difficulty_policy_version": expected_policy_version,
                    "activation_registry_id": expected_registry_id,
                    "difficulty": actual_difficulty,
                    "reward_recipient": profile["reward_recipient"],
                    "total_miner_btc_sats": profile["total_miner_btc_sats"],
                    "active_miner_owner_count": profile[
                        "active_miner_owner_count"
                    ],
                    "k_bps": k_bps,
                    "reward": emission,
                    "issued_usdb_atoms": expected_issued,
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
                "issued_usdb_atoms": expected_issued,
                "k_window_sum": expected_k_sum,
                "k_window_count": expected_k_count,
                "k_window_cursor": expected_k_cursor,
                "price_atoms_per_btc": FIXED_PRICE_ATOMS_PER_BTC,
                "raw_energy": all_raw,
                "stage1_raw_energy": stage1_raw,
                "stage2_raw_energy": stage2_raw,
            },
            sort_keys=True,
        )
    )


if __name__ == "__main__":
    main()
