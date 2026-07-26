package ethash

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/usdbstate"
	"github.com/ethereum/go-ethereum/internal/usdb"
)

func TestPrepareQuotePolicyTransitionRejectsParentStateMismatch(t *testing.T) {
	config := newTestUSDBRewardChainConfig()
	statedb := newTestStateDB(t)
	initializeTestUSDBRewardState(t, statedb, config, new(big.Int))
	statedb.SetState(
		usdbstate.SystemStateAddress,
		usdbstate.QuotePolicyVersionSlot,
		common.BigToHash(big.NewInt(1)),
	)
	profile := &usdb.ResolvedConsensusProfile{CollabContribution: big.NewInt(7)}
	energy, writes, err := prepareQuotePolicyTransition(
		config,
		&config.USDB.Activations[0],
		statedb,
		1,
		profile,
	)
	if err == nil || !strings.Contains(err.Error(), "parent USDB quote policy mismatch") {
		t.Fatalf("parent quote mismatch was accepted: energy=%v writes=%v err=%v", energy, writes, err)
	}
	if energy != nil || len(writes) != 0 {
		t.Fatalf("parent quote mismatch prepared writes: energy=%v writes=%v", energy, writes)
	}
}
