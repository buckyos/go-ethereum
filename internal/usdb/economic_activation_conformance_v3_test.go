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
		RewardRecipient:    common.HexToAddress("0x1001"),
	}
	for _, test := range []struct {
		version    uint16
		energy     *big.Int
		wantCE     *big.Int
		accepted   bool
		auxVersion uint16
		auxBps     uint64
		recipient  common.Address
	}{
		{
			version:    QuotePolicyVersionActivationConformanceV2,
			energy:     profile.RawEnergy,
			wantCE:     new(big.Int),
			accepted:   false,
			auxVersion: AuxPoolPolicyVersionActivationConformanceV2,
			auxBps:     ActivationConformanceAuxPoolBpsV2,
			recipient:  common.HexToAddress(ActivationConformanceAuxPoolRecipientV2Hex),
		},
		{
			version:    QuotePolicyVersionActivationConformanceV3,
			energy:     profile.EffectiveEnergy,
			wantCE:     profile.CollabContribution,
			accepted:   true,
			auxVersion: AuxPoolPolicyVersionActivationConformanceV3,
			auxBps:     ActivationConformanceAuxPoolBpsV3,
			recipient:  common.HexToAddress(ActivationConformanceAuxPoolRecipientV3Hex),
		},
	} {
		quote, err := ResolveQuotePolicy(test.version, QuotePolicyContext{
			Profile: profile,
			Block: QuoteBlockContext{
				Number:             10,
				RewardRecipient:    profile.RewardRecipient,
				PricePolicyVersion: PricePolicyVersionV1,
			},
		})
		if err != nil {
			t.Fatalf("quote version %d was not handled: result=%v err=%v", test.version, quote, err)
		}
		wantFactor := DifficultyFactorBpsForLevel(LevelForEffectiveEnergy(test.energy))
		if quote.DifficultyFactorBps != wantFactor ||
			quote.CollaborationEnergy.Cmp(test.wantCE) != 0 ||
			quote.CurrentBlockQuoteAccepted != test.accepted {
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

func TestEconomicActivationConformanceV3RequiresImplicitFixedPriceHeartbeat(t *testing.T) {
	profile := testQuotePolicyProfile()
	profile.RewardRecipient = common.HexToAddress("0x1001")
	for _, test := range []struct {
		name  string
		block QuoteBlockContext
	}{
		{
			name: "wrong price policy",
			block: QuoteBlockContext{
				RewardRecipient: profile.RewardRecipient,
			},
		},
		{
			name: "wrong recipient",
			block: QuoteBlockContext{
				RewardRecipient:    common.HexToAddress("0x2001"),
				PricePolicyVersion: PricePolicyVersionV1,
			},
		},
		{
			name: "explicit evidence",
			block: QuoteBlockContext{
				RewardRecipient:    profile.RewardRecipient,
				PricePolicyVersion: PricePolicyVersionV1,
				Evidence:           []byte{1},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if decision, err := ResolveQuotePolicy(
				QuotePolicyVersionActivationConformanceV3,
				QuotePolicyContext{Profile: profile, Block: test.block},
			); err == nil || decision != nil {
				t.Fatalf("invalid implicit heartbeat was accepted: decision=%v err=%v", decision, err)
			}
		})
	}
}
