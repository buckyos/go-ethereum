#!/usr/bin/env bash
# Real Core/Ord/BH/indexer/Geth acceptance. Synthetic rules require explicit binaries.
set -euo pipefail
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(cd "$SCRIPT_DIR/../.." && pwd)
USDB_REPO_DIR=${USDB_REPO_DIR:-"$ROOT_DIR/../usdb"}
UPGRADE_TOOLS_DIR=${UPGRADE_TOOLS_DIR:-/tmp/usdb-miner-pass-upgrade-tools}
HELPER="$SCRIPT_DIR/miner_pass_upgrade_services.py"
PROTOCOL_HELPER="$USDB_REPO_DIR/tests/common/protocol_upgrade_live.py"
phase=${1:-all}
[[ "$phase" == all || "$phase" == --prepare-only || "$phase" == --run-only ]] || exit 2
# shellcheck source=lib/go_toolchain.sh
source "$SCRIPT_DIR/lib/go_toolchain.sh"
if [[ "$phase" != --run-only ]]; then
  mkdir -p "$UPGRADE_TOOLS_DIR"
  target=${CARGO_TARGET_DIR:-$USDB_REPO_DIR/src/btc/target}
  cargo build --locked --manifest-path "$USDB_REPO_DIR/src/btc/Cargo.toml" -p usdb-indexer
  cp "$target/debug/usdb-indexer" "$UPGRADE_TOOLS_DIR/indexer-default"
  cargo build --locked --manifest-path "$USDB_REPO_DIR/src/btc/Cargo.toml" -p usdb-indexer --features miner-pass-conformance
  cp "$target/debug/usdb-indexer" "$UPGRADE_TOOLS_DIR/indexer-conformance"
  cargo build --locked --manifest-path "$USDB_REPO_DIR/src/btc/Cargo.toml" -p balance-history
  cp "$target/debug/balance-history" "$UPGRADE_TOOLS_DIR/balance-history"
  usdb_build_geth "$ROOT_DIR" "$UPGRADE_TOOLS_DIR/geth-default"
  usdb_build_geth "$ROOT_DIR" "$UPGRADE_TOOLS_DIR/geth-conformance" usdb_miner_pass_conformance
  python3 "$HELPER" record-tools "$UPGRADE_TOOLS_DIR" "$ROOT_DIR" "$USDB_REPO_DIR"
  python3 "$PROTOCOL_HELPER" build-image "$UPGRADE_TOOLS_DIR"
  rustc --version >"$UPGRADE_TOOLS_DIR/rust-version.txt"
  "$USDB_GO_BIN" version >"$UPGRADE_TOOLS_DIR/go-version.txt"
fi
[[ "$phase" != --prepare-only ]] || exit 0
for binary in indexer-default indexer-conformance balance-history geth-default geth-conformance; do
  [[ -x "$UPGRADE_TOOLS_DIR/$binary" ]] || { echo "Missing prepared binary: $binary" >&2; exit 1; }
done
python3 "$HELPER" check-tools "$UPGRADE_TOOLS_DIR"
python3 "$PROTOCOL_HELPER" check-image "$UPGRADE_TOOLS_DIR"
RUN_ROOT=${WORK_DIR:-$(mktemp -d /tmp/usdb-miner-pass-upgrade-XXXXXX)}
mkdir -p "$RUN_ROOT"
# A run never clears or adopts an existing service root.
[[ -z "$(ls -A "$RUN_ROOT")" ]] || { echo "Fresh WORK_DIR required: $RUN_ROOT" >&2; exit 1; }
cp "$UPGRADE_TOOLS_DIR/build-manifest.json" "$RUN_ROOT/build-manifest.json"
export WORK_DIR="$RUN_ROOT"
export BITCOIN_DIR="$RUN_ROOT/usdb/bitcoin" ORD_DATA_DIR="$RUN_ROOT/usdb/ord"
export BALANCE_HISTORY_ROOT="$RUN_ROOT/usdb/balance-history" USDB_INDEXER_ROOT="$RUN_ROOT/usdb/usdb-indexer"
export USDB_CHAIN_WORK_DIR="$RUN_ROOT/geth" DATADIR="$RUN_ROOT/geth/datadir"
export GENESIS_JSON="$RUN_ROOT/geth/genesis.json" VALIDATOR_DATADIR="$RUN_ROOT/geth/validator"
export GETH_BIN="$UPGRADE_TOOLS_DIR/geth-conformance"
export GETH_LOG_FILE="$RUN_ROOT/geth/geth.log" VALIDATOR_LOG_FILE="$RUN_ROOT/geth/validator.log"
export BALANCE_HISTORY_LOG_FILE="$RUN_ROOT/usdb/balance-history.log"
export USDB_INDEXER_LOG_FILE="$RUN_ROOT/usdb/usdb-indexer.log" ORD_SERVER_LOG_FILE="$RUN_ROOT/usdb/ord-server.log"
# Do not inherit a caller's stale process handles or unrelated launch modes.
unset GETH_PID VALIDATOR_PID USDB_INDEXER_PID BALANCE_HISTORY_PID ORD_SERVER_PID
export BTC_STABLE_LAG_BLOCKS=10
unset ACTIVATION_CONFORMANCE_BLOCK ECONOMIC_CONFORMANCE_V2_BLOCK ECONOMIC_CONFORMANCE_V3_BLOCK
export PREMINE_BLOCKS=110
export INSCRIPTION_SOURCE=bitcoind
export USDB_UPSTREAM_POLL_INTERVAL_MS=200
export ORD_POLLING_INTERVAL=200ms
export ORD_MAX_SAVEPOINTS=256
export BTC_RPC_PORT=${BTC_RPC_PORT:-28732} BTC_P2P_PORT=${BTC_P2P_PORT:-28733}
export BH_RPC_PORT=${BH_RPC_PORT:-28710} USDB_INDEXER_RPC_PORT=${USDB_INDEXER_RPC_PORT:-28720} ORD_RPC_PORT=${ORD_RPC_PORT:-28730}
export HTTP_PORT=${HTTP_PORT:-19745} AUTHRPC_PORT=${AUTHRPC_PORT:-19751} P2P_PORT=${P2P_PORT:-31713}
export VALIDATOR_HTTP_PORT=${VALIDATOR_HTTP_PORT:-19746} VALIDATOR_AUTHRPC_PORT=${VALIDATOR_AUTHRPC_PORT:-19752} VALIDATOR_P2P_PORT=${VALIDATOR_P2P_PORT:-31714}
# Enables peer acceptance in the shared Geth launcher, without running its transition scenario.
export MINER_PASS_V2_TRANSITIONS=1
# shellcheck source=run_usdb_profile_e2e.sh
source "$SCRIPT_DIR/run_usdb_profile_e2e.sh"
HELPER="$SCRIPT_DIR/miner_pass_upgrade_services.py"
CATALOG="$USDB_REPO_DIR/tests/fixtures/miner-pass-upgrade/live-catalog.json"
SOURCE_CATALOG="$USDB_REPO_DIR/tests/fixtures/miner-pass-upgrade/live-source-catalog.json"
GOLDEN="$ROOT_DIR/internal/usdb/testdata/miner_pass_live_activation_golden.json"
INDEXER_BIN="$UPGRADE_TOOLS_DIR/indexer-conformance"
trap cleanup EXIT
python3 "$HELPER" scenario --seed "${MINER_PASS_UPGRADE_SEED:-0}" --output "$RUN_ROOT/scenario.json"
plan_value() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2],{"__builtins__":{}},{"d":d}))' "$RUN_ROOT/scenario.json" "$1"; }

regtest_start_usdb_indexer() {
  "$INDEXER_BIN" --root-dir "$USDB_INDEXER_ROOT" --skip-process-lock >>"$USDB_INDEXER_LOG_FILE" 2>&1 &
  export USDB_INDEXER_PID=$!
}
regtest_start_balance_history() {
  "$UPGRADE_TOOLS_DIR/balance-history" --root-dir "$BALANCE_HISTORY_ROOT" --skip-process-lock >>"$BALANCE_HISTORY_LOG_FILE" 2>&1 &
  export BALANCE_HISTORY_PID=$!
}
wait_owned_rpc() {
  local pid="$1" url="$2" method="$3" response deadline=$((SECONDS+SYNC_TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    kill -0 "$pid" 2>/dev/null || { echo "Service exited before RPC ready: pid=$pid url=$url" >&2; return 1; }
    response=$(curl -s --connect-timeout 1 --max-time 2 -H 'content-type: application/json' \
      --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$method\",\"params\":[]}" "$url" || true)
    if python3 -c 'import json,sys; d=json.loads(sys.argv[1]); assert "result" in d and "error" not in d' "$response" 2>/dev/null; then return 0; fi
    sleep 0.2
  done
  echo "RPC readiness timeout: $url" >&2; return 1
}
regtest_wait_usdb_rpc_ready() { wait_owned_rpc "$USDB_INDEXER_PID" "http://127.0.0.1:$USDB_INDEXER_RPC_PORT" get_network_type; }
regtest_wait_balance_history_rpc_ready() { wait_owned_rpc "$BALANCE_HISTORY_PID" "http://127.0.0.1:$BH_RPC_PORT" get_network_type; }
ready() {
  local height="$1"
  regtest_wait_until_ord_server_synced_to_bitcoind
  regtest_wait_until_balance_history_synced_eq "$height"
  regtest_wait_balance_history_consensus_ready
  regtest_wait_until_usdb_synced_eq "$height"
  regtest_wait_usdb_consensus_ready
}
advance() {
  regtest_ensure_stable_height_reachable "$1"
  ready "$1"
}
record() {
  python3 "$HELPER" capture --url "http://127.0.0.1:$USDB_INDEXER_RPC_PORT" \
    --ids "$leader" "$fixed" "$address_collab" --owners "$owner" "$fixed_owner" "$address_owner" \
    --core-url "http://127.0.0.1:$BTC_RPC_PORT" --cookie "$BITCOIN_DIR/regtest/.cookie" --output "$RUN_ROOT/$1.json"
}
write_mint() {
  local output="$1" version="$2" field="$3" value="$4" prev="${5:-}"
  python3 - "$output" "$version" "$field" "$value" "$prev" <<'PY'
import json,sys
from pathlib import Path
Path(sys.argv[1]).write_text(json.dumps(dict(p='usdb',op='mint',v=int(sys.argv[2]),prev=[sys.argv[5]] if sys.argv[5] else [],**{sys.argv[3]:sys.argv[4]})))
PY
}
mint() {
  local name="$1" version="$2" field="$3" value="$4" dest="$5" funding="$6" prev="${7:-}"
  write_mint "$WORK_DIR/$name.json" "$version" "$field" "$value" "$prev"
  regtest_ord_inscribe_file "$ORD_WALLET_NAME" "$WORK_DIR/$name.json" "$dest" "$funding"
}
confirm() {
  regtest_mine_blocks 1 "$miner_address"
  regtest_wait_until_ord_server_synced_to_bitcoind
}
mine_epoch() {
  local before after
  before=$(usdb_chain_current_height)
  usdb_chain_start_mining
  usdb_chain_wait_block_height "$((before+2))" >/dev/null
  usdb_chain_stop_mining
  sleep 1
  after=$(usdb_chain_current_height)
  usdb_chain_log "Completed epoch at BTC=$(($(regtest_get_bitcoin_tip_height)-10)), USDB=$after"
}

regtest_resolve_bitcoin_binaries
# Detect incompatible downloaded binaries before starting any services.
"$ORD_BIN" --version >"$RUN_ROOT/ord-version.txt"
"$BITCOIND_BIN" --version >"$RUN_ROOT/core-version.txt"
"$GETH_BIN" version >"$RUN_ROOT/geth-version.txt"
regtest_assert_ord_server_port_available
regtest_ensure_workspace_dirs
mkdir -p "$USDB_CHAIN_WORK_DIR"
regtest_start_bitcoind
regtest_ensure_wallet
miner_address=$(regtest_get_new_address)
regtest_mine_blocks 110 "$miner_address"
regtest_start_ord_server
regtest_wait_until_ord_server_synced_to_bitcoind
regtest_prepare_ord_wallets
funding=$(regtest_get_ord_wallet_receive_address "$ORD_WALLET_NAME")
# Separate cardinal outputs let Ord select the same authorized source repeatedly.
for _ in 1 2 3 4; do regtest_fund_address "$funding" 3; done
confirm
owner=$(regtest_get_ord_wallet_receive_address "$ORD_WALLET_NAME")
fixed_owner=$(regtest_get_ord_wallet_receive_address "$ORD_WALLET_NAME")
address_owner=$(regtest_get_ord_wallet_receive_address "$ORD_WALLET_NAME")
leader=$(mint leader 1 usdb_main "$MINER_PASS_USDB_MAIN" "$owner" "$funding")
confirm
fixed=$(mint fixed 1 leader_pass_id "$leader" "$fixed_owner" "$funding")
confirm
address_collab=$(mint address 1 leader_btc_addr "$owner" "$address_owner" "$funding")
confirm
regtest_fund_address "$owner" "$(plan_value "d['deposits'][0]")"
regtest_fund_address "$fixed_owner" "$(plan_value "d['deposits'][1]")"
regtest_fund_address "$address_owner" "$(plan_value "d['deposits'][2]")"
confirm
regtest_ensure_stable_height_reachable 159
regtest_create_balance_history_config
regtest_create_usdb_indexer_config
python3 "$HELPER" configure "$USDB_INDEXER_ROOT/config.json" "$SOURCE_CATALOG"
regtest_start_balance_history
regtest_wait_balance_history_rpc_ready
regtest_start_usdb_indexer
regtest_wait_usdb_rpc_ready
ready 159
# Mine one USDB segment before BTC upgrades; stop between explicit anchor changes.
run_geth dumpgenesis --usdb >"$GENESIS_JSON"
python3 "$HELPER" configure "$GENESIS_JSON" "$SOURCE_CATALOG"
run_geth init --datadir "$DATADIR" "$GENESIS_JSON" >"$RUN_ROOT/geth-init.log" 2>&1
usdb_chain_start_node false "${GETH_CMD[@]}"
usdb_chain_wait_rpc_ready
usdb_chain_wait_block_height 2 >/dev/null
usdb_chain_stop_mining
sleep 1
# Freeze both authoritative frontiers before staging the future chain checkpoint.
chain_head=$(usdb_chain_current_height)
registry_checkpoint=$((chain_head+1))
genesis_hash=$(regtest_json_expr "$(usdb_chain_rpc_call eth_getBlockByNumber '["0x0",false]')" "data['result']['hash']")
regtest_stop_process "$GETH_PID"
GETH_PID=""
source_registry=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["current_registry_id"])' "$SOURCE_CATALOG")
capture_prefix() {
  python3 "$HELPER" capture --url "http://127.0.0.1:$USDB_INDEXER_RPC_PORT" --ids "$leader" "$fixed" "$address_collab" \
    --heights 159 --registry "$source_registry" --output "$RUN_ROOT/$1.json"
}
capture_prefix before-adoption
regtest_stop_usdb_indexer
cp -a "$USDB_INDEXER_ROOT" "$WORK_DIR/pre-upgrade-indexer"
cp -a "$DATADIR" "$WORK_DIR/pre-upgrade-chain"
# All fixtures below use stopped database copies. A real rejection must leave
# every database byte intact, including the service which passed its preflight.
stage_upgrade() {
  python3 "$PROTOCOL_HELPER" stage --root "$RUN_ROOT/$1" --indexer "$2" --chain "$3" \
    --genesis "$GENESIS_JSON" --source-catalog "$SOURCE_CATALOG" --target-catalog "$CATALOG" \
    --genesis-hash "$genesis_hash" --checkpoint "$4" --tools "$UPGRADE_TOOLS_DIR"
}
cp -a "$USDB_INDEXER_ROOT" "$WORK_DIR/late-chain-indexer"
cp -a "$DATADIR" "$WORK_DIR/late-chain-data"
stage_upgrade late-chain "$WORK_DIR/late-chain-indexer" "$WORK_DIR/late-chain-data" "$chain_head"
python3 "$PROTOCOL_HELPER" refusal "$RUN_ROOT/late-chain" 'requires explicit derived-data rebuild'
# Simulate an operator who missed H on BTC while the USDB head is still compatible.
source_indexer_root="$USDB_INDEXER_ROOT"
USDB_INDEXER_ROOT="$WORK_DIR/late-indexer-data"
cp -a "$source_indexer_root" "$USDB_INDEXER_ROOT"
regtest_start_usdb_indexer
regtest_wait_usdb_rpc_ready
advance 160
regtest_stop_usdb_indexer
cp -a "$DATADIR" "$WORK_DIR/late-indexer-chain"
stage_upgrade late-indexer "$USDB_INDEXER_ROOT" "$WORK_DIR/late-indexer-chain" "$registry_checkpoint"
python3 "$PROTOCOL_HELPER" refusal "$RUN_ROOT/late-indexer" 'Registry adoption requires rebuild'
USDB_INDEXER_ROOT="$source_indexer_root"
stage_upgrade protocol-upgrade "$USDB_INDEXER_ROOT" "$DATADIR" "$registry_checkpoint"
python3 "$PROTOCOL_HELPER" exercise "$RUN_ROOT/protocol-upgrade"
USDB_INDEXER_ROOT="$USDB_INDEXER_ROOT-adopted"
python3 "$HELPER" configure "$USDB_INDEXER_ROOT/config.json" "$CATALOG"
cp "$RUN_ROOT/protocol-upgrade/target-kit/docker/networks/usdb-testnet-v999/usdb-genesis.json" "$GENESIS_JSON"
# The independent validator starts fresh with the entire canonical checkpoint history.
run_geth init --datadir "$VALIDATOR_DATADIR" "$GENESIS_JSON" >"$RUN_ROOT/validator-init.log" 2>&1
# An ordinary binary runs before H, then must stop progressing at H.
INDEXER_BIN="$UPGRADE_TOOLS_DIR/indexer-default"
regtest_start_usdb_indexer
regtest_wait_usdb_rpc_ready
regtest_ensure_stable_height_reachable 160
regtest_wait_until_balance_history_synced_eq 160
# A height-only check can pass before the first 5-second poll. Require causal
# rejection evidence before asserting that the durable frontier stayed at H-1.
rejection_deadline=$((SECONDS+SYNC_TIMEOUT_SEC))
while ! grep -hF 'version not supported: energy_formula_version=conformance-energy:double' \
    "$USDB_INDEXER_ROOT"/logs/*.log >"$RUN_ROOT/ordinary-indexer-rejection.txt" 2>/dev/null; do
  kill -0 "$USDB_INDEXER_PID" 2>/dev/null || { echo 'Ordinary indexer exited before rule rejection' >&2; exit 1; }
  (( SECONDS < rejection_deadline )) || { echo 'Missing ordinary indexer rule rejection' >&2; exit 1; }
  sleep 0.2
done
regtest_assert_json_expr "$(regtest_rpc_call_usdb_indexer get_synced_block_height '[]')" "data['result']" 159
regtest_stop_usdb_indexer
INDEXER_BIN="$UPGRADE_TOOLS_DIR/indexer-conformance"
regtest_start_usdb_indexer
regtest_wait_usdb_rpc_ready
ready 160
capture_prefix after-adoption
python3 "$HELPER" compare "$RUN_ROOT/before-adoption.json" "$RUN_ROOT/after-adoption.json"
usdb_chain_start_node true "${GETH_CMD[@]}"
usdb_chain_wait_rpc_ready
usdb_chain_wait_block_height "$((registry_checkpoint+1))" >/dev/null
usdb_chain_stop_mining
sleep 1
regtest_fund_address "$fixed_owner" "$(plan_value "d['topup']")"
confirm
# Spend only a selected cardinal output, preserving the original inscription sat.
regtest_run_ord_wallet_named "$ORD_WALLET_NAME" inscriptions >"$RUN_ROOT/protected.json"
python3 "$HELPER" spend-cardinal --url "http://127.0.0.1:$BTC_RPC_PORT/wallet/$ORD_WALLET_NAME" \
  --cookie "$BITCOIN_DIR/regtest/.cookie" --owner "$owner" --destination "$miner_address" \
  --amount "$(plan_value "d['withdrawal']")" --protected "$RUN_ROOT/protected.json" >"$RUN_ROOT/withdrawal-txid.txt"
confirm
advance 165
mine_epoch
regtest_stop_usdb_indexer
cp -a "$USDB_INDEXER_ROOT" "$WORK_DIR/middle-indexer"
regtest_start_usdb_indexer
regtest_wait_usdb_rpc_ready
advance 182
mine_epoch
record continuous
# Compare the independent validator's complete header/state root, then audit rewards separately.
final_height=$(usdb_chain_current_height)
run_activation_fresh_validator_check "$final_height"
python3 "$HELPER" blocks --url "http://$HTTP_ADDR:$HTTP_PORT" --output "$RUN_ROOT/blocks.json"
balance=$(regtest_json_expr "$(usdb_chain_rpc_call eth_getBalance "[\"$USDB_CHAIN_MINER_ADDRESS\",\"latest\"]")" "data['result']")
python3 "$SCRIPT_DIR/verify_usdb_profile_e2e.py" --blocks "$RUN_ROOT/blocks.json" \
  --coinbase "$USDB_CHAIN_MINER_ADDRESS" --balance-hex "$balance" \
  --usdb-chain-rpc-url "http://$HTTP_ADDR:$HTTP_PORT" --usdb-indexer-rpc-url "http://127.0.0.1:$USDB_INDEXER_RPC_PORT" \
  --miner-pass-upgrade-golden "$GOLDEN" --miner-pass-registry-checkpoint "$registry_checkpoint" >"$RUN_ROOT/rewards.jsonl"
regtest_stop_process "$GETH_PID"
GETH_PID=""
run_geth --datadir "$DATADIR" export "$RUN_ROOT/canonical.rlp" >"$RUN_ROOT/export.log" 2>&1
# Ordinary Geth cannot adopt the service-test catalog, even if it knows the baseline formulas.
if "$UPGRADE_TOOLS_DIR/geth-default" init --datadir "$USDB_CHAIN_WORK_DIR/ordinary" "$GENESIS_JSON" >"$RUN_ROOT/ordinary-geth.log" 2>&1; then
  if timeout 60 "$UPGRADE_TOOLS_DIR/geth-default" --datadir "$USDB_CHAIN_WORK_DIR/ordinary" --nocompaction import \
    --ethash.usdb-indexer.rpcurl "http://127.0.0.1:$USDB_INDEXER_RPC_PORT" "$RUN_ROOT/canonical.rlp" >>"$RUN_ROOT/ordinary-geth.log" 2>&1; then
    echo "Ordinary Geth accepted test-only rule history" >&2; exit 1
  fi
fi
grep -F 'BTC activation registry not supported' "$RUN_ROOT/ordinary-geth.log" >/dev/null
# A new schema can inherit an old-schema Leader. The tightened state rule rejects new collabs.
invalid_owner=$(regtest_get_ord_wallet_receive_address "$ORD_WALLET_NAME")
invalid=$(mint rejected-collab 901 leader_pass_id "$leader" "$invalid_owner" "$funding")
confirm
new_owner=$(regtest_get_ord_wallet_receive_address "$ORD_WALLET_NAME")
# Use a dedicated small source coin; Ord's normal largest-coin selection would
# empty the old owner and erase the very energy this scenario must inherit.
source_txid=$("$BITCOIN_CLI_BIN" -regtest -datadir="$BITCOIN_DIR" -rpcport="$BTC_RPC_PORT" \
  -rpcwallet="$WALLET_NAME" sendtoaddress "$owner" 0.005)
confirm
source_vout=$("$BITCOIN_CLI_BIN" -regtest -datadir="$BITCOIN_DIR" -rpcport="$BTC_RPC_PORT" \
  -rpcwallet="$ORD_WALLET_NAME" listunspent 1 | python3 -c \
  'import json,sys; rows=[r for r in json.load(sys.stdin) if r["txid"]==sys.argv[1] and r["address"]==sys.argv[2]]; assert len(rows)==1; print(rows[0]["vout"])' "$source_txid" "$owner")
write_mint "$WORK_DIR/successor.json" 901 usdb_main "$MINER_PASS_USDB_MAIN" "$leader"
regtest_run_ord_wallet_named "$ORD_WALLET_NAME" inscribe --fee-rate "$ORD_FEE_RATE" \
  --file "$WORK_DIR/successor.json" --destination "$new_owner" --satpoint "$source_txid:$source_vout:0" >"$RUN_ROOT/successor.ord-result.json"
child=$(regtest_extract_inscription_id "$(cat "$RUN_ROOT/successor.ord-result.json")")
confirm
transfer_owner=$(regtest_get_ord_wallet_receive_address "$ORD_WALLET_NAME_B")
regtest_ord_send_inscription "$ORD_WALLET_NAME" "$transfer_owner" "$fixed" >"$RUN_ROOT/transfer-txid.txt"
confirm
regtest_ord_burn_inscription "$ORD_WALLET_NAME" "$address_collab" >"$RUN_ROOT/burn-txid.txt"
confirm
through=$(regtest_get_bitcoin_tip_height)
advance "$through"
regtest_assert_usdb_pass_snapshot_state "$invalid" "$through" invalid
regtest_assert_usdb_pass_snapshot_state "$leader" "$through" consumed
regtest_assert_usdb_pass_snapshot_state "$child" "$through" active
regtest_assert_usdb_pass_snapshot_state "$fixed" "$through" dormant
regtest_assert_usdb_pass_snapshot_state "$address_collab" "$through" burned
regtest_assert_json_expr "$(regtest_get_pass_economic_profile_response "$child" "$through")" "int(data['result']['pass']['raw_energy']) > 0" True
capture_final() {
  record "$1-history"
  python3 "$HELPER" capture --url "http://127.0.0.1:$USDB_INDEXER_RPC_PORT" --output "$RUN_ROOT/$1-final.json" \
    --ids "$leader" "$fixed" "$address_collab" "$child" "$invalid" --heights "$through"
}
python3 "$HELPER" inheritance --url "http://127.0.0.1:$USDB_INDEXER_RPC_PORT" \
  --core-url "http://127.0.0.1:$BTC_RPC_PORT" --cookie "$BITCOIN_DIR/regtest/.cookie" \
  --parent "$leader" --child "$child" --owner "$owner" --child-owner "$new_owner" \
  --height "$through" --output "$RUN_ROOT/inheritance.json"
capture_final original
# Process crash and paired-checkpoint catch-up must preserve exact historical query evidence.
regtest_crash_usdb_indexer
regtest_start_usdb_indexer
regtest_wait_usdb_rpc_ready
ready "$through"
capture_final restarted
python3 "$HELPER" compare "$RUN_ROOT/original-final.json" "$RUN_ROOT/restarted-final.json"
python3 "$HELPER" compare "$RUN_ROOT/continuous.json" "$RUN_ROOT/restarted-history.json"
original_root="$USDB_INDEXER_ROOT"
for checkpoint in pre-upgrade-indexer middle-indexer clean-indexer; do
  regtest_stop_usdb_indexer
  USDB_INDEXER_ROOT="$WORK_DIR/$checkpoint"
  if [[ "$checkpoint" == clean-indexer ]]; then
    mkdir -p "$USDB_INDEXER_ROOT"
    cp "$original_root/config.json" "$USDB_INDEXER_ROOT/config.json"
  fi
  # Old paired checkpoints must retain their binding until startup validates adoption.
  python3 "$HELPER" configure "$USDB_INDEXER_ROOT/config.json" "$CATALOG"
  regtest_start_usdb_indexer
  regtest_wait_usdb_rpc_ready
  ready "$through"
  capture_final "$checkpoint"
  python3 "$HELPER" compare "$RUN_ROOT/original-final.json" "$RUN_ROOT/$checkpoint-final.json"
  python3 "$HELPER" compare "$RUN_ROOT/continuous.json" "$RUN_ROOT/$checkpoint-history.json"
done
# Keep one live instance across a deep upstream reorg. Replace every block from H1 onward.
for ((round=1; round<=$(plan_value "d['reorg_rounds']"); round++)); do
old_tip=$(regtest_get_bitcoin_tip_height)
reorg_hash=$(regtest_get_bitcoin_block_hash 160)
"$BITCOIN_CLI_BIN" -regtest -datadir="$BITCOIN_DIR" -rpcport="$BTC_RPC_PORT" invalidateblock "$reorg_hash"
while (( $(regtest_get_bitcoin_tip_height) <= old_tip )); do regtest_mine_empty_block "$miner_address"; done
regtest_finish_ord_reorg
reorg_height=$(($(regtest_get_bitcoin_tip_height)-10))
ready "$reorg_height"
regtest_assert_usdb_pass_snapshot_state "$leader" "$reorg_height" active
regtest_assert_usdb_pass_snapshot_state "$fixed" "$reorg_height" active
regtest_assert_usdb_pass_snapshot_state "$address_collab" "$reorg_height" active
regtest_assert_usdb_pass_snapshot_missing "$child" "$reorg_height"
regtest_assert_usdb_pass_snapshot_missing "$invalid" "$reorg_height"
record "reorg-$round"
done
# Replaying the replacement branch from origin must agree with live rollback and recovery.
regtest_stop_usdb_indexer
USDB_INDEXER_ROOT="$WORK_DIR/reorg-clean-indexer"
mkdir -p "$USDB_INDEXER_ROOT"
cp "$original_root/config.json" "$USDB_INDEXER_ROOT/config.json"
regtest_start_usdb_indexer
regtest_wait_usdb_rpc_ready
ready "$reorg_height"
record reorg-clean
python3 "$HELPER" compare "$RUN_ROOT/reorg-$((round-1)).json" "$RUN_ROOT/reorg-clean.json"
# Reusing old USDB headers against the changed BTC branch must fail, not acquire new labels.
run_geth init --datadir "$USDB_CHAIN_WORK_DIR/stale-import" "$GENESIS_JSON" >"$RUN_ROOT/stale-import.log" 2>&1
if timeout 60 "$GETH_BIN" --datadir "$USDB_CHAIN_WORK_DIR/stale-import" --nocompaction import \
  --ethash.usdb-indexer.rpcurl "http://127.0.0.1:$USDB_INDEXER_RPC_PORT" "$RUN_ROOT/canonical.rlp" >>"$RUN_ROOT/stale-import.log" 2>&1; then
  echo "Stale branch headers unexpectedly imported" >&2; exit 1
fi
grep -Ei 'snapshot.*mismatch|system.state.*mismatch|SNAPSHOT_ID_MISMATCH|SYSTEM_STATE_ID_MISMATCH' "$RUN_ROOT/stale-import.log" >/dev/null
# The shared cleanup exits explicitly; isolate that exit and preserve its status.
(cleanup)
trap - EXIT
python3 - "$RUN_ROOT" "$UPGRADE_TOOLS_DIR" "$ROOT_DIR" "$USDB_REPO_DIR" "$CATALOG" <<'PY'
import hashlib,json,subprocess,sys
from pathlib import Path
root,tools,go,usdb,catalog = map(Path,sys.argv[1:])
def sha(path):
    h=hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda:stream.read(1024*1024),b''): h.update(block)
    return h.hexdigest()
report=dict(schema_version='miner-pass-live-upgrade:v2',status='passed',
    source={str(repo):dict(revision=subprocess.check_output(['git','-C',str(repo),'rev-parse','HEAD'],text=True).strip(),dirty=bool(subprocess.check_output(['git','-C',str(repo),'status','--porcelain']))) for repo in [go,usdb]},
    harness_sha256={name:sha(go/'scripts/usdb'/name) for name in ['run_miner_pass_upgrade_services.sh','miner_pass_upgrade_services.py','verify_usdb_profile_e2e.py']},
    protocol_harness_sha256=sha(usdb/'tests/common/protocol_upgrade_live.py'),
    registry_adoption=json.loads((root/'protocol-upgrade/acceptance.json').read_text()),
    upgrade_evidence={str(p.relative_to(root)):sha(p) for dirname in ['protocol-upgrade','late-chain','late-indexer'] for p in (root/dirname).rglob('*.json')},
    catalog_sha256=sha(catalog),binaries={p.name:sha(p) for p in tools.iterdir() if p.is_file()},
    evidence={p.name:sha(p) for p in root.glob('*.json')},
    scenario=json.loads((root/'scenario.json').read_text()),
    checks=['container-preflight','registry-adoption','checkpoint-adoption','adoption-process-recovery','late-upgrade-rejection','pinned-old-registry','core-reference','ord-compare','ordinary-rejection','independent-geth-validator','reward-ledger',
            'schema-inheritance','tightened-collab','transfer','burn','process-restart','checkpoint-catchup','origin-replay','cross-boundary-reorg','stale-header-rejection'])
(root/'report.json').write_text(json.dumps(report,indent=2)+'\n')
PY
regtest_log "MinerPass live upgrade acceptance passed: $RUN_ROOT/report.json"
