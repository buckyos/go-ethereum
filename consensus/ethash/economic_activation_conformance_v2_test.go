//go:build usdb_economic_conformance_v2 && !usdb_economic_conformance_v3
// +build usdb_economic_conformance_v2,!usdb_economic_conformance_v3

package ethash

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/params"
)

func TestEconomicActivationConformanceV2DispatchAndV3Boundary(t *testing.T) {
	profile := &usdb.ResolvedConsensusProfile{
		RawEnergy:           big.NewInt(1_000_000),
		CollabContribution:  big.NewInt(20_000_000),
		EffectiveEnergy:     big.NewInt(21_000_000),
		DifficultyFactorBps: usdb.DifficultyFactorBpsForLevel(usdb.LevelForEffectiveEnergy(big.NewInt(21_000_000))),
	}
	policy := &params.USDBConsensusVersions{
		DifficultyPolicyVersion: usdb.DifficultyPolicyVersionV1,
		QuotePolicyVersion:      usdb.QuotePolicyVersionActivationConformanceV2,
		PricePolicyVersion:      usdb.PricePolicyVersionV1,
	}
	header := &types.Header{Number: big.NewInt(1)}
	decision, err := resolveUSDBQuotePolicy(policy, header, profile)
	if err != nil {
		t.Fatalf("fake v2 quote resolution failed: %v", err)
	}
	got, err := applyUSDBDifficultyPolicy(policy, big.NewInt(100_000), decision)
	if err != nil {
		t.Fatalf("fake v2 quote difficulty failed: %v", err)
	}
	rawFactor := usdb.DifficultyFactorBpsForLevel(usdb.LevelForEffectiveEnergy(profile.RawEnergy))
	want, err := usdb.RealDifficultyV1(big.NewInt(100_000), rawFactor)
	if err != nil {
		t.Fatalf("failed to calculate expected fake v2 difficulty: %v", err)
	}
	if got.Cmp(want) != 0 {
		t.Fatalf("fake v2 did not use raw energy: have %s want %s", got, want)
	}

	policy.QuotePolicyVersion = usdb.QuotePolicyVersionActivationConformanceV3
	if _, err := resolveUSDBQuotePolicy(policy, header, profile); err == nil ||
		!strings.Contains(err.Error(), "unsupported usdb quote policy version 65535") {
		t.Fatalf("fake v2 build crossed fake v3 boundary: %v", err)
	}
}
