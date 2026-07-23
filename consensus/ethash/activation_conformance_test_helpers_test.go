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
		USDB: &params.USDBConsensusConfig{Activations: []params.USDBConsensusActivation{
			{
				Block:                   0,
				BTCActivationRegistryID: usdb.BTCRegtestActivationRegistryIDV1,
				Versions: params.USDBConsensusVersions{
					PayloadVersion:          usdb.ProfileSelectorPayloadVersionV1,
					DifficultyPolicyVersion: usdb.DifficultyPolicyVersionV1,
				},
			},
			{
				Block:                   activationBlock,
				BTCActivationRegistryID: usdb.BTCRegtestActivationRegistryIDRevision2,
				Versions: params.USDBConsensusVersions{
					PayloadVersion:          usdb.ProfileSelectorPayloadVersionV1,
					DifficultyPolicyVersion: usdb.DifficultyPolicyVersionActivationConformance,
				},
			},
		}},
	}
}

func newActivationConformanceTestHeader(t *testing.T, parent *types.Header, number uint64, difficultyPolicyVersion uint16, timestamp uint64) *types.Header {
	t.Helper()
	return &types.Header{
		ParentHash: parent.Hash(),
		Number:     new(big.Int).SetUint64(number),
		Time:       timestamp,
		GasLimit:   parent.GasLimit,
		Extra:      newTestPayloadBytesForDifficultyVersion(t, difficultyPolicyVersion),
	}
}
