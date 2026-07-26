//go:build usdb_economic_conformance_v2 && !usdb_economic_conformance_v3
// +build usdb_economic_conformance_v2,!usdb_economic_conformance_v3

package usdb

import "math/big"

// SupportsActivationConformanceQuotePolicy limits fake-v2 binaries to v2.
func SupportsActivationConformanceQuotePolicy(version uint16) bool {
	return version == QuotePolicyVersionActivationConformanceV2
}

// SupportsActivationConformanceAuxPoolPolicy limits fake-v2 binaries to v2.
func SupportsActivationConformanceAuxPoolPolicy(version uint16) bool {
	return version == AuxPoolPolicyVersionActivationConformanceV2
}

// ResolveActivationConformanceQuotePolicy dispatches fake v2 only.
func ResolveActivationConformanceQuotePolicy(version uint16, profile *ResolvedConsensusProfile) (*ActivationConformanceQuoteResult, bool, error) {
	if !SupportsActivationConformanceQuotePolicy(version) {
		return nil, false, nil
	}
	result, err := resolveActivationConformanceQuotePolicy(version, profile)
	return result, true, err
}

// ResolveActivationConformanceAuxPoolPolicy dispatches fake v2 only.
func ResolveActivationConformanceAuxPoolPolicy(version uint16, emission *big.Int) (*ActivationConformanceAuxReward, bool, error) {
	if !SupportsActivationConformanceAuxPoolPolicy(version) {
		return nil, false, nil
	}
	result, err := resolveActivationConformanceAuxPoolPolicy(version, emission)
	return result, true, err
}
