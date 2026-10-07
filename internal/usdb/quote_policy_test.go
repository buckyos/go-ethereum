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
		RawEnergy:           big.NewInt(1_000_000),
		CollabContribution:  big.NewInt(20_000_000),
		EffectiveEnergy:     big.NewInt(21_000_000),
		Level:               LevelForEffectiveEnergy(big.NewInt(21_000_000)),
		DifficultyFactorBps: DifficultyFactorBpsForLevel(LevelForEffectiveEnergy(big.NewInt(21_000_000))),
	}
}

func TestQuotePolicyDisabledPreservesVerifiedRulesAndRejectsInvalidBounds(t *testing.T) {
	profile := testQuotePolicyProfile()
	// A future verified BTC rule may assign another level to the same energy.
	profile.Level, profile.DifficultyFactorBps = 50, 5000
	decision, err := ResolveQuotePolicy(QuotePolicyVersionDisabled, QuotePolicyContext{Profile: profile})
	if err != nil || decision.CandidateLevel != 50 || decision.DifficultyFactorBps != 5000 {
		t.Fatalf("discarded verified level: decision=%+v err=%v", decision, err)
	}
	profile.CollabContribution.SetUint64(0)
	if decision.CollaborationEnergy.Cmp(big.NewInt(20_000_000)) != 0 {
		t.Fatal("quote decision aliases collaboration energy")
	}
	for _, values := range []struct{ level, factor uint64 }{{51, 5000}, {0, 4999}, {0, 10001}} {
		profile = testQuotePolicyProfile()
		profile.Level, profile.DifficultyFactorBps = uint8(values.level), values.factor
		if decision, err := ResolveQuotePolicy(QuotePolicyVersionDisabled, QuotePolicyContext{Profile: profile}); err == nil || decision != nil {
			t.Fatalf("accepted invalid nominal bounds: %+v", values)
		}
	}
}
