package core

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/usdbstate"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/params"
)

func newUSDBFeeTestState(t *testing.T) *state.StateDB {
	t.Helper()
	statedb, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatalf("failed to create fee test state: %v", err)
	}
	return statedb
}

func newUSDBFeeTestConfig(policy uint16, splitBlock uint64, dividend common.Address, codeHash common.Hash) *params.ChainConfig {
	return &params.ChainConfig{
		ChainID:               big.NewInt(20_260_323),
		HomesteadBlock:        big.NewInt(0),
		EIP150Block:           big.NewInt(0),
		EIP155Block:           big.NewInt(0),
		EIP158Block:           big.NewInt(0),
		ByzantiumBlock:        big.NewInt(0),
		ConstantinopleBlock:   big.NewInt(0),
		PetersburgBlock:       big.NewInt(0),
		IstanbulBlock:         big.NewInt(0),
		BerlinBlock:           big.NewInt(0),
		LondonBlock:           big.NewInt(0),
		DividendFeeSplitBlock: new(big.Int).SetUint64(splitBlock),
		DividendAddress:       dividend,
		DividendCodeHash:      codeHash,
		USDB: &params.USDBConsensusConfig{Activations: []params.USDBConsensusActivation{{
			BTCActivationRegistryID: usdb.BTCRegtestActivationRegistryIDV1,
			BTCAnchorMaxAgeBlocks:   params.USDBDevelopmentBTCAnchorMaxAgeBlocks,
			Versions: params.USDBConsensusVersions{
				PayloadVersion:                       usdb.ProfileSelectorPayloadVersionV1,
				BTCAnchorPolicyVersion:               usdb.BTCAnchorPolicyVersionV1,
				DifficultyPolicyVersion:              usdb.DifficultyPolicyVersionV1,
				RewardRuleVersion:                    usdb.RewardRuleVersionV1,
				CoinbaseEmissionPolicyVersion:        usdb.CoinbaseEmissionPolicyVersionV1,
				FeeSplitPolicyVersion:                policy,
				CollaborationEfficiencyPolicyVersion: usdb.CollaborationEfficiencyPolicyVersionV1,
				PricePolicyVersion:                   usdb.PricePolicyVersionV1,
			},
		}}},
	}
}

func TestResolveUSDBTransactionFeeRoute(t *testing.T) {
	dividend := common.HexToAddress("0x1002")
	code := []byte{0x00}
	codeHash := crypto.Keccak256Hash(code)
	config := newUSDBFeeTestConfig(usdb.FeeSplitPolicyVersionV1, 10, dividend, codeHash)
	statedb := newUSDBFeeTestState(t)

	if route, err := resolveTransactionFeeRoute(config, big.NewInt(9), statedb); err != nil || route != usdbMinerTransactionFeeRoute {
		t.Fatalf("pre-gate route mismatch: route=%d err=%v", route, err)
	}
	if _, err := resolveTransactionFeeRoute(config, big.NewInt(10), statedb); err == nil ||
		!strings.Contains(err.Error(), "runtime code") {
		t.Fatalf("missing Dividend code was not rejected: %v", err)
	}

	statedb.SetCode(dividend, code)
	if _, err := resolveTransactionFeeRoute(config, big.NewInt(10), statedb); err == nil ||
		!strings.Contains(err.Error(), "not finalized") {
		t.Fatalf("missing readiness marker was not rejected: %v", err)
	}
	statedb.SetState(dividend, usdbstate.DividendBootstrapFinalizedSlot, common.BigToHash(big.NewInt(1)))
	if route, err := resolveTransactionFeeRoute(config, big.NewInt(10), statedb); err != nil || route != usdbSplitTransactionFeeRoute {
		t.Fatalf("ready fee route mismatch: route=%d err=%v", route, err)
	}

	config.USDB.Activations[0].Versions.FeeSplitPolicyVersion = 0
	config.DividendFeeSplitBlock = nil
	config.DividendAddress = common.Address{}
	config.DividendCodeHash = common.Hash{}
	if route, err := resolveTransactionFeeRoute(config, big.NewInt(10), statedb); err != nil || route != usdbMinerTransactionFeeRoute {
		t.Fatalf("policy-zero fee route mismatch: route=%d err=%v", route, err)
	}

	for _, invalid := range []*big.Int{
		big.NewInt(-1),
		new(big.Int).Lsh(big.NewInt(1), 64),
	} {
		if _, err := resolveTransactionFeeRoute(config, invalid, statedb); err == nil {
			t.Fatalf("invalid block number %v was accepted", invalid)
		}
	}
}

func TestUSDBTransactionFeeV1SplitsRefundAdjustedFee(t *testing.T) {
	sender := common.HexToAddress("0x2001")
	recipient := common.HexToAddress("0x2002")
	miner := common.HexToAddress("0x2003")
	dividend := common.HexToAddress("0x2004")
	code := []byte{0x00}
	config := newUSDBFeeTestConfig(
		usdb.FeeSplitPolicyVersionV1,
		1,
		dividend,
		crypto.Keccak256Hash(code),
	)
	statedb := newUSDBFeeTestState(t)
	initialBalance := big.NewInt(1_000_000)
	statedb.AddBalance(sender, initialBalance)
	statedb.SetCode(dividend, code)
	statedb.SetState(dividend, usdbstate.DividendBootstrapFinalizedSlot, common.BigToHash(big.NewInt(1)))

	gasPrice := big.NewInt(3)
	message := types.NewMessage(
		sender,
		&recipient,
		0,
		big.NewInt(0),
		params.TxGas,
		gasPrice,
		big.NewInt(3),
		big.NewInt(2),
		nil,
		nil,
		false,
	)
	blockContext := vm.BlockContext{
		CanTransfer: CanTransfer,
		Transfer:    Transfer,
		GetHash:     func(uint64) common.Hash { return common.Hash{} },
		Coinbase:    miner,
		GasLimit:    30_000_000,
		BlockNumber: big.NewInt(1),
		Time:        big.NewInt(1),
		Difficulty:  big.NewInt(1),
		BaseFee:     big.NewInt(1),
	}
	evm := vm.NewEVM(
		blockContext,
		NewEVMTxContext(message),
		statedb,
		config,
		vm.Config{},
	)
	gasPool := new(GasPool).AddGas(params.TxGas)
	result, err := ApplyMessage(evm, message, gasPool)
	if err != nil {
		t.Fatalf("failed to apply USDB fee-split message: %v", err)
	}
	if result.UsedGas != params.TxGas {
		t.Fatalf("unexpected gas used: %d", result.UsedGas)
	}
	const (
		totalFee = 63_000
		minerFee = 37_800
		daoFee   = 25_200
	)
	if got := statedb.GetBalance(miner); got.Cmp(big.NewInt(minerFee)) != 0 {
		t.Fatalf("unexpected miner fee: have %s want %d", got, minerFee)
	}
	if got := statedb.GetBalance(dividend); got.Cmp(big.NewInt(daoFee)) != 0 {
		t.Fatalf("unexpected Dividend fee: have %s want %d", got, daoFee)
	}
	if got := statedb.GetBalance(sender); got.Cmp(new(big.Int).Sub(initialBalance, big.NewInt(totalFee))) != 0 {
		t.Fatalf("unexpected sender balance: %s", got)
	}
	if got := statedb.GetBalance(params.MinerDAOAddress); got.Sign() != 0 {
		t.Fatalf("legacy MinerDAO path received USDB fee: %s", got)
	}
}
