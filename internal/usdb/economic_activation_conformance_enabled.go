//go:build usdb_economic_conformance_v2 || usdb_economic_conformance_v3
// +build usdb_economic_conformance_v2 usdb_economic_conformance_v3

package usdb

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

func resolveActivationConformanceQuotePolicy(version uint16, profile *ResolvedConsensusProfile) (*ActivationConformanceQuoteResult, error) {
	if profile == nil {
		return nil, errors.New("activation conformance quote profile is nil")
	}
	if err := validateActivationConformanceEnergy("raw energy", profile.RawEnergy); err != nil {
		return nil, err
	}
	if err := validateActivationConformanceEnergy("collaboration energy", profile.CollabContribution); err != nil {
		return nil, err
	}
	if err := validateActivationConformanceEnergy("effective energy", profile.EffectiveEnergy); err != nil {
		return nil, err
	}
	expectedEffective := saturatingAddEnergy(profile.RawEnergy, profile.CollabContribution)
	if profile.EffectiveEnergy.Cmp(expectedEffective) != 0 {
		return nil, errors.New("activation conformance effective energy mismatch")
	}

	switch version {
	case QuotePolicyVersionActivationConformanceV2:
		level := LevelForEffectiveEnergy(profile.RawEnergy)
		return &ActivationConformanceQuoteResult{
			DifficultyFactorBps: DifficultyFactorBpsForLevel(level),
			CollaborationEnergy: new(big.Int),
		}, nil
	case QuotePolicyVersionActivationConformanceV3:
		level := LevelForEffectiveEnergy(profile.EffectiveEnergy)
		return &ActivationConformanceQuoteResult{
			DifficultyFactorBps: DifficultyFactorBpsForLevel(level),
			CollaborationEnergy: new(big.Int).Set(profile.CollabContribution),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported activation conformance quote policy %d", version)
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

func validateActivationConformanceEnergy(name string, value *big.Int) error {
	if value == nil || value.Sign() < 0 || value.Cmp(maximumEnergyValue) > 0 {
		return fmt.Errorf("activation conformance %s is outside uint128", name)
	}
	return nil
}
