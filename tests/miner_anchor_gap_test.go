package tests

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/usdbstate"
	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/params"
)

type gapHeaderChain struct {
	consensus.ChainHeaderReader
	config *params.ChainConfig
	parent *types.Header
}

func (c *gapHeaderChain) Config() *params.ChainConfig { return c.config }
func (c *gapHeaderChain) GetHeader(hash common.Hash, height uint64) *types.Header {
	if height == c.parent.Number.Uint64() && hash == c.parent.Hash() {
		return c.parent
	}
	return nil
}

// This exercises real RPC adapters, the builder, Ethash preparation, header
// verification and reward finalization. No PoW race or external services run.
func TestUSDBMinerAnchorGapHistoricalBlockEconomics(t *testing.T) {
	encoded, err := os.ReadFile("../internal/usdb/testdata/miner_pass_v2_activation_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Registries []struct {
			ID          string `json:"activation_registry_id"`
			Activations []struct {
				Set usdb.ActiveVersionSet `json:"active_version_set"`
				ID  string                `json:"active_version_set_id"`
			} `json:"activations"`
		} `json:"registries"`
	}
	if err := json.Unmarshal(encoded, &catalog); err != nil {
		t.Fatal(err)
	}
	registry := catalog.Registries[0]
	activation := registry.Activations[0]
	if registry.ID != usdb.BTCRegtestMinerPassV2RegistryID {
		t.Fatal("unexpected fixture registry")
	}
	coinbase := common.HexToAddress("0x1111111111111111111111111111111111111111")
	main := coinbase.Hex()
	views := map[uint32]usdb.PassEconomicProfileView{}
	refs := map[uint32]usdb.HistoricalStateRefInfo{}
	for _, height := range []uint32{100, 102} {
		snapshot, system, pass, energy, total := strings.Repeat("11", 32), strings.Repeat("22", 32), strings.Repeat("33", 32)+"i0", "0", "100000000"
		if height == 102 {
			snapshot, system, pass, energy, total = strings.Repeat("aa", 32), strings.Repeat("bb", 32), strings.Repeat("cc", 32)+"i1", "1000000000000000000", "200000000"
		}
		raw, _ := new(big.Int).SetString(energy, 10)
		effective := new(big.Int).Add(raw, big.NewInt(100))
		level := usdb.LevelForEffectiveEnergy(effective)
		commit, blockHash := strings.Repeat("44", 32), strings.Repeat("55", 32)
		views[height] = usdb.PassEconomicProfileView{
			ViewVersion: usdb.EconomicStateViewVersionV1,
			ExternalState: usdb.EconomicExternalState{BTCHeight: height, SnapshotID: snapshot, SystemStateID: system, StableBlockHash: blockHash, StableLag: 10, LocalStateCommit: commit,
				BalanceHistoryAPIVersion: "1.0.0", BalanceHistorySemanticsVersion: usdb.BalanceHistorySemanticsVersionV1, ActivationRegistryID: registry.ID, ActiveVersionSet: activation.Set, ActiveVersionSetID: activation.ID},
			Pass:           usdb.PassEconomicProfile{PassID: pass, OwnerScriptHash: strings.Repeat("66", 32), State: "active", PassKind: "standard", USDBMain: &main, RawEnergy: energy, CollabContribution: "100", EffectiveEnergy: effective.String(), Level: level, DifficultyFactorBps: usdb.DifficultyFactorBpsForLevel(level), CollabBreakdownCount: 1},
			MinerAggregate: usdb.MinerEconomicAggregate{TotalMinerBTCSats: total, ActiveMinerOwnerCount: 1},
		}
		refs[height] = usdb.HistoricalStateRefInfo{BlockHeight: height,
			SnapshotInfo:         usdb.HistoricalSnapshotInfo{LocalSyncedBlockHeight: height, BalanceHistoryStableHeight: height, SnapshotID: snapshot, StableBlockHash: blockHash},
			LocalStateCommitInfo: usdb.HistoricalLocalStateInfo{LocalSyncedBlockHeight: height, UpstreamSnapshotID: snapshot, LocalStateCommit: commit, ActivationRegistryID: registry.ID, ActiveVersionSet: activation.Set, ActiveVersionSetID: activation.ID},
			SystemStateInfo:      usdb.SystemStateInfo{LocalSyncedBlockHeight: height, UpstreamSnapshotID: snapshot, LocalStateCommit: commit, SystemStateID: system, ActivationRegistryID: registry.ID, ActiveVersionSet: activation.Set, ActiveVersionSetID: activation.ID},
		}
	}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params []struct {
				BlockHeight uint32            `json:"block_height"`
				Context     usdb.QueryContext `json:"context"`
				PassID      string            `json:"pass_id"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		var result interface{}
		if request.Method == "get_system_state_info" {
			result = refs[102].SystemStateInfo
		} else {
			if len(request.Params) != 1 {
				t.Error("wrong arity")
				http.Error(w, "bad params", 400)
				return
			}
			p := request.Params[0]
			view := views[p.BlockHeight]
			if p.BlockHeight != 100 || p.Context.RequestedHeight != 100 || p.Context.ExpectedState.ActivationRegistryID != registry.ID {
				t.Errorf("history not pinned: %+v", p)
				http.Error(w, "wrong height", 400)
				return
			}
			switch request.Method {
			case "get_state_ref_at_height":
				result = refs[100]
			case "resolve_miner_candidate":
				result = usdb.MinerCandidateProfileView{ViewVersion: view.ViewVersion, ExternalState: view.ExternalState, SelectionRule: usdb.MinerCandidateSelectionRuleV1, MatchingCandidateCount: 1, Pass: view.Pass, MinerAggregate: view.MinerAggregate}
			case "get_pass_economic_profile":
				if p.PassID != view.Pass.PassID {
					t.Error("wrong pass ID")
				}
				result = view
			default:
				t.Errorf("unexpected RPC method %s", request.Method)
				http.Error(w, "bad method", 400)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
			t.Error(err)
		}
	}))
	defer endpoint.Close()
	config := &params.ChainConfig{ChainID: big.NewInt(123), HomesteadBlock: big.NewInt(0), USDB: &params.USDBConsensusConfig{
		BTCNetworkID: "btc-regtest", BTCIndexOriginHeight: 1, Activations: []params.USDBConsensusActivation{{BTCActivationRegistryID: registry.ID, BTCAnchorMaxAgeBlocks: 5,
			Versions: params.USDBConsensusVersions{PayloadVersion: 1, BTCAnchorPolicyVersion: 1, DifficultyPolicyVersion: 1, RewardRuleVersion: 1, CoinbaseEmissionPolicyVersion: 1, CollaborationEfficiencyPolicyVersion: 1, PricePolicyVersion: 1}}},
	}}
	builder, err := usdb.NewRPCPayloadBuilder(endpoint.URL, config, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer builder.Close()
	built, err := builder.BuildCurrentPayload(context.Background(), 1, nil, coinbase)
	if err != nil {
		t.Fatal(err)
	}
	engine := ethash.NewWithChainConfig(ethash.Config{USDBIndexer: ethash.USDBIndexerConfig{RPCURL: endpoint.URL}}, config, nil, false)
	defer engine.Close()
	parent := &types.Header{Number: big.NewInt(0), Time: 1000, Difficulty: big.NewInt(131072), GasLimit: 30000000}
	chain := &gapHeaderChain{config: config, parent: parent}
	header := &types.Header{ParentHash: parent.Hash(), Number: big.NewInt(1), Time: 1001, GasLimit: parent.GasLimit, Coinbase: built.RewardRecipient, Extra: built.Payload, UncleHash: types.EmptyUncleHash}
	if err := engine.Prepare(chain, header); err != nil {
		t.Fatal(err)
	}
	base := ethash.CalcDifficulty(config, header.Time, parent)
	wantDifficulty, _ := usdb.RealDifficultyV1(base, views[100].Pass.DifficultyFactorBps)
	latestDifficulty, _ := usdb.RealDifficultyV1(base, views[102].Pass.DifficultyFactorBps)
	if header.Difficulty.Cmp(wantDifficulty) != 0 || header.Difficulty.Cmp(latestDifficulty) == 0 {
		t.Fatalf("used wrong difficulty: %s", header.Difficulty)
	}
	statedb, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatal(err)
	}
	statedb.SetNonce(usdbstate.SystemStateAddress, usdbstate.SystemStateNonce)
	rangeID, err := usdb.FixedPriceRangeIDV1(config.ChainID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for slot, value := range map[common.Hash]common.Hash{
		usdbstate.SystemStateSchemaVersionSlot: common.BigToHash(big.NewInt(1)),
		usdbstate.PriceAtomsPerBTCSlot:         common.BigToHash(usdb.FixedPriceAtomsPerBTCV1()),
		usdbstate.RealPriceAtomsPerBTCSlot:     common.BigToHash(usdb.FixedPriceAtomsPerBTCV1()),
		usdbstate.PricePolicyVersionSlot:       common.BigToHash(big.NewInt(1)),
		usdbstate.PriceSourceKindSlot:          common.BigToHash(big.NewInt(1)),
		usdbstate.PricePolicyRangeIDSlot:       rangeID,
	} {
		statedb.SetState(usdbstate.SystemStateAddress, slot, value)
	}
	block, err := engine.FinalizeAndAssemble(chain, header, statedb, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := statedb.GetBalance(coinbase).String(); got != "634195839675291730" {
		t.Fatalf("historical reward mismatch: %s", got)
	}
	if err := engine.VerifyHeader(chain, block.Header(), false); err != nil {
		t.Fatal(err)
	}
}
