//go:build usdb_activation_conformance
// +build usdb_activation_conformance

package ethash

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

func TestApplyUSDBDifficultyPolicyDispatchesActivationConformanceVersion(t *testing.T) {
	base := big.NewInt(131_072)
	profile := &usdb.ResolvedConsensusProfile{DifficultyFactorBps: 9_500}
	got, err := applyUSDBDifficultyPolicy(
		&params.USDBConsensusVersions{DifficultyPolicyVersion: usdb.DifficultyPolicyVersionActivationConformance},
		base,
		profile,
	)
	if err != nil {
		t.Fatalf("test-only policy failed: %v", err)
	}
	v1, err := usdb.RealDifficultyV1(base, profile.DifficultyFactorBps)
	if err != nil {
		t.Fatalf("v1 policy failed: %v", err)
	}
	want := new(big.Int).Add(v1, big.NewInt(1))
	if got.Cmp(want) != 0 {
		t.Fatalf("unexpected test-only difficulty: have %v want %v", got, want)
	}
}

func TestActivationConformanceUpgradeBoundaryRestartAndReorg(t *testing.T) {
	const activationBlock = uint64(3)
	config := newActivationConformanceTestChainConfig(activationBlock)
	profile := &usdb.ResolvedConsensusProfile{DifficultyFactorBps: 9_500}
	resolver := &stubProfileResolver{resolved: profile}
	engine := &Ethash{config: Config{Log: log.Root()}, usdbProfileResolver: resolver}
	genesis := &types.Header{
		Number:     big.NewInt(0),
		Time:       1_000,
		Difficulty: big.NewInt(131_072),
		GasLimit:   30_000_000,
	}

	chain := &stubChainHeaderReader{config: config, header: genesis}
	canonical := []*types.Header{genesis}
	for number := uint64(1); number <= 5; number++ {
		parent := canonical[len(canonical)-1]
		version := usdb.DifficultyPolicyVersionV1
		wantRegistry := usdb.BTCRegtestActivationRegistryIDV1
		if number >= activationBlock {
			version = usdb.DifficultyPolicyVersionActivationConformance
			wantRegistry = usdb.BTCRegtestActivationRegistryIDRevision2
		}
		header := newActivationConformanceTestHeader(t, parent, number, version, 1_000+number)
		base := CalcDifficulty(config, header.Time, parent)
		want, err := usdb.RealDifficultyV1(base, profile.DifficultyFactorBps)
		if err != nil {
			t.Fatalf("block %d v1 formula failed: %v", number, err)
		}
		if version == usdb.DifficultyPolicyVersionActivationConformance {
			want = new(big.Int).Add(want, big.NewInt(1))
		}
		if err := engine.Prepare(chain, header); err != nil {
			t.Fatalf("block %d prepare failed: %v", number, err)
		}
		if header.Difficulty.Cmp(want) != 0 {
			t.Fatalf("block %d difficulty mismatch: have %v want %v", number, header.Difficulty, want)
		}
		if resolver.lastRegistry != wantRegistry {
			t.Fatalf("block %d used registry %s, want %s", number, resolver.lastRegistry, wantRegistry)
		}
		if err := engine.verifyHeader(chain, header, parent, false, false, 2_000); err != nil {
			t.Fatalf("block %d verification failed: %v", number, err)
		}
		canonical = append(canonical, header)
		chain.header = header
	}

	// A fresh engine must replay the already-mined post-activation header using
	// only chain config and committed selector data.
	restartedResolver := &stubProfileResolver{resolved: profile}
	restarted := &Ethash{config: Config{Log: log.Root()}, usdbProfileResolver: restartedResolver}
	if err := restarted.verifyHeader(chain, canonical[4], canonical[3], false, false, 2_000); err != nil {
		t.Fatalf("restarted validator failed historical replay: %v", err)
	}
	if restartedResolver.lastRegistry != usdb.BTCRegtestActivationRegistryIDRevision2 {
		t.Fatalf("restart replay used registry %s", restartedResolver.lastRegistry)
	}

	// Re-enter the activation from an alternative block H-1 and prove the same
	// H/H+1 rules are selected on the replacement branch.
	branchParent := canonical[activationBlock-1]
	branchChain := &stubChainHeaderReader{config: config, header: branchParent}
	branchEngine := &Ethash{
		config:              Config{Log: log.Root()},
		usdbProfileResolver: &stubProfileResolver{resolved: profile},
	}
	branchAt := newActivationConformanceTestHeader(
		t,
		branchParent,
		activationBlock,
		usdb.DifficultyPolicyVersionActivationConformance,
		branchParent.Time+2,
	)
	if err := branchEngine.Prepare(branchChain, branchAt); err != nil {
		t.Fatalf("replacement activation block prepare failed: %v", err)
	}
	if err := branchEngine.verifyHeader(branchChain, branchAt, branchParent, false, false, 2_000); err != nil {
		t.Fatalf("replacement activation block rejected: %v", err)
	}
	branchChain.header = branchAt
	branchAfter := newActivationConformanceTestHeader(
		t,
		branchAt,
		activationBlock+1,
		usdb.DifficultyPolicyVersionActivationConformance,
		branchAt.Time+1,
	)
	if err := branchEngine.Prepare(branchChain, branchAfter); err != nil {
		t.Fatalf("replacement post-activation block prepare failed: %v", err)
	}
	if err := branchEngine.verifyHeader(branchChain, branchAfter, branchAt, false, false, 2_000); err != nil {
		t.Fatalf("replacement post-activation block rejected: %v", err)
	}
}
