//go:build usdb_activation_conformance
// +build usdb_activation_conformance

package usdb

import (
	"math/big"
	"testing"
)

func TestActivationConformanceDifficultyPolicyIsDistinctFromV1(t *testing.T) {
	base := big.NewInt(131_072)
	factor := uint64(9_500)
	v1, err := RealDifficultyV1(base, factor)
	if err != nil {
		t.Fatalf("failed to calculate v1 difficulty: %v", err)
	}
	got, handled, err := ApplyActivationConformanceDifficultyPolicy(
		DifficultyPolicyVersionActivationConformance,
		base,
		factor,
	)
	if err != nil {
		t.Fatalf("test-only policy failed: %v", err)
	}
	if !handled {
		t.Fatal("test-only policy was not handled in tagged build")
	}
	want := new(big.Int).Add(v1, big.NewInt(1))
	if got.Cmp(want) != 0 {
		t.Fatalf("unexpected test-only difficulty: have %v want %v", got, want)
	}
}

func TestActivationConformanceDifficultyPolicyDoesNotClaimUnknownVersion(t *testing.T) {
	if got, handled, err := ApplyActivationConformanceDifficultyPolicy(2, big.NewInt(1), BasisPointDenominator); err != nil || handled || got != nil {
		t.Fatalf("unknown policy was claimed: difficulty=%v handled=%v err=%v", got, handled, err)
	}
}
