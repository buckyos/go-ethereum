package ethash

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/params"
)

func newActivationConformanceTestChainConfig(activationBlock uint64) *params.ChainConfig {
	return &params.ChainConfig{
		HomesteadBlock: big.NewInt(0),
		USDB: &params.USDBConsensusConfig{
			BTCNetworkID:         "btc-regtest",
			BTCIndexOriginHeight: 1,
			Activations: []params.USDBConsensusActivation{
				{
					Block:                   0,
					BTCActivationRegistryID: usdb.BTCRegtestActivationRegistryIDV1,
					BTCAnchorMaxAgeBlocks:   params.USDBDevelopmentBTCAnchorMaxAgeBlocks,
					Versions: params.USDBConsensusVersions{
						PayloadVersion:          usdb.ProfileSelectorPayloadVersionV1,
						BTCAnchorPolicyVersion:  usdb.BTCAnchorPolicyVersionV1,
						DifficultyPolicyVersion: usdb.DifficultyPolicyVersionV1,
					},
				},
				{
					Block:                   activationBlock,
					BTCActivationRegistryID: usdb.BTCRegtestActivationRegistryIDRevision2,
					BTCAnchorMaxAgeBlocks:   params.USDBDevelopmentBTCAnchorMaxAgeBlocks,
					Versions: params.USDBConsensusVersions{
						PayloadVersion:          usdb.ProfileSelectorPayloadVersionV1,
						BTCAnchorPolicyVersion:  usdb.BTCAnchorPolicyVersionV1,
						DifficultyPolicyVersion: usdb.DifficultyPolicyVersionActivationConformance,
					},
				},
			},
		},
	}
}

func newActivationConformanceTestHeader(t *testing.T, parent *types.Header, number uint64, difficultyPolicyVersion uint16, timestamp uint64) *types.Header {
	t.Helper()
	extra := newTestPayloadBytesForDifficultyVersion(t, difficultyPolicyVersion)
	if parent.Number.Sign() > 0 {
		var parentSelector, childSelector usdb.ProfileSelectorPayload
		if err := parentSelector.UnmarshalBinary(parent.Extra); err != nil {
			t.Fatalf("decode parent selector: %v", err)
		}
		if err := childSelector.UnmarshalBinary(extra); err != nil {
			t.Fatalf("decode child selector: %v", err)
		}
		childSelector.BTCAnchorAgeBlocks = parentSelector.BTCAnchorAgeBlocks + 1
		var err error
		extra, err = childSelector.MarshalBinary()
		if err != nil {
			t.Fatalf("encode child selector: %v", err)
		}
	}
	return &types.Header{
		ParentHash: parent.Hash(),
		Number:     new(big.Int).SetUint64(number),
		Time:       timestamp,
		GasLimit:   parent.GasLimit,
		Extra:      extra,
	}
}

func newTestUSDBDifficultyProfile(energy int64) *usdb.ResolvedConsensusProfile {
	value := big.NewInt(energy)
	level := usdb.LevelForEffectiveEnergy(value)
	return &usdb.ResolvedConsensusProfile{
		RawEnergy:           new(big.Int).Set(value),
		CollabContribution:  new(big.Int),
		EffectiveEnergy:     new(big.Int).Set(value),
		Level:               level,
		DifficultyFactorBps: usdb.DifficultyFactorBpsForLevel(level),
	}
}

func newTestUSDBQuoteDecision(version uint16, factor uint64) *usdb.QuotePolicyDecision {
	return &usdb.QuotePolicyDecision{
		PolicyVersion:       version,
		DifficultyFactorBps: factor,
	}
}
