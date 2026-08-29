package ethash

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/usdbstate"
	"github.com/ethereum/go-ethereum/internal/usdb"
)

const (
	kOracleWindowBlocks uint64 = 50_400
	kOracleBaseBps      uint64 = 10_000
	kOracleMinBps       uint64 = 8_001
	kOracleMaxBps       uint64 = 20_000
	kOracleSteps        uint64 = 50_405
)

func TestPrepareKTransitionMatchesIndependent50405StepOracle(t *testing.T) {
	if usdb.KWindowBlocks != kOracleWindowBlocks ||
		usdb.KBpsBase != kOracleBaseBps ||
		usdb.KBpsMin != kOracleMinBps ||
		usdb.KBpsMax != kOracleMaxBps {
		t.Fatalf(
			"production K constants drifted from UIP-0012 v1: window=%d base=%d min=%d max=%d",
			usdb.KWindowBlocks,
			usdb.KBpsBase,
			usdb.KBpsMin,
			usdb.KBpsMax,
		)
	}

	statedb := newTestStateDB(t)
	oracle := newKTransitionOracle()
	for step := uint64(0); step < kOracleSteps; step++ {
		currentEnergy := kOracleEnergyForStep(step, oracle)
		gotKBps, writes, err := prepareKTransition(
			usdb.CollaborationEfficiencyPolicyVersionV1,
			statedb,
			currentEnergy,
		)
		if err != nil {
			t.Fatalf("K transition failed at step %d: %v", step, err)
		}
		if len(writes) != 7 {
			t.Fatalf("K transition emitted %d writes at step %d, want 7", len(writes), step)
		}
		want := oracle.apply(currentEnergy)
		if gotKBps != want.kBps {
			t.Fatalf("K mismatch at step %d: have %d want %d", step, gotKBps, want.kBps)
		}
		for _, write := range writes {
			statedb.SetState(usdbstate.SystemStateAddress, write.slot, write.value)
		}
		assertKOracleState(t, statedb, step, currentEnergy, oracle, want)
	}
}

type kTransitionOracle struct {
	samples []*big.Int
	sum     *big.Int
	count   uint64
	cursor  uint64
}

type kTransitionOracleResult struct {
	kBps         uint64
	average      *big.Int
	writtenIndex uint64
}

func newKTransitionOracle() *kTransitionOracle {
	return &kTransitionOracle{
		samples: make([]*big.Int, kOracleWindowBlocks),
		sum:     new(big.Int),
	}
}

func (oracle *kTransitionOracle) apply(currentEnergy *big.Int) kTransitionOracleResult {
	average := new(big.Int)
	kBps := kOracleBaseBps
	if oracle.count == kOracleWindowBlocks {
		average.Div(new(big.Int).Set(oracle.sum), new(big.Int).SetUint64(kOracleWindowBlocks))
		kBps = calculateKOracleBps(currentEnergy, average)
	}
	writtenIndex := oracle.cursor
	oldSample := new(big.Int)
	if oracle.count == kOracleWindowBlocks && oracle.samples[writtenIndex] != nil {
		oldSample.Set(oracle.samples[writtenIndex])
	}
	oracle.sum.Sub(oracle.sum, oldSample)
	oracle.sum.Add(oracle.sum, currentEnergy)
	oracle.samples[writtenIndex] = new(big.Int).Set(currentEnergy)
	if oracle.count < kOracleWindowBlocks {
		oracle.count++
	}
	oracle.cursor = (oracle.cursor + 1) % kOracleWindowBlocks
	return kTransitionOracleResult{
		kBps:         kBps,
		average:      average,
		writtenIndex: writtenIndex,
	}
}

func (oracle *kTransitionOracle) average() *big.Int {
	if oracle.count != kOracleWindowBlocks {
		return new(big.Int)
	}
	return new(big.Int).Div(new(big.Int).Set(oracle.sum), new(big.Int).SetUint64(kOracleWindowBlocks))
}

func kOracleEnergyForStep(step uint64, oracle *kTransitionOracle) *big.Int {
	if step < kOracleWindowBlocks {
		// This deterministic sequence is deliberately non-constant and includes
		// zeros, so sum/cursor corruption cannot be masked by identical samples.
		value := (step*7_919 + (step%97)*(step%97)*31) % 1_000_003
		return new(big.Int).SetUint64(value)
	}
	average := oracle.average()
	switch step - kOracleWindowBlocks {
	case 0:
		return new(big.Int) // CE == 0, AE > 0 => minimum K.
	case 1:
		return average // CE == AE => baseline K.
	case 2:
		return new(big.Int).Mul(average, big.NewInt(2)) // CE == 2 * AE => maximum K.
	case 3:
		return new(big.Int).Div(average, big.NewInt(2)) // Penalty branch.
	default:
		return new(big.Int).Add(average, big.NewInt(1))
	}
}

func calculateKOracleBps(currentEnergy, averageEnergy *big.Int) uint64 {
	if averageEnergy.Sign() == 0 {
		return kOracleBaseBps
	}
	if currentEnergy.Cmp(averageEnergy) < 0 {
		numerator := new(big.Int).Mul(big.NewInt(60_000), averageEnergy)
		denominator := new(big.Int).Add(currentEnergy, new(big.Int).Mul(big.NewInt(5), averageEnergy))
		// ceil(numerator / denominator), implemented without production helpers.
		penalty := new(big.Int).Sub(new(big.Int).Add(numerator, denominator), big.NewInt(1))
		penalty.Div(penalty, denominator)
		kBps := new(big.Int).Sub(big.NewInt(20_000), penalty)
		if kBps.Cmp(new(big.Int).SetUint64(kOracleMinBps)) < 0 {
			return kOracleMinBps
		}
		return kBps.Uint64()
	}
	kBps := new(big.Int).Mul(big.NewInt(10_000), currentEnergy)
	kBps.Div(kBps, averageEnergy)
	if kBps.Cmp(new(big.Int).SetUint64(kOracleMaxBps)) > 0 {
		return kOracleMaxBps
	}
	return kBps.Uint64()
}

func assertKOracleState(
	t *testing.T,
	statedb *state.StateDB,
	step uint64,
	currentEnergy *big.Int,
	oracle *kTransitionOracle,
	result kTransitionOracleResult,
) {
	t.Helper()
	checks := []struct {
		name string
		slot common.Hash
		want *big.Int
	}{
		{name: "window sum", slot: usdbstate.KWindowSumSlot, want: oracle.sum},
		{name: "window count", slot: usdbstate.KWindowCountSlot, want: new(big.Int).SetUint64(oracle.count)},
		{name: "window cursor", slot: usdbstate.KWindowCursorSlot, want: new(big.Int).SetUint64(oracle.cursor)},
		{name: "last CE", slot: usdbstate.KLastCESlot, want: currentEnergy},
		{name: "last AE", slot: usdbstate.KLastAESlot, want: result.average},
		{name: "last K", slot: usdbstate.KLastKBpsSlot, want: new(big.Int).SetUint64(result.kBps)},
		{name: "written ring sample", slot: usdbstate.KCERingSlot(result.writtenIndex), want: currentEnergy},
	}
	for _, check := range checks {
		got, err := usdbstate.ReadUint256(statedb, check.slot)
		if err != nil {
			t.Fatalf("failed to read %s at step %d: %v", check.name, step, err)
		}
		if got.Cmp(check.want) != 0 {
			t.Fatalf("%s mismatch at step %d: have %s want %s", check.name, step, got, check.want)
		}
	}
}
