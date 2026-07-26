package usdb

import (
	"math/big"
	"strings"
	"testing"
)

func TestQuotePolicyDisabledUsesNominalProfile(t *testing.T) {
	profile := testQuotePolicyProfile()
	decision, err := ResolveQuotePolicy(QuotePolicyVersionDisabled, QuotePolicyContext{
		Profile: profile,
	})
	if err != nil {
		t.Fatalf("resolve disabled quote policy: %v", err)
	}
	if decision.PolicyVersion != QuotePolicyVersionDisabled ||
		decision.CandidateEnergy.Cmp(profile.EffectiveEnergy) != 0 ||
		decision.CollaborationEnergy.Cmp(profile.CollabContribution) != 0 ||
		decision.CurrentBlockQuoteAccepted {
		t.Fatalf("unexpected disabled quote decision: %+v", decision)
	}
	wantLevel := LevelForEffectiveEnergy(profile.EffectiveEnergy)
	if decision.CandidateLevel != wantLevel ||
		decision.DifficultyFactorBps != DifficultyFactorBpsForLevel(wantLevel) {
		t.Fatalf("unexpected disabled quote level/factor: %+v", decision)
	}

	profile.EffectiveEnergy.SetUint64(1)
	if decision.CandidateEnergy.Cmp(big.NewInt(21_000_000)) != 0 {
		t.Fatalf("quote decision aliased profile energy: %s", decision.CandidateEnergy)
	}
}

func TestFormalQuotePolicyV1FailsClosed(t *testing.T) {
	decision, err := ResolveQuotePolicy(QuotePolicyVersionV1, QuotePolicyContext{
		Profile: testQuotePolicyProfile(),
	})
	if err == nil || decision != nil ||
		!strings.Contains(err.Error(), "unsupported usdb quote policy version 1") {
		t.Fatalf("formal quote v1 was accepted before implementation: decision=%v err=%v", decision, err)
	}
}

func TestQuotePolicyRejectsInconsistentEffectiveEnergy(t *testing.T) {
	profile := testQuotePolicyProfile()
	profile.EffectiveEnergy = big.NewInt(20_999_999)
	decision, err := ResolveQuotePolicy(QuotePolicyVersionDisabled, QuotePolicyContext{
		Profile: profile,
	})
	if err == nil || decision != nil ||
		!strings.Contains(err.Error(), "effective energy mismatch") {
		t.Fatalf("inconsistent profile was accepted: decision=%v err=%v", decision, err)
	}
}

func testQuotePolicyProfile() *ResolvedConsensusProfile {
	return &ResolvedConsensusProfile{
		RawEnergy:          big.NewInt(1_000_000),
		CollabContribution: big.NewInt(20_000_000),
		EffectiveEnergy:    big.NewInt(21_000_000),
	}
}
