package usdb

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

const (
	// QuotePolicyVersionActivationConformanceV2 models no accepted quote.
	QuotePolicyVersionActivationConformanceV2 uint16 = 0xfffe
	// QuotePolicyVersionActivationConformanceV3 models a current-block implicit
	// FixedPriceHeartbeat. Both IDs are build-tagged and have no production
	// policy meaning.
	QuotePolicyVersionActivationConformanceV3 uint16 = 0xffff

	// AuxPoolPolicyVersionActivationConformanceV2 and V3 are reserved for
	// build-tagged activation tests. They have no production policy meaning.
	AuxPoolPolicyVersionActivationConformanceV2 uint16 = 0xfffe
	AuxPoolPolicyVersionActivationConformanceV3 uint16 = 0xffff

	// ActivationConformanceAuxPoolBpsV2 and V3 make test-only reward dispatch
	// observable without freezing a future UIP-0015 split.
	ActivationConformanceAuxPoolBpsV2 uint64 = 1_000
	ActivationConformanceAuxPoolBpsV3 uint64 = 2_000

	// ActivationConformanceAuxPoolRecipientV2Hex and V3Hex are test-only
	// sentinel recipients. They must not be used by a production chain.
	ActivationConformanceAuxPoolRecipientV2Hex = "0x000000000000000000000000000000000000fa02"
	ActivationConformanceAuxPoolRecipientV3Hex = "0x000000000000000000000000000000000000fa03"
)

// ActivationConformanceAuxReward contains a deterministic test-only split.
type ActivationConformanceAuxReward struct {
	MinerReward  *big.Int
	AuxReward    *big.Int
	AuxRecipient common.Address
}
