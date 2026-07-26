//go:build !usdb_activation_conformance
// +build !usdb_activation_conformance

package ethash

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

func TestApplyUSDBDifficultyPolicyRejectsActivationConformanceVersionByDefault(t *testing.T) {
	policy := &params.USDBConsensusVersions{
		DifficultyPolicyVersion: usdb.DifficultyPolicyVersionActivationConformance,
	}
	decision := newTestUSDBQuoteDecision(usdb.QuotePolicyVersionDisabled, usdb.BasisPointDenominator)
	if _, err := applyUSDBDifficultyPolicy(policy, big.NewInt(100), decision); err == nil {
		t.Fatal("test-only activation policy unexpectedly accepted by default build")
	}
}

func TestDefaultBinaryStopsAtActivationConformanceBoundary(t *testing.T) {
	config := newActivationConformanceTestChainConfig(3)
	profile := newTestUSDBDifficultyProfile(7_154_210)
	engine := &Ethash{
		config:              Config{Log: log.Root()},
		usdbProfileResolver: &stubProfileResolver{resolved: profile},
	}
	parent := &types.Header{
		Number:     big.NewInt(1),
		Time:       1_001,
		Difficulty: big.NewInt(131_072),
		GasLimit:   30_000_000,
	}
	chain := &stubChainHeaderReader{config: config, header: parent}

	before := newActivationConformanceTestHeader(t, parent, 2, usdb.DifficultyPolicyVersionV1, 1_002)
	if err := engine.Prepare(chain, before); err != nil {
		t.Fatalf("default binary failed before activation: %v", err)
	}
	if err := engine.verifyHeader(chain, before, parent, false, false, 2_000); err != nil {
		t.Fatalf("default binary rejected pre-activation block: %v", err)
	}

	chain.header = before
	atBoundary := newActivationConformanceTestHeader(
		t,
		before,
		3,
		usdb.DifficultyPolicyVersionActivationConformance,
		1_003,
	)
	if err := engine.Prepare(chain, atBoundary); err == nil || !strings.Contains(err.Error(), "unsupported usdb difficulty policy version 65535") {
		t.Fatalf("default miner did not fail closed at activation: %v", err)
	}
	if err := engine.verifyHeader(chain, atBoundary, before, false, false, 2_000); err == nil || !strings.Contains(err.Error(), "unsupported usdb difficulty policy version 65535") {
		t.Fatalf("default validator did not fail closed at activation: %v", err)
	}
}
