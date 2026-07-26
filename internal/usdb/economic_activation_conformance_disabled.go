//go:build !usdb_economic_conformance_v2 && !usdb_economic_conformance_v3
// +build !usdb_economic_conformance_v2,!usdb_economic_conformance_v3

package usdb

import "math/big"

// SupportsActivationConformanceQuotePolicy always rejects reserved policies in
// production builds.
func SupportsActivationConformanceQuotePolicy(version uint16) bool {
	return false
}

// SupportsActivationConformanceAuxPoolPolicy always rejects reserved policies
// in production builds.
func SupportsActivationConformanceAuxPoolPolicy(version uint16) bool {
	return false
}

// ResolveActivationConformanceQuotePolicy leaves fake quote behavior absent
// from production builds.
func ResolveActivationConformanceQuotePolicy(version uint16, profile *ResolvedConsensusProfile) (*ActivationConformanceQuoteResult, bool, error) {
	return nil, false, nil
}

// ResolveActivationConformanceAuxPoolPolicy leaves fake reward behavior absent
// from production builds.
func ResolveActivationConformanceAuxPoolPolicy(version uint16, emission *big.Int) (*ActivationConformanceAuxReward, bool, error) {
	return nil, false, nil
}
