//go:build !usdb_economic_conformance_v2 && !usdb_economic_conformance_v3
// +build !usdb_economic_conformance_v2,!usdb_economic_conformance_v3

package usdb

import "math/big"

// SupportsActivationConformanceAuxPoolPolicy always rejects reserved policies
// in production builds.
func SupportsActivationConformanceAuxPoolPolicy(version uint16) bool {
	return false
}

func resolveActivationConformanceQuotePolicy(version uint16, context *QuotePolicyContext) (*QuotePolicyDecision, bool, error) {
	return nil, false, nil
}

// ResolveActivationConformanceAuxPoolPolicy leaves fake reward behavior absent
// from production builds.
func ResolveActivationConformanceAuxPoolPolicy(version uint16, emission *big.Int) (*ActivationConformanceAuxReward, bool, error) {
	return nil, false, nil
}
