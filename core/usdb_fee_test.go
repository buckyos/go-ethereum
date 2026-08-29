package core

import (
	"crypto/ecdsa"
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

const (
	usdbFeeTestDAOBps   uint64 = 4_000
	usdbFeeTestBpsScale uint64 = 10_000
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
		USDB: &params.USDBConsensusConfig{
			BTCNetworkID:         "btc-regtest",
			BTCIndexOriginHeight: 1,
			Activations: []params.USDBConsensusActivation{{
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
			}},
		},
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

func TestUSDBTransactionFeeExecutionMatrix(t *testing.T) {
	if usdb.DAOFeeBps != usdbFeeTestDAOBps || usdb.BasisPointDenominator != usdbFeeTestBpsScale {
		t.Fatalf(
			"production fee constants drifted from UIP-0011 v1: dao_bps=%d scale=%d",
			usdb.DAOFeeBps,
			usdb.BasisPointDenominator,
		)
	}
	key := mustUSDBFeeTestKey(t)
	sender := crypto.PubkeyToAddress(key.PublicKey)
	recipient := common.HexToAddress("0x3002")
	miner := common.HexToAddress("0x3003")
	dividend := common.HexToAddress("0x3004")
	refundContract := common.HexToAddress("0x3005")
	dividendCode := []byte{0x00}
	refundCode := common.FromHex("0x600060005500") // SSTORE(0, 0), STOP.
	initialBalance := big.NewInt(1_000_000_000)

	tests := []struct {
		name           string
		policy         uint16
		blockNumber    uint64
		splitBlock     uint64
		dynamicFee     bool
		refund         bool
		wantSplit      bool
		wantGasPrice   int64
		legacyGasPrice int64
	}{
		{
			name:           "policy zero routes all paid fee to miner",
			policy:         0,
			blockNumber:    2,
			splitBlock:     1,
			legacyGasPrice: 3,
			wantGasPrice:   3,
		},
		{
			name:           "policy one before gate routes all paid fee to miner",
			policy:         usdb.FeeSplitPolicyVersionV1,
			blockNumber:    1,
			splitBlock:     2,
			legacyGasPrice: 3,
			wantGasPrice:   3,
		},
		{
			name:           "legacy transaction splits full gas price at gate",
			policy:         usdb.FeeSplitPolicyVersionV1,
			blockNumber:    2,
			splitBlock:     2,
			legacyGasPrice: 3,
			wantGasPrice:   3,
			wantSplit:      true,
		},
		{
			name:         "dynamic transaction splits base fee and tip",
			policy:       usdb.FeeSplitPolicyVersionV1,
			blockNumber:  2,
			splitBlock:   2,
			dynamicFee:   true,
			wantGasPrice: 3,
			wantSplit:    true,
		},
		{
			name:           "refund adjusted gas is the split basis",
			policy:         usdb.FeeSplitPolicyVersionV1,
			blockNumber:    2,
			splitBlock:     2,
			legacyGasPrice: 2,
			wantGasPrice:   2,
			wantSplit:      true,
			refund:         true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := newUSDBFeeTestConfig(test.policy, test.splitBlock, dividend, crypto.Keccak256Hash(dividendCode))
			statedb := newUSDBFeeTestState(t)
			statedb.AddBalance(sender, initialBalance)
			statedb.SetCode(dividend, dividendCode)
			statedb.SetState(dividend, usdbstate.DividendBootstrapFinalizedSlot, common.BigToHash(big.NewInt(1)))

			to := recipient
			gasLimit := uint64(params.TxGas)
			if test.refund {
				to = refundContract
				gasLimit = 100_000
				statedb.SetCode(refundContract, refundCode)
				statedb.SetState(refundContract, common.Hash{}, common.BigToHash(big.NewInt(1)))
			}
			header := newUSDBFeeTestHeader(test.blockNumber, miner)
			var unsigned *types.Transaction
			if test.dynamicFee {
				unsigned = types.NewTx(&types.DynamicFeeTx{
					ChainID:   config.ChainID,
					Nonce:     0,
					GasTipCap: big.NewInt(2),
					GasFeeCap: big.NewInt(5),
					Gas:       gasLimit,
					To:        &to,
					Value:     new(big.Int),
				})
			} else {
				unsigned = types.NewTransaction(0, to, new(big.Int), gasLimit, big.NewInt(test.legacyGasPrice), nil)
			}
			tx := signUSDBFeeTestTransaction(t, config, key, unsigned)
			statedb.Prepare(tx.Hash(), 0)
			gasPool := new(GasPool).AddGas(header.GasLimit)
			var usedGas uint64
			receipt, err := ApplyTransaction(config, nil, &miner, gasPool, statedb, header, tx, &usedGas, vm.Config{})
			if err != nil {
				t.Fatalf("failed to execute signed fee transaction: %v", err)
			}
			message, err := tx.AsMessage(types.MakeSigner(config, header.Number), header.BaseFee)
			if err != nil {
				t.Fatalf("failed to recover signed fee transaction: %v", err)
			}
			if message.GasPrice().Cmp(big.NewInt(test.wantGasPrice)) != 0 {
				t.Fatalf("effective gas price mismatch: have %s want %d", message.GasPrice(), test.wantGasPrice)
			}
			if receipt.GasUsed != usedGas {
				t.Fatalf("receipt gas mismatch: receipt=%d cumulative=%d", receipt.GasUsed, usedGas)
			}
			if test.refund && receipt.GasUsed >= 26_006 {
				t.Fatalf("refund path did not reduce the SSTORE execution charge: used=%d", receipt.GasUsed)
			}

			totalFee := new(big.Int).Mul(new(big.Int).SetUint64(receipt.GasUsed), message.GasPrice())
			wantMiner := new(big.Int).Set(totalFee)
			wantDividend := new(big.Int)
			if test.wantSplit {
				wantMiner, wantDividend = expectedUSDBFeeTestSplit(totalFee)
			}
			assertUSDBFeeTestBalance(t, "miner", statedb.GetBalance(miner), wantMiner)
			assertUSDBFeeTestBalance(t, "Dividend", statedb.GetBalance(dividend), wantDividend)
			assertUSDBFeeTestBalance(t, "sender", statedb.GetBalance(sender), new(big.Int).Sub(initialBalance, totalFee))
			if got := statedb.GetBalance(params.MinerDAOAddress); got.Sign() != 0 {
				t.Fatalf("legacy MinerDAO path received USDB fee: %s", got)
			}
		})
	}
}

func TestUSDBTransactionFeeV1RoundsPerTransaction(t *testing.T) {
	key := mustUSDBFeeTestKey(t)
	sender := crypto.PubkeyToAddress(key.PublicKey)
	miner := common.HexToAddress("0x4003")
	dividend := common.HexToAddress("0x4004")
	contracts := []common.Address{
		common.HexToAddress("0x4010"),
		common.HexToAddress("0x4011"),
	}
	dividendCode := []byte{0x00}
	roundingCode := common.FromHex("0x600000") // PUSH1(0), STOP: stable 21,003 gas.
	config := newUSDBFeeTestConfig(
		usdb.FeeSplitPolicyVersionV1,
		1,
		dividend,
		crypto.Keccak256Hash(dividendCode),
	)
	statedb := newUSDBFeeTestState(t)
	initialBalance := big.NewInt(1_000_000_000)
	statedb.AddBalance(sender, initialBalance)
	statedb.SetCode(dividend, dividendCode)
	statedb.SetState(dividend, usdbstate.DividendBootstrapFinalizedSlot, common.BigToHash(big.NewInt(1)))
	for _, contract := range contracts {
		statedb.SetCode(contract, roundingCode)
	}

	header := newUSDBFeeTestHeader(1, miner)
	gasPool := new(GasPool).AddGas(header.GasLimit)
	var usedGas uint64
	totalFee := new(big.Int)
	wantMiner := new(big.Int)
	wantDividend := new(big.Int)
	for index, contract := range contracts {
		unsigned := types.NewTransaction(uint64(index), contract, new(big.Int), 30_000, big.NewInt(4), nil)
		tx := signUSDBFeeTestTransaction(t, config, key, unsigned)
		statedb.Prepare(tx.Hash(), index)
		receipt, err := ApplyTransaction(config, nil, &miner, gasPool, statedb, header, tx, &usedGas, vm.Config{})
		if err != nil {
			t.Fatalf("failed to execute rounding transaction %d: %v", index, err)
		}
		if receipt.GasUsed != 21_003 {
			t.Fatalf("rounding transaction %d used unexpected gas: %d", index, receipt.GasUsed)
		}
		fee := new(big.Int).Mul(new(big.Int).SetUint64(receipt.GasUsed), big.NewInt(4))
		minerPart, dividendPart := expectedUSDBFeeTestSplit(fee)
		totalFee.Add(totalFee, fee)
		wantMiner.Add(wantMiner, minerPart)
		wantDividend.Add(wantDividend, dividendPart)
	}
	aggregateDividend := new(big.Int).Mul(new(big.Int).Set(totalFee), new(big.Int).SetUint64(usdbFeeTestDAOBps))
	aggregateDividend.Div(aggregateDividend, new(big.Int).SetUint64(usdbFeeTestBpsScale))
	if aggregateDividend.Cmp(wantDividend) == 0 {
		t.Fatalf("test vector does not distinguish per-transaction rounding: fees=%s dao=%s", totalFee, wantDividend)
	}
	assertUSDBFeeTestBalance(t, "per-transaction miner", statedb.GetBalance(miner), wantMiner)
	assertUSDBFeeTestBalance(t, "per-transaction Dividend", statedb.GetBalance(dividend), wantDividend)
	assertUSDBFeeTestBalance(t, "multi-transaction sender", statedb.GetBalance(sender), new(big.Int).Sub(initialBalance, totalFee))
	if distributed := new(big.Int).Add(statedb.GetBalance(miner), statedb.GetBalance(dividend)); distributed.Cmp(totalFee) != 0 {
		t.Fatalf("fee split changed total paid fee: distributed=%s paid=%s", distributed, totalFee)
	}
}

func TestUSDBTransactionFeeGateFailureLeavesTransactionStateUnchanged(t *testing.T) {
	key := mustUSDBFeeTestKey(t)
	sender := crypto.PubkeyToAddress(key.PublicKey)
	recipient := common.HexToAddress("0x5002")
	miner := common.HexToAddress("0x5003")
	dividend := common.HexToAddress("0x5004")
	expectedCode := []byte{0x00}
	initialBalance := big.NewInt(1_000_000)

	tests := []struct {
		name      string
		code      []byte
		marker    int64
		wantError string
	}{
		{name: "missing runtime code", wantError: "runtime code"},
		{name: "wrong runtime code", code: []byte{0x01}, marker: 1, wantError: "runtime code"},
		{name: "missing finalized marker", code: expectedCode, wantError: "not finalized"},
		{name: "wrong finalized marker", code: expectedCode, marker: 2, wantError: "not finalized"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := newUSDBFeeTestConfig(
				usdb.FeeSplitPolicyVersionV1,
				1,
				dividend,
				crypto.Keccak256Hash(expectedCode),
			)
			statedb := newUSDBFeeTestState(t)
			statedb.AddBalance(sender, initialBalance)
			if test.code != nil {
				statedb.SetCode(dividend, test.code)
			}
			if test.marker != 0 {
				statedb.SetState(dividend, usdbstate.DividendBootstrapFinalizedSlot, common.BigToHash(big.NewInt(test.marker)))
			}
			header := newUSDBFeeTestHeader(1, miner)
			tx := signUSDBFeeTestTransaction(
				t,
				config,
				key,
				types.NewTransaction(0, recipient, new(big.Int), params.TxGas, big.NewInt(3), nil),
			)
			statedb.Prepare(tx.Hash(), 0)
			var usedGas uint64
			if receipt, err := ApplyTransaction(
				config,
				nil,
				&miner,
				new(GasPool).AddGas(header.GasLimit),
				statedb,
				header,
				tx,
				&usedGas,
				vm.Config{},
			); err == nil || receipt != nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("invalid Dividend readiness was not rejected: receipt=%v err=%v", receipt, err)
			}
			assertUSDBFeeTestBalance(t, "sender after rejected gate", statedb.GetBalance(sender), initialBalance)
			if statedb.GetNonce(sender) != 0 || usedGas != 0 || statedb.GetBalance(miner).Sign() != 0 || statedb.GetBalance(dividend).Sign() != 0 {
				t.Fatalf(
					"rejected fee gate mutated transaction state: nonce=%d gas=%d miner=%s dividend=%s",
					statedb.GetNonce(sender),
					usedGas,
					statedb.GetBalance(miner),
					statedb.GetBalance(dividend),
				)
			}
		})
	}
}

func mustUSDBFeeTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	if err != nil {
		t.Fatalf("failed to create fee test key: %v", err)
	}
	return key
}

func signUSDBFeeTestTransaction(t *testing.T, config *params.ChainConfig, key *ecdsa.PrivateKey, tx *types.Transaction) *types.Transaction {
	t.Helper()
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(config.ChainID), key)
	if err != nil {
		t.Fatalf("failed to sign fee test transaction: %v", err)
	}
	return signed
}

func newUSDBFeeTestHeader(blockNumber uint64, miner common.Address) *types.Header {
	return &types.Header{
		Number:     new(big.Int).SetUint64(blockNumber),
		Coinbase:   miner,
		GasLimit:   30_000_000,
		Time:       blockNumber + 1,
		Difficulty: big.NewInt(1),
		BaseFee:    big.NewInt(1),
	}
}

func expectedUSDBFeeTestSplit(totalFee *big.Int) (*big.Int, *big.Int) {
	dividend := new(big.Int).Mul(new(big.Int).Set(totalFee), new(big.Int).SetUint64(usdbFeeTestDAOBps))
	dividend.Div(dividend, new(big.Int).SetUint64(usdbFeeTestBpsScale))
	miner := new(big.Int).Sub(new(big.Int).Set(totalFee), dividend)
	return miner, dividend
}

func assertUSDBFeeTestBalance(t *testing.T, label string, got, want *big.Int) {
	t.Helper()
	if got.Cmp(want) != 0 {
		t.Fatalf("%s balance mismatch: have %s want %s", label, got, want)
	}
}
