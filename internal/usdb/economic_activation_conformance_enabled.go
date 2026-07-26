//go:build usdb_economic_conformance_v2 || usdb_economic_conformance_v3
// +build usdb_economic_conformance_v2 usdb_economic_conformance_v3

package usdb

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

func resolveActivationConformanceQuotePolicy(version uint16, context *QuotePolicyContext) (*QuotePolicyDecision, bool, error) {
	if !supportsActivationConformanceQuotePolicy(version) {
		return nil, false, nil
	}
	switch version {
	case QuotePolicyVersionActivationConformanceV2:
		decision, err := newQuotePolicyDecision(
			version,
			context.Profile.RawEnergy,
			new(big.Int),
			false,
		)
		return decision, true, err
	case QuotePolicyVersionActivationConformanceV3:
		if context.Block.PricePolicyVersion != PricePolicyVersionV1 {
			return nil, true, errors.New("activation conformance FixedPriceHeartbeat requires price policy v1")
		}
		if len(context.Block.Evidence) != 0 {
			return nil, true, errors.New("activation conformance FixedPriceHeartbeat must be implicit")
		}
		if context.Block.RewardRecipient != context.Profile.RewardRecipient {
			return nil, true, errors.New("activation conformance FixedPriceHeartbeat reward recipient mismatch")
		}
		decision, err := newQuotePolicyDecision(
			version,
			context.Profile.EffectiveEnergy,
			context.Profile.CollabContribution,
			true,
		)
		return decision, true, err
	default:
		return nil, false, nil
	}
}

func resolveActivationConformanceAuxPoolPolicy(version uint16, emission *big.Int) (*ActivationConformanceAuxReward, error) {
	if emission == nil || emission.Sign() < 0 {
		return nil, errors.New("activation conformance emission is nil or negative")
	}
	var (
		auxBps       uint64
		auxRecipient common.Address
	)
	switch version {
	case AuxPoolPolicyVersionActivationConformanceV2:
		auxBps = ActivationConformanceAuxPoolBpsV2
		auxRecipient = common.HexToAddress(ActivationConformanceAuxPoolRecipientV2Hex)
	case AuxPoolPolicyVersionActivationConformanceV3:
		auxBps = ActivationConformanceAuxPoolBpsV3
		auxRecipient = common.HexToAddress(ActivationConformanceAuxPoolRecipientV3Hex)
	default:
		return nil, fmt.Errorf("unsupported activation conformance auxiliary policy %d", version)
	}
	auxReward := new(big.Int).Mul(emission, new(big.Int).SetUint64(auxBps))
	auxReward.Div(auxReward, new(big.Int).SetUint64(BasisPointDenominator))
	minerReward := new(big.Int).Sub(new(big.Int).Set(emission), auxReward)
	return &ActivationConformanceAuxReward{
		MinerReward:  minerReward,
		AuxReward:    auxReward,
		AuxRecipient: auxRecipient,
	}, nil
}
