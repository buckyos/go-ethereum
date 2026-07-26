//go:build usdb_economic_conformance_v3
// +build usdb_economic_conformance_v3

package ethash

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/usdbstate"
	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/params"
)

func TestEconomicActivationConformanceThreeStageReplay(t *testing.T) {
	const (
		v2Block = uint64(2)
		v3Block = uint64(4)
		last    = uint64(5)
	)
	config := newTestUSDBRewardChainConfig()
	v2 := config.USDB.Activations[0]
	v2.Block = v2Block
	v2.Versions.QuotePolicyVersion = usdb.QuotePolicyVersionActivationConformanceV2
	v2.Versions.AuxPoolPolicyVersion = usdb.AuxPoolPolicyVersionActivationConformanceV2
	v3 := v2
	v3.Block = v3Block
	v3.Versions.QuotePolicyVersion = usdb.QuotePolicyVersionActivationConformanceV3
	v3.Versions.AuxPoolPolicyVersion = usdb.AuxPoolPolicyVersionActivationConformanceV3
	config.USDB.Activations = append(config.USDB.Activations, v2, v3)

	miner := common.HexToAddress("0x1008")
	profile := &usdb.ResolvedConsensusProfile{
		RawEnergy:           big.NewInt(1_000_000),
		CollabContribution:  big.NewInt(20_000_000),
		EffectiveEnergy:     big.NewInt(21_000_000),
		DifficultyFactorBps: usdb.DifficultyFactorBpsForLevel(usdb.LevelForEffectiveEnergy(big.NewInt(21_000_000))),
		RewardRecipient:     miner,
		TotalMinerBTCSats:   big.NewInt(100_000_000),
	}
	stateDatabase := state.NewDatabase(rawdb.NewMemoryDatabase())
	genesisState, err := state.New(common.Hash{}, stateDatabase, nil)
	if err != nil {
		t.Fatalf("create genesis state: %v", err)
	}
	initializeTestUSDBRewardState(t, genesisState, config, new(big.Int))
	genesisRoot := commitEconomicConformanceState(t, stateDatabase, genesisState)

	firstRoot, parentV3Root, finalRoot := replayEconomicConformanceRewards(
		t,
		config,
		stateDatabase,
		genesisRoot,
		profile,
		v3Block,
		last,
	)
	if firstRoot == genesisRoot || parentV3Root == firstRoot || finalRoot == parentV3Root {
		t.Fatalf("activation stages did not change state roots")
	}

	finalState, err := state.New(finalRoot, stateDatabase, nil)
	if err != nil {
		t.Fatalf("reopen final state: %v", err)
	}
	if got, err := usdbstate.ReadUint256(finalState, usdbstate.QuotePolicyVersionSlot); err != nil ||
		got.Uint64() != uint64(usdb.QuotePolicyVersionActivationConformanceV3) {
		t.Fatalf("unexpected final quote policy: value=%v err=%v", got, err)
	}
	if got, err := usdbstate.ReadUint256(finalState, usdbstate.KLastCESlot); err != nil ||
		got.Cmp(profile.CollabContribution) != 0 {
		t.Fatalf("fake v3 did not restore collaboration energy: value=%v err=%v", got, err)
	}
	issued, err := usdbstate.ReadUint256(finalState, usdbstate.IssuedUSDBAtomsSlot)
	if err != nil {
		t.Fatalf("read final issued supply: %v", err)
	}
	auxV2Recipient := common.HexToAddress(usdb.ActivationConformanceAuxPoolRecipientV2Hex)
	auxV3Recipient := common.HexToAddress(usdb.ActivationConformanceAuxPoolRecipientV3Hex)
	totalCredited := new(big.Int).Add(finalState.GetBalance(miner), finalState.GetBalance(auxV2Recipient))
	totalCredited.Add(totalCredited, finalState.GetBalance(auxV3Recipient))
	if totalCredited.Cmp(issued) != 0 {
		t.Fatalf("reward split changed issued supply: credited=%s issued=%s", totalCredited, issued)
	}
	if finalState.GetBalance(auxV2Recipient).Sign() == 0 ||
		finalState.GetBalance(auxV3Recipient).Sign() == 0 {
		t.Fatalf("activation stages did not credit both auxiliary recipients")
	}

	restartedRoot := replayEconomicConformanceBlock(
		t,
		config,
		stateDatabase,
		parentV3Root,
		profile,
		v3Block,
	)
	if restartedRoot != replayEconomicConformanceBlock(
		t,
		config,
		stateDatabase,
		parentV3Root,
		profile,
		v3Block,
	) {
		t.Fatalf("same-parent fake v3 replay produced different roots")
	}
	if restartedRoot == parentV3Root {
		t.Fatalf("fake v3 boundary replay did not change state")
	}

	_, _, freshReplayRoot := replayEconomicConformanceRewards(
		t,
		config,
		stateDatabase,
		genesisRoot,
		profile,
		v3Block,
		last,
	)
	if freshReplayRoot != finalRoot {
		t.Fatalf("fresh replay root mismatch: have %s want %s", freshReplayRoot, finalRoot)
	}
}

func TestEconomicActivationConformanceSplitFailureIsAtomic(t *testing.T) {
	config := newTestUSDBRewardChainConfig()
	v2 := config.USDB.Activations[0]
	v2.Block = 1
	v2.Versions.QuotePolicyVersion = usdb.QuotePolicyVersionActivationConformanceV2
	v2.Versions.AuxPoolPolicyVersion = usdb.AuxPoolPolicyVersionActivationConformanceV2
	config.USDB.Activations = append(config.USDB.Activations, v2)

	statedb := newTestStateDB(t)
	initializeTestUSDBRewardState(t, statedb, config, new(big.Int))
	beforeRoot := statedb.IntermediateRoot(false)
	auxRecipient := common.HexToAddress(usdb.ActivationConformanceAuxPoolRecipientV2Hex)
	profile := &usdb.ResolvedConsensusProfile{
		RawEnergy:           big.NewInt(1),
		CollabContribution:  big.NewInt(2),
		EffectiveEnergy:     big.NewInt(3),
		DifficultyFactorBps: usdb.BasisPointDenominator,
		RewardRecipient:     auxRecipient,
		TotalMinerBTCSats:   big.NewInt(100_000_000),
	}
	header := &types.Header{
		Number:   big.NewInt(1),
		Coinbase: auxRecipient,
	}
	if transition, err := prepareUSDBRewardTransition(
		config,
		&config.USDB.Activations[1],
		statedb,
		header,
		profile,
	); err == nil || transition != nil {
		t.Fatalf("aux recipient collision was accepted: transition=%v err=%v", transition, err)
	}
	if afterRoot := statedb.IntermediateRoot(false); afterRoot != beforeRoot {
		t.Fatalf("failed reward preparation changed state root: have %s want %s", afterRoot, beforeRoot)
	}
	if statedb.GetBalance(auxRecipient).Sign() != 0 {
		t.Fatalf("failed reward preparation changed auxiliary balance")
	}
	for _, slot := range []common.Hash{
		usdbstate.IssuedUSDBAtomsSlot,
		usdbstate.KWindowCountSlot,
		usdbstate.QuotePolicyVersionSlot,
	} {
		if value := statedb.GetState(usdbstate.SystemStateAddress, slot); value != (common.Hash{}) {
			t.Fatalf("failed reward preparation changed slot %s: %s", slot, value)
		}
	}
}

func replayEconomicConformanceRewards(
	t *testing.T,
	config *params.ChainConfig,
	database state.Database,
	parentRoot common.Hash,
	profile *usdb.ResolvedConsensusProfile,
	v3Block uint64,
	last uint64,
) (common.Hash, common.Hash, common.Hash) {
	t.Helper()
	var firstRoot, parentV3Root common.Hash
	for number := uint64(1); number <= last; number++ {
		if number == v3Block {
			parentV3Root = parentRoot
		}
		parentRoot = replayEconomicConformanceBlock(t, config, database, parentRoot, profile, number)
		if number == 1 {
			firstRoot = parentRoot
		}
	}
	return firstRoot, parentV3Root, parentRoot
}

func replayEconomicConformanceBlock(
	t *testing.T,
	config *params.ChainConfig,
	database state.Database,
	parentRoot common.Hash,
	profile *usdb.ResolvedConsensusProfile,
	number uint64,
) common.Hash {
	t.Helper()
	statedb, err := state.New(parentRoot, database, nil)
	if err != nil {
		t.Fatalf("reopen state before block %d: %v", number, err)
	}
	activation, err := config.USDBActivationAt(number)
	if err != nil {
		t.Fatalf("resolve activation at block %d: %v", number, err)
	}
	header := &types.Header{
		Number:   new(big.Int).SetUint64(number),
		Coinbase: profile.RewardRecipient,
	}
	transition, err := prepareUSDBRewardTransition(config, activation, statedb, header, profile)
	if err != nil {
		t.Fatalf("prepare reward at block %d: %v", number, err)
	}
	applyUSDBRewardTransition(statedb, transition)
	return commitEconomicConformanceState(t, database, statedb)
}

func commitEconomicConformanceState(t *testing.T, database state.Database, statedb *state.StateDB) common.Hash {
	t.Helper()
	root, err := statedb.Commit(false)
	if err != nil {
		t.Fatalf("commit conformance state: %v", err)
	}
	if err := database.TrieDB().Commit(root, false, nil); err != nil {
		t.Fatalf("persist conformance state: %v", err)
	}
	return root
}
