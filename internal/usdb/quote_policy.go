package usdb

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

const (
	// QuotePolicyVersionDisabled keeps UIP-0014 quote handling fully disabled.
	QuotePolicyVersionDisabled uint16 = 0
	// QuotePolicyVersionV1 is reserved for the first formal, evidence-backed
	// UIP-0014 quote policy. It is intentionally not implemented yet.
	QuotePolicyVersionV1 uint16 = 1
)

// QuoteBlockContext contains consensus-visible current-block inputs. A future
// formal quote policy can extend Evidence without changing the decision wiring.
type QuoteBlockContext struct {
	Number             uint64
	RewardRecipient    common.Address
	PricePolicyVersion uint32
	Evidence           []byte
}

// QuotePolicyContext contains the selector-bound profile and current-block
// inputs used by both miners and validators.
type QuotePolicyContext struct {
	Profile *ResolvedConsensusProfile
	Block   QuoteBlockContext
}

// QuotePolicyDecision is the complete quote-derived input shared by difficulty,
// collaboration efficiency, and reward state transitions.
type QuotePolicyDecision struct {
	PolicyVersion             uint16
	CandidateEnergy           *big.Int
	CandidateLevel            uint8
	DifficultyFactorBps       uint64
	CollaborationEnergy       *big.Int
	CurrentBlockQuoteAccepted bool
}

// ResolveQuotePolicy deterministically dispatches one activated UIP-0014
// policy. Version zero uses the nominal UIP-0004 profile, while formal v1 and
// every unknown non-zero version fail closed until implemented.
func ResolveQuotePolicy(version uint16, context QuotePolicyContext) (*QuotePolicyDecision, error) {
	if err := validateQuotePolicyContext(&context); err != nil {
		return nil, err
	}
	switch version {
	case QuotePolicyVersionDisabled:
		return newQuotePolicyDecision(
			version,
			context.Profile.EffectiveEnergy,
			context.Profile.CollabContribution,
			false,
		)
	case QuotePolicyVersionV1:
		return nil, fmt.Errorf("unsupported usdb quote policy version %d", version)
	default:
		decision, handled, err := resolveActivationConformanceQuotePolicy(version, &context)
		if err != nil {
			return nil, err
		}
		if !handled {
			return nil, fmt.Errorf("unsupported usdb quote policy version %d", version)
		}
		if decision == nil || decision.PolicyVersion != version {
			return nil, errors.New("invalid activation conformance quote decision")
		}
		return decision, nil
	}
}

func validateQuotePolicyContext(context *QuotePolicyContext) error {
	if context == nil || context.Profile == nil {
		return errors.New("usdb quote policy profile is nil")
	}
	profile := context.Profile
	if err := validateQuoteEnergy("raw energy", profile.RawEnergy); err != nil {
		return err
	}
	if err := validateQuoteEnergy("collaboration energy", profile.CollabContribution); err != nil {
		return err
	}
	if err := validateQuoteEnergy("effective energy", profile.EffectiveEnergy); err != nil {
		return err
	}
	expectedEffective := saturatingAddEnergy(profile.RawEnergy, profile.CollabContribution)
	if profile.EffectiveEnergy.Cmp(expectedEffective) != 0 {
		return errors.New("usdb quote policy effective energy mismatch")
	}
	return nil
}

func newQuotePolicyDecision(
	version uint16,
	candidateEnergy *big.Int,
	collaborationEnergy *big.Int,
	currentBlockQuoteAccepted bool,
) (*QuotePolicyDecision, error) {
	if err := validateQuoteEnergy("candidate energy", candidateEnergy); err != nil {
		return nil, err
	}
	if err := validateQuoteEnergy("collaboration energy", collaborationEnergy); err != nil {
		return nil, err
	}
	candidate := new(big.Int).Set(candidateEnergy)
	level := LevelForEffectiveEnergy(candidate)
	return &QuotePolicyDecision{
		PolicyVersion:             version,
		CandidateEnergy:           candidate,
		CandidateLevel:            level,
		DifficultyFactorBps:       DifficultyFactorBpsForLevel(level),
		CollaborationEnergy:       new(big.Int).Set(collaborationEnergy),
		CurrentBlockQuoteAccepted: currentBlockQuoteAccepted,
	}, nil
}

func validateQuoteEnergy(name string, value *big.Int) error {
	if value == nil || value.Sign() < 0 || value.Cmp(maximumEnergyValue) > 0 {
		return fmt.Errorf("usdb quote policy %s is outside uint128", name)
	}
	return nil
}
