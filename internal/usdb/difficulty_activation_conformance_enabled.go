//go:build usdb_activation_conformance
// +build usdb_activation_conformance

package usdb

import "math/big"

// ApplyActivationConformanceDifficultyPolicy implements a deliberately simple,
// test-only second policy. The distinct result proves activation dispatch
// without assigning protocol meaning to an as-yet undefined production v2.
func ApplyActivationConformanceDifficultyPolicy(version uint16, baseDifficulty *big.Int, difficultyFactorBps uint64) (*big.Int, bool, error) {
	if version != DifficultyPolicyVersionActivationConformance {
		return nil, false, nil
	}
	difficulty, err := RealDifficultyV1(baseDifficulty, difficultyFactorBps)
	if err != nil {
		return nil, true, err
	}
	return new(big.Int).Add(difficulty, big.NewInt(1)), true, nil
}
