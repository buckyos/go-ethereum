//go:build usdb_economic_conformance_v3
// +build usdb_economic_conformance_v3

package usdb

import "math/big"

// SupportsActivationConformanceQuotePolicy allows fake-v3 binaries to replay
// v2 history and activate v3.
func SupportsActivationConformanceQuotePolicy(version uint16) bool {
	return version == QuotePolicyVersionActivationConformanceV2 ||
		version == QuotePolicyVersionActivationConformanceV3
}

// SupportsActivationConformanceAuxPoolPolicy allows fake-v3 binaries to replay
// v2 history and activate v3.
func SupportsActivationConformanceAuxPoolPolicy(version uint16) bool {
	return version == AuxPoolPolicyVersionActivationConformanceV2 ||
		version == AuxPoolPolicyVersionActivationConformanceV3
}

// ResolveActivationConformanceQuotePolicy dispatches fake v2 and v3.
func ResolveActivationConformanceQuotePolicy(version uint16, profile *ResolvedConsensusProfile) (*ActivationConformanceQuoteResult, bool, error) {
	if !SupportsActivationConformanceQuotePolicy(version) {
		return nil, false, nil
	}
	result, err := resolveActivationConformanceQuotePolicy(version, profile)
	return result, true, err
}

// ResolveActivationConformanceAuxPoolPolicy dispatches fake v2 and v3.
func ResolveActivationConformanceAuxPoolPolicy(version uint16, emission *big.Int) (*ActivationConformanceAuxReward, bool, error) {
	if !SupportsActivationConformanceAuxPoolPolicy(version) {
		return nil, false, nil
	}
	result, err := resolveActivationConformanceAuxPoolPolicy(version, emission)
	return result, true, err
}
