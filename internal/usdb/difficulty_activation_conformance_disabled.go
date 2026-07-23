//go:build !usdb_activation_conformance
// +build !usdb_activation_conformance

package usdb

import "math/big"

// ApplyActivationConformanceDifficultyPolicy leaves the test-only policy
// unavailable in normal binaries.
func ApplyActivationConformanceDifficultyPolicy(version uint16, baseDifficulty *big.Int, difficultyFactorBps uint64) (*big.Int, bool, error) {
	return nil, false, nil
}
