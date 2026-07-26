//go:build !usdb_economic_conformance_v2 && !usdb_economic_conformance_v3
// +build !usdb_economic_conformance_v2,!usdb_economic_conformance_v3

package ethash

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/params"
)

func TestDefaultBuildRejectsEconomicActivationConformancePolicies(t *testing.T) {
	profile := &usdb.ResolvedConsensusProfile{
		RawEnergy:           big.NewInt(1),
		CollabContribution:  big.NewInt(2),
		EffectiveEnergy:     big.NewInt(3),
		DifficultyFactorBps: usdb.BasisPointDenominator,
	}
	quotePolicy := &params.USDBConsensusVersions{
		DifficultyPolicyVersion: usdb.DifficultyPolicyVersionV1,
		QuotePolicyVersion:      usdb.QuotePolicyVersionActivationConformanceV2,
	}
	if _, err := applyUSDBDifficultyPolicy(quotePolicy, big.NewInt(100), profile); err == nil ||
		!strings.Contains(err.Error(), "unsupported usdb quote policy version 65534") {
		t.Fatalf("default build accepted fake quote policy: %v", err)
	}

	rewardPolicy := newTestUSDBRewardChainConfig().USDB.Activations[0].Versions
	rewardPolicy.AuxPoolPolicyVersion = usdb.AuxPoolPolicyVersionActivationConformanceV2
	if err := validateUSDBRewardPolicies(&rewardPolicy); err == nil ||
		!strings.Contains(err.Error(), "unsupported USDB auxiliary pool policy version 65534") {
		t.Fatalf("default build accepted fake auxiliary policy: %v", err)
	}
}
