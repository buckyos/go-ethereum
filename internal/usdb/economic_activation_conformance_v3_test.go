//go:build usdb_economic_conformance_v3
// +build usdb_economic_conformance_v3

package usdb

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestEconomicActivationConformanceV3IncludesHistoricalV2(t *testing.T) {
	profile := &ResolvedConsensusProfile{
		RawEnergy:          big.NewInt(1_000_000),
		CollabContribution: big.NewInt(20_000_000),
		EffectiveEnergy:    big.NewInt(21_000_000),
	}
	for _, test := range []struct {
		version    uint16
		energy     *big.Int
		wantCE     *big.Int
		auxVersion uint16
		auxBps     uint64
		recipient  common.Address
	}{
		{
			version:    QuotePolicyVersionActivationConformanceV2,
			energy:     profile.RawEnergy,
			wantCE:     new(big.Int),
			auxVersion: AuxPoolPolicyVersionActivationConformanceV2,
			auxBps:     ActivationConformanceAuxPoolBpsV2,
			recipient:  common.HexToAddress(ActivationConformanceAuxPoolRecipientV2Hex),
		},
		{
			version:    QuotePolicyVersionActivationConformanceV3,
			energy:     profile.EffectiveEnergy,
			wantCE:     profile.CollabContribution,
			auxVersion: AuxPoolPolicyVersionActivationConformanceV3,
			auxBps:     ActivationConformanceAuxPoolBpsV3,
			recipient:  common.HexToAddress(ActivationConformanceAuxPoolRecipientV3Hex),
		},
	} {
		quote, handled, err := ResolveActivationConformanceQuotePolicy(test.version, profile)
		if err != nil || !handled {
			t.Fatalf("quote version %d was not handled: result=%v handled=%t err=%v", test.version, quote, handled, err)
		}
		wantFactor := DifficultyFactorBpsForLevel(LevelForEffectiveEnergy(test.energy))
		if quote.DifficultyFactorBps != wantFactor || quote.CollaborationEnergy.Cmp(test.wantCE) != 0 {
			t.Fatalf("unexpected quote version %d result: %+v", test.version, quote)
		}

		emission := big.NewInt(10_003)
		split, handled, err := ResolveActivationConformanceAuxPoolPolicy(test.auxVersion, emission)
		if err != nil || !handled {
			t.Fatalf("aux version %d was not handled: split=%v handled=%t err=%v", test.auxVersion, split, handled, err)
		}
		wantAux := new(big.Int).Mul(emission, new(big.Int).SetUint64(test.auxBps))
		wantAux.Div(wantAux, new(big.Int).SetUint64(BasisPointDenominator))
		if split.AuxReward.Cmp(wantAux) != 0 ||
			new(big.Int).Add(split.MinerReward, split.AuxReward).Cmp(emission) != 0 ||
			split.AuxRecipient != test.recipient {
			t.Fatalf("unexpected aux version %d split: %+v", test.auxVersion, split)
		}
	}
}
