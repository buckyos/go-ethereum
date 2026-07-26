//go:build !usdb_economic_conformance_v2 && !usdb_economic_conformance_v3
// +build !usdb_economic_conformance_v2,!usdb_economic_conformance_v3

package usdb

import (
	"math/big"
	"strings"
	"testing"
)

func TestEconomicActivationConformancePoliciesAreDisabledByDefault(t *testing.T) {
	profile := &ResolvedConsensusProfile{
		RawEnergy:          big.NewInt(1),
		CollabContribution: big.NewInt(2),
		EffectiveEnergy:    big.NewInt(3),
	}
	for _, version := range []uint16{
		QuotePolicyVersionActivationConformanceV2,
		QuotePolicyVersionActivationConformanceV3,
	} {
		if result, err := ResolveQuotePolicy(version, QuotePolicyContext{Profile: profile}); err == nil ||
			result != nil ||
			!strings.Contains(err.Error(), "unsupported usdb quote policy version") {
			t.Fatalf("default build claimed quote version %d: result=%v err=%v", version, result, err)
		}
		if result, handled, err := ResolveActivationConformanceAuxPoolPolicy(version, big.NewInt(100)); err != nil || handled || result != nil {
			t.Fatalf("default build claimed aux version %d: result=%v handled=%t err=%v", version, result, handled, err)
		}
	}
}
