package ethash

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/usdbstate"
	"github.com/ethereum/go-ethereum/internal/usdb"
)

func TestPrepareQuotePolicyTransitionDisabledUsesNominalCollaborationEnergy(t *testing.T) {
	config := newTestUSDBRewardChainConfig()
	statedb := newTestStateDB(t)
	initializeTestUSDBRewardState(t, statedb, config, new(big.Int))
	decision, err := usdb.ResolveQuotePolicy(
		usdb.QuotePolicyVersionDisabled,
		usdb.QuotePolicyContext{
			Profile: &usdb.ResolvedConsensusProfile{
				RawEnergy:          big.NewInt(1),
				CollabContribution: big.NewInt(2),
				EffectiveEnergy:    big.NewInt(3),
			},
		},
	)
	if err != nil {
		t.Fatalf("resolve disabled quote policy: %v", err)
	}
	energy, writes, err := prepareQuotePolicyTransition(
		config,
		&config.USDB.Activations[0],
		statedb,
		1,
		decision,
	)
	if err != nil {
		t.Fatalf("prepare disabled quote transition: %v", err)
	}
	if energy.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("disabled quote policy changed nominal collaboration energy: %s", energy)
	}
	if len(writes) != 1 ||
		writes[0].slot != usdbstate.QuotePolicyVersionSlot ||
		writes[0].value != (common.Hash{}) {
		t.Fatalf("disabled quote policy prepared unexpected state: %+v", writes)
	}
	if got := statedb.GetState(usdbstate.SystemStateAddress, usdbstate.LeaderQuoteWindowBlocksSlot); got != (common.Hash{}) {
		t.Fatalf("disabled quote policy wrote quote window: %s", got)
	}
}

func TestPrepareQuotePolicyTransitionRejectsParentStateMismatch(t *testing.T) {
	config := newTestUSDBRewardChainConfig()
	statedb := newTestStateDB(t)
	initializeTestUSDBRewardState(t, statedb, config, new(big.Int))
	statedb.SetState(
		usdbstate.SystemStateAddress,
		usdbstate.QuotePolicyVersionSlot,
		common.BigToHash(big.NewInt(1)),
	)
	decision := &usdb.QuotePolicyDecision{
		PolicyVersion:       usdb.QuotePolicyVersionDisabled,
		CollaborationEnergy: big.NewInt(7),
	}
	energy, writes, err := prepareQuotePolicyTransition(
		config,
		&config.USDB.Activations[0],
		statedb,
		1,
		decision,
	)
	if err == nil || !strings.Contains(err.Error(), "parent USDB quote policy mismatch") {
		t.Fatalf("parent quote mismatch was accepted: energy=%v writes=%v err=%v", energy, writes, err)
	}
	if energy != nil || len(writes) != 0 {
		t.Fatalf("parent quote mismatch prepared writes: energy=%v writes=%v", energy, writes)
	}
}

func TestPrepareQuotePolicyTransitionRejectsDecisionVersionMismatch(t *testing.T) {
	config := newTestUSDBRewardChainConfig()
	statedb := newTestStateDB(t)
	initializeTestUSDBRewardState(t, statedb, config, new(big.Int))
	beforeRoot := statedb.IntermediateRoot(false)

	energy, writes, err := prepareQuotePolicyTransition(
		config,
		&config.USDB.Activations[0],
		statedb,
		1,
		&usdb.QuotePolicyDecision{
			PolicyVersion:       usdb.QuotePolicyVersionV1,
			CollaborationEnergy: big.NewInt(7),
		},
	)
	if err == nil || !strings.Contains(err.Error(), "does not match active policy") {
		t.Fatalf("quote decision mismatch was accepted: energy=%v writes=%v err=%v", energy, writes, err)
	}
	if energy != nil || len(writes) != 0 {
		t.Fatalf("quote decision mismatch prepared writes: energy=%v writes=%v", energy, writes)
	}
	if afterRoot := statedb.IntermediateRoot(false); afterRoot != beforeRoot {
		t.Fatalf("quote decision mismatch changed state: have %s want %s", afterRoot, beforeRoot)
	}
}
