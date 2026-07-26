//go:build usdb_economic_conformance_v2 && !usdb_economic_conformance_v3
// +build usdb_economic_conformance_v2,!usdb_economic_conformance_v3

package usdb

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestEconomicActivationConformanceV2Only(t *testing.T) {
	profile := &ResolvedConsensusProfile{
		RawEnergy:          big.NewInt(1_000_000),
		CollabContribution: big.NewInt(20_000_000),
		EffectiveEnergy:    big.NewInt(21_000_000),
	}
	result, handled, err := ResolveActivationConformanceQuotePolicy(
		QuotePolicyVersionActivationConformanceV2,
		profile,
	)
	if err != nil || !handled {
		t.Fatalf("fake v2 quote was not handled: result=%v handled=%t err=%v", result, handled, err)
	}
	wantFactor := DifficultyFactorBpsForLevel(LevelForEffectiveEnergy(profile.RawEnergy))
	if result.DifficultyFactorBps != wantFactor || result.CollaborationEnergy.Sign() != 0 {
		t.Fatalf("unexpected fake v2 quote result: %+v", result)
	}
	if result, handled, err := ResolveActivationConformanceQuotePolicy(
		QuotePolicyVersionActivationConformanceV3,
		profile,
	); err != nil || handled || result != nil {
		t.Fatalf("v2 build claimed fake v3 quote: result=%v handled=%t err=%v", result, handled, err)
	}

	split, handled, err := ResolveActivationConformanceAuxPoolPolicy(
		AuxPoolPolicyVersionActivationConformanceV2,
		big.NewInt(101),
	)
	if err != nil || !handled {
		t.Fatalf("fake v2 aux was not handled: split=%v handled=%t err=%v", split, handled, err)
	}
	if split.MinerReward.Cmp(big.NewInt(91)) != 0 ||
		split.AuxReward.Cmp(big.NewInt(10)) != 0 ||
		split.AuxRecipient != common.HexToAddress(ActivationConformanceAuxPoolRecipientV2Hex) {
		t.Fatalf("unexpected fake v2 aux split: %+v", split)
	}
	if split, handled, err := ResolveActivationConformanceAuxPoolPolicy(
		AuxPoolPolicyVersionActivationConformanceV3,
		big.NewInt(101),
	); err != nil || handled || split != nil {
		t.Fatalf("v2 build claimed fake v3 aux: split=%v handled=%t err=%v", split, handled, err)
	}
}
