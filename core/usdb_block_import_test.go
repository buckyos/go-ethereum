package core

import (
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/usdbstate"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

const usdbImportTestActiveVersionSetID = "01d1d45f342994690d8ae27ac3d8538ad31e5f81f8e948c838067b3b52f94691"

func TestUSDBBlockImportCommitsRewardState(t *testing.T) {
	rewardRecipient := common.HexToAddress("0x1111111111111111111111111111111111111111")
	genesis := newUSDBImportTestGenesis(t)
	profile := newUSDBImportTestProfile(t, rewardRecipient, "100000000")
	server := newUSDBImportTestProfileServer(t, profile)
	defer server.Close()

	producerDB := rawdb.NewMemoryDatabase()
	producerGenesis := genesis.MustCommit(producerDB)
	producer := newUSDBImportTestEngine(genesis.Config, server.URL)
	defer producer.Close()
	blocks := generateUSDBImportTestBlocks(t, genesis.Config, producerGenesis, producer, producerDB, rewardRecipient, 2)

	validatorDB := rawdb.NewMemoryDatabase()
	validatorGenesis := genesis.MustCommit(validatorDB)
	validator := newUSDBImportTestEngine(genesis.Config, server.URL)
	defer validator.Close()
	chain, err := NewBlockChain(validatorDB, nil, genesis.Config, validator, vm.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("failed to create USDB import chain: %v", err)
	}
	defer chain.Stop()

	if index, err := chain.InsertChain(blocks); err != nil {
		t.Fatalf("failed to import valid USDB blocks at index %d: %v", index, err)
	}
	if head := chain.CurrentBlock(); head.Hash() != blocks[len(blocks)-1].Hash() {
		t.Fatalf("unexpected canonical head: have %s want %s", head.Hash(), blocks[len(blocks)-1].Hash())
	}
	if chain.GetBlockByNumber(validatorGenesis.NumberU64()).Hash() != validatorGenesis.Hash() {
		t.Fatal("USDB import replaced the configured genesis block")
	}
	statedb, err := chain.State()
	if err != nil {
		t.Fatalf("failed to open imported USDB state: %v", err)
	}
	issued, err := usdbstate.ReadUint256(statedb, usdbstate.IssuedUSDBAtomsSlot)
	if err != nil {
		t.Fatalf("failed to read imported issued supply: %v", err)
	}
	if issued.Sign() <= 0 || statedb.GetBalance(rewardRecipient).Cmp(issued) != 0 {
		t.Fatalf("imported reward ledger mismatch: recipient=%s issued=%s", statedb.GetBalance(rewardRecipient), issued)
	}
	if count, err := usdbstate.ReadUint256(statedb, usdbstate.KWindowCountSlot); err != nil || count.Uint64() != uint64(len(blocks)) {
		t.Fatalf("imported K sample count mismatch: count=%v err=%v", count, err)
	}
}

func TestUSDBBlockImportRejectsProfileRewardMismatch(t *testing.T) {
	rewardRecipient := common.HexToAddress("0x1111111111111111111111111111111111111111")
	genesis := newUSDBImportTestGenesis(t)
	producerProfile := newUSDBImportTestProfile(t, rewardRecipient, "100000000")
	producerServer := newUSDBImportTestProfileServer(t, producerProfile)
	defer producerServer.Close()

	producerDB := rawdb.NewMemoryDatabase()
	producerGenesis := genesis.MustCommit(producerDB)
	producer := newUSDBImportTestEngine(genesis.Config, producerServer.URL)
	defer producer.Close()
	blocks := generateUSDBImportTestBlocks(t, genesis.Config, producerGenesis, producer, producerDB, rewardRecipient, 1)

	// Keep selector identity, energy, difficulty, and recipient valid while changing
	// only the reward aggregate. Header verification succeeds, but block execution
	// must derive a different state root and reject the producer's block.
	validatorProfile := newUSDBImportTestProfile(t, rewardRecipient, "200000000")
	validatorServer := newUSDBImportTestProfileServer(t, validatorProfile)
	defer validatorServer.Close()
	validatorDB := rawdb.NewMemoryDatabase()
	validatorGenesis := genesis.MustCommit(validatorDB)
	validator := newUSDBImportTestEngine(genesis.Config, validatorServer.URL)
	defer validator.Close()
	chain, err := NewBlockChain(validatorDB, nil, genesis.Config, validator, vm.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("failed to create mismatched USDB import chain: %v", err)
	}
	defer chain.Stop()

	index, err := chain.InsertChain(blocks)
	if err == nil || !strings.Contains(err.Error(), "invalid merkle root") {
		t.Fatalf("profile reward mismatch was not rejected by state-root validation: index=%d err=%v", index, err)
	}
	if head := chain.CurrentBlock(); head.Hash() != validatorGenesis.Hash() {
		t.Fatalf("rejected block changed canonical head: have %s want genesis %s", head.Hash(), validatorGenesis.Hash())
	}
	statedb, stateErr := chain.State()
	if stateErr != nil {
		t.Fatalf("failed to reopen state after rejected import: %v", stateErr)
	}
	if got := statedb.GetBalance(rewardRecipient); got.Sign() != 0 {
		t.Fatalf("rejected import leaked reward balance: %s", got)
	}
}

func newUSDBImportTestGenesis(t *testing.T) *Genesis {
	t.Helper()
	config := newUSDBFeeTestConfig(0, 1, common.Address{}, common.Hash{})
	config.DividendFeeSplitBlock = nil
	config.EthPoWMinimumDifficultyOverride = new(big.Int).Set(params.MinimumDifficulty)
	alloc := GenesisAlloc{}
	if err := initializeUSDBGenesisSystemState(alloc, config); err != nil {
		t.Fatalf("failed to initialize USDB import genesis state: %v", err)
	}
	return &Genesis{
		Config:     config,
		GasLimit:   30_000_000,
		Difficulty: new(big.Int).Set(params.MinimumDifficulty),
		BaseFee:    new(big.Int).SetUint64(params.InitialBaseFee),
		Alloc:      alloc,
	}
}

func newUSDBImportTestEngine(config *params.ChainConfig, rpcURL string) *ethash.Ethash {
	return ethash.NewWithChainConfig(ethash.Config{
		CachesInMem: 1,
		PowMode:     ethash.ModeFake,
		Log:         log.Root(),
		USDBIndexer: ethash.USDBIndexerConfig{
			RPCURL:       rpcURL,
			QueryTimeout: time.Second,
		},
	}, config, nil, false)
}

func generateUSDBImportTestBlocks(
	t *testing.T,
	config *params.ChainConfig,
	genesis *types.Block,
	engine *ethash.Ethash,
	db ethdb.Database,
	rewardRecipient common.Address,
	count int,
) types.Blocks {
	t.Helper()
	blocks, _ := GenerateChain(config, genesis, engine, db, count, func(index int, block *BlockGen) {
		block.SetCoinbase(rewardRecipient)
		block.SetExtra(newUSDBImportTestSelector(t, uint32(index)))
		block.header.UncleHash = types.EmptyUncleHash
	})
	for index, block := range blocks {
		if block == nil {
			t.Fatalf("producer failed to assemble USDB block %d", index+1)
		}
	}
	return blocks
}

func newUSDBImportTestSelector(t *testing.T, anchorAge uint32) []byte {
	t.Helper()
	payload, err := usdb.NewProfileSelectorPayload(
		usdb.DifficultyPolicyVersionV1,
		123,
		anchorAge,
		strings.Repeat("11", common.HashLength),
		strings.Repeat("22", common.HashLength),
		strings.Repeat("33", common.HashLength)+"i7",
	)
	if err != nil {
		t.Fatalf("failed to create USDB import selector: %v", err)
	}
	encoded, err := payload.MarshalBinary()
	if err != nil {
		t.Fatalf("failed to encode USDB import selector: %v", err)
	}
	return encoded
}

func newUSDBImportTestProfile(t *testing.T, rewardRecipient common.Address, totalMinerBTCSats string) *usdb.PassEconomicProfileView {
	t.Helper()
	activeVersions := usdb.ActiveVersionSet{
		"inscription_schema_version":        json.RawMessage(`"uip-0001-miner-pass-inscription:v1"`),
		"pass_state_machine_version":        json.RawMessage(`"uip-0002-pass-state-machine:v1"`),
		"energy_formula_version":            json.RawMessage(`"uip-0003-pass-energy-formula:v1"`),
		"effective_energy_formula_version":  json.RawMessage(`"uip-0004-collab-leader-effective-energy:v1"`),
		"level_formula_version":             json.RawMessage(`"uip-0005-level-and-real-difficulty:v1"`),
		"query_semantics_version":           json.RawMessage(`"uip-0006-economic-query-semantics:v1"`),
		"state_view_version":                json.RawMessage(`"uip-0006-usdb-economic-state-view:v1"`),
		"commit_protocol_version":           json.RawMessage(`"uip-0008-usdb-local-state-commit:v1"`),
		"balance_history_semantics_version": json.RawMessage(`"balance-snapshot-at-or-before:v1"`),
	}
	if id, err := activeVersions.ID(); err != nil || id != usdbImportTestActiveVersionSetID {
		t.Fatalf("unexpected test active version identity: id=%s err=%v", id, err)
	}
	recipient := rewardRecipient.Hex()
	return &usdb.PassEconomicProfileView{
		ViewVersion: usdb.EconomicStateViewVersionV1,
		ExternalState: usdb.EconomicExternalState{
			BTCHeight:                      123,
			SnapshotID:                     strings.Repeat("11", common.HashLength),
			StableBlockHash:                strings.Repeat("44", common.HashLength),
			StableLag:                      10,
			LocalStateCommit:               strings.Repeat("55", common.HashLength),
			SystemStateID:                  strings.Repeat("22", common.HashLength),
			BalanceHistoryAPIVersion:       "1.0.0",
			BalanceHistorySemanticsVersion: usdb.BalanceHistorySemanticsVersionV1,
			ActivationRegistryID:           usdb.BTCRegtestActivationRegistryIDV1,
			ActiveVersionSet:               activeVersions,
			ActiveVersionSetID:             usdbImportTestActiveVersionSetID,
		},
		Pass: usdb.PassEconomicProfile{
			PassID:              strings.Repeat("33", common.HashLength) + "i7",
			OwnerScriptHash:     strings.Repeat("66", common.HashLength),
			State:               "active",
			PassKind:            "standard",
			USDBMain:            &recipient,
			RawEnergy:           "0",
			CollabContribution:  "0",
			EffectiveEnergy:     "0",
			Level:               0,
			DifficultyFactorBps: usdb.BasisPointDenominator,
		},
		MinerAggregate: usdb.MinerEconomicAggregate{
			TotalMinerBTCSats:     totalMinerBTCSats,
			ActiveMinerOwnerCount: 1,
		},
	}
}

func newUSDBImportTestProfileServer(t *testing.T, profile *usdb.PassEconomicProfileView) *httptest.Server {
	t.Helper()
	type rpcRequest struct {
		JSONRPC string            `json:"jsonrpc"`
		ID      json.RawMessage   `json:"id"`
		Method  string            `json:"method"`
		Params  []json.RawMessage `json:"params"`
	}
	type rpcResponse struct {
		JSONRPC string                        `json:"jsonrpc"`
		ID      json.RawMessage               `json:"id"`
		Result  *usdb.PassEconomicProfileView `json:"result"`
	}
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		var rpcRequest rpcRequest
		if err := json.NewDecoder(request.Body).Decode(&rpcRequest); err != nil {
			t.Errorf("failed to decode profile RPC request: %v", err)
			http.Error(writer, "invalid JSON-RPC request", http.StatusBadRequest)
			return
		}
		if rpcRequest.Method != "get_pass_economic_profile" || len(rpcRequest.Params) != 1 {
			t.Errorf("unexpected profile RPC request: method=%q params=%d", rpcRequest.Method, len(rpcRequest.Params))
			http.Error(writer, "unexpected JSON-RPC request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(writer).Encode(rpcResponse{JSONRPC: "2.0", ID: rpcRequest.ID, Result: profile}); err != nil {
			t.Errorf("failed to encode profile RPC response: %v", err)
		}
	}))
}
