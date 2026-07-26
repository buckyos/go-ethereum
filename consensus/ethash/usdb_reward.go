package ethash

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/usdbstate"
	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/params"
)

type usdbSlotWrite struct {
	slot  common.Hash
	value common.Hash
}

type usdbBalanceCredit struct {
	recipient common.Address
	amount    *big.Int
}

type usdbRewardTransition struct {
	credits []usdbBalanceCredit
	writes  []usdbSlotWrite
}

func validateUSDBRewardPolicies(policy *params.USDBConsensusVersions) error {
	if policy == nil {
		return errors.New("USDB reward policy is nil")
	}
	switch policy.RewardRuleVersion {
	case 0:
		if policy.CoinbaseEmissionPolicyVersion != 0 ||
			policy.FeeSplitPolicyVersion != 0 ||
			policy.CollaborationEfficiencyPolicyVersion != 0 ||
			policy.PricePolicyVersion != 0 ||
			policy.AuxPoolPolicyVersion != 0 {
			return fmt.Errorf(
				"invalid inactive USDB reward policy dependencies: coinbase=%d fee_split=%d collaboration=%d price=%d aux=%d",
				policy.CoinbaseEmissionPolicyVersion,
				policy.FeeSplitPolicyVersion,
				policy.CollaborationEfficiencyPolicyVersion,
				policy.PricePolicyVersion,
				policy.AuxPoolPolicyVersion,
			)
		}
		return nil
	case usdb.RewardRuleVersionV1:
	default:
		return fmt.Errorf("unsupported USDB reward rule version %d", policy.RewardRuleVersion)
	}
	if policy.CoinbaseEmissionPolicyVersion != usdb.CoinbaseEmissionPolicyVersionV1 {
		return fmt.Errorf(
			"unsupported USDB coinbase emission policy version %d",
			policy.CoinbaseEmissionPolicyVersion,
		)
	}
	if policy.FeeSplitPolicyVersion != 0 &&
		policy.FeeSplitPolicyVersion != usdb.FeeSplitPolicyVersionV1 {
		return fmt.Errorf(
			"unsupported USDB fee split policy version %d",
			policy.FeeSplitPolicyVersion,
		)
	}
	if policy.CollaborationEfficiencyPolicyVersion != 0 &&
		policy.CollaborationEfficiencyPolicyVersion != usdb.CollaborationEfficiencyPolicyVersionV1 {
		return fmt.Errorf(
			"unsupported USDB collaboration efficiency policy version %d",
			policy.CollaborationEfficiencyPolicyVersion,
		)
	}
	if policy.PricePolicyVersion != usdb.PricePolicyVersionV1 {
		return fmt.Errorf(
			"unsupported USDB price policy version %d",
			policy.PricePolicyVersion,
		)
	}
	if policy.AuxPoolPolicyVersion != 0 &&
		!usdb.SupportsActivationConformanceAuxPoolPolicy(policy.AuxPoolPolicyVersion) {
		return fmt.Errorf(
			"unsupported USDB auxiliary pool policy version %d",
			policy.AuxPoolPolicyVersion,
		)
	}
	return nil
}

func prepareUSDBRewardTransition(
	config *params.ChainConfig,
	activation *params.USDBConsensusActivation,
	statedb *state.StateDB,
	header *types.Header,
	profile *usdb.ResolvedConsensusProfile,
) (*usdbRewardTransition, error) {
	if config == nil || activation == nil || statedb == nil || header == nil || profile == nil {
		return nil, errors.New("incomplete USDB reward transition input")
	}
	if header.Number == nil || !header.Number.IsUint64() {
		return nil, consensus.ErrInvalidNumber
	}
	if header.Coinbase != profile.RewardRecipient {
		return nil, fmt.Errorf(
			"USDB reward recipient mismatch: header coinbase %s, profile recipient %s",
			header.Coinbase,
			profile.RewardRecipient,
		)
	}
	if err := usdbstate.ValidateSystemAccount(statedb); err != nil {
		return nil, fmt.Errorf("invalid USDB system state: %w", err)
	}
	if activation.Versions.FeeSplitPolicyVersion == usdb.FeeSplitPolicyVersionV1 {
		if _, err := usdbstate.ResolveDividendFeeGate(statedb, config, header.Number); err != nil {
			return nil, fmt.Errorf("validate USDB Dividend fee gate: %w", err)
		}
	}

	price, priceWrites, err := prepareFixedPriceTransition(
		config,
		activation,
		statedb,
		header.Number.Uint64(),
	)
	if err != nil {
		return nil, err
	}
	collaborationEnergy, quoteWrites, err := prepareQuotePolicyTransition(
		config,
		activation,
		statedb,
		header.Number.Uint64(),
		profile,
	)
	if err != nil {
		return nil, err
	}
	kBps, kWrites, err := prepareKTransition(
		activation.Versions.CollaborationEfficiencyPolicyVersion,
		statedb,
		collaborationEnergy,
	)
	if err != nil {
		return nil, err
	}
	issued, err := usdbstate.ReadUint256(statedb, usdbstate.IssuedUSDBAtomsSlot)
	if err != nil {
		return nil, fmt.Errorf("read issued USDB state: %w", err)
	}
	emission, err := usdb.CalculateCoinbaseEmissionV1(
		profile.TotalMinerBTCSats,
		price,
		issued,
		kBps,
	)
	if err != nil {
		return nil, fmt.Errorf("calculate USDB coinbase emission: %w", err)
	}
	issuedWrite, err := encodeUSDBSlotWrite(
		usdbstate.IssuedUSDBAtomsSlot,
		emission.IssuedUSDBAtomsAfter,
	)
	if err != nil {
		return nil, fmt.Errorf("encode issued USDB state: %w", err)
	}
	credits, err := prepareUSDBRewardCredits(
		activation.Versions.AuxPoolPolicyVersion,
		profile.RewardRecipient,
		emission.CoinbaseEmissionAtoms,
	)
	if err != nil {
		return nil, err
	}
	writes := make([]usdbSlotWrite, 0, 1+len(priceWrites)+len(quoteWrites)+len(kWrites))
	writes = append(writes, issuedWrite)
	writes = append(writes, priceWrites...)
	writes = append(writes, quoteWrites...)
	writes = append(writes, kWrites...)
	return &usdbRewardTransition{
		credits: credits,
		writes:  writes,
	}, nil
}

func applyUSDBRewardTransition(statedb *state.StateDB, transition *usdbRewardTransition) {
	for _, write := range transition.writes {
		statedb.SetState(usdbstate.SystemStateAddress, write.slot, write.value)
	}
	for _, credit := range transition.credits {
		statedb.AddBalance(credit.recipient, credit.amount)
	}
}

func prepareQuotePolicyTransition(
	config *params.ChainConfig,
	activation *params.USDBConsensusActivation,
	statedb *state.StateDB,
	blockNumber uint64,
	profile *usdb.ResolvedConsensusProfile,
) (*big.Int, []usdbSlotWrite, error) {
	if blockNumber == 0 {
		return nil, nil, errors.New("USDB quote transition cannot execute at genesis")
	}
	parentActivation, err := config.USDBActivationAt(blockNumber - 1)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve parent USDB quote activation: %w", err)
	}
	if parentActivation == nil {
		return nil, nil, errors.New("parent USDB state has no quote policy")
	}
	parentPolicy, err := usdbstate.ReadUint256(statedb, usdbstate.QuotePolicyVersionSlot)
	if err != nil {
		return nil, nil, fmt.Errorf("read parent USDB quote policy: %w", err)
	}
	expectedParentPolicy := new(big.Int).SetUint64(uint64(parentActivation.Versions.QuotePolicyVersion))
	if parentPolicy.Cmp(expectedParentPolicy) != 0 {
		return nil, nil, fmt.Errorf(
			"parent USDB quote policy mismatch: have %s want %s",
			parentPolicy,
			expectedParentPolicy,
		)
	}

	currentEnergy := profile.CollabContribution
	if activation.Versions.QuotePolicyVersion != 0 {
		quote, handled, err := usdb.ResolveActivationConformanceQuotePolicy(
			activation.Versions.QuotePolicyVersion,
			profile,
		)
		if err != nil {
			return nil, nil, err
		}
		if !handled {
			return nil, nil, fmt.Errorf(
				"unsupported usdb quote policy version %d",
				activation.Versions.QuotePolicyVersion,
			)
		}
		currentEnergy = quote.CollaborationEnergy
	}
	if currentEnergy == nil {
		return nil, nil, errors.New("USDB collaboration energy is nil")
	}
	policyWrite, err := encodeUSDBSlotWrite(
		usdbstate.QuotePolicyVersionSlot,
		new(big.Int).SetUint64(uint64(activation.Versions.QuotePolicyVersion)),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("encode USDB quote policy state: %w", err)
	}
	return new(big.Int).Set(currentEnergy), []usdbSlotWrite{policyWrite}, nil
}

func prepareUSDBRewardCredits(
	policyVersion uint16,
	minerRecipient common.Address,
	emission *big.Int,
) ([]usdbBalanceCredit, error) {
	if minerRecipient == (common.Address{}) {
		return nil, errors.New("USDB miner reward recipient is zero")
	}
	if emission == nil || emission.Sign() < 0 {
		return nil, errors.New("USDB CoinBase emission is nil or negative")
	}
	if policyVersion == 0 {
		return []usdbBalanceCredit{{
			recipient: minerRecipient,
			amount:    new(big.Int).Set(emission),
		}}, nil
	}
	split, handled, err := usdb.ResolveActivationConformanceAuxPoolPolicy(policyVersion, emission)
	if err != nil {
		return nil, err
	}
	if !handled {
		return nil, fmt.Errorf("unsupported USDB auxiliary pool policy version %d", policyVersion)
	}
	if split == nil ||
		split.MinerReward == nil ||
		split.AuxReward == nil ||
		split.MinerReward.Sign() < 0 ||
		split.AuxReward.Sign() < 0 ||
		split.AuxRecipient == (common.Address{}) ||
		split.AuxRecipient == minerRecipient {
		return nil, errors.New("invalid activation conformance auxiliary reward split")
	}
	total := new(big.Int).Add(split.MinerReward, split.AuxReward)
	if total.Cmp(emission) != 0 {
		return nil, errors.New("activation conformance auxiliary reward split changes emission")
	}
	return []usdbBalanceCredit{
		{recipient: minerRecipient, amount: new(big.Int).Set(split.MinerReward)},
		{recipient: split.AuxRecipient, amount: new(big.Int).Set(split.AuxReward)},
	}, nil
}

func prepareFixedPriceTransition(
	config *params.ChainConfig,
	activation *params.USDBConsensusActivation,
	statedb *state.StateDB,
	blockNumber uint64,
) (*big.Int, []usdbSlotWrite, error) {
	if activation.Versions.PricePolicyVersion != usdb.PricePolicyVersionV1 {
		return nil, nil, fmt.Errorf(
			"unsupported USDB price policy version %d",
			activation.Versions.PricePolicyVersion,
		)
	}
	if blockNumber == 0 {
		return nil, nil, errors.New("USDB reward transition cannot execute at genesis")
	}
	parentActivation, err := config.USDBActivationAt(blockNumber - 1)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve parent USDB price activation: %w", err)
	}
	if parentActivation == nil ||
		parentActivation.Versions.PricePolicyVersion != usdb.PricePolicyVersionV1 {
		return nil, nil, errors.New("parent USDB state has no active fixed-price policy")
	}
	expectedPrice := usdb.FixedPriceAtomsPerBTCV1()
	price, err := usdbstate.ReadUint256(statedb, usdbstate.PriceAtomsPerBTCSlot)
	if err != nil {
		return nil, nil, err
	}
	realPrice, err := usdbstate.ReadUint256(statedb, usdbstate.RealPriceAtomsPerBTCSlot)
	if err != nil {
		return nil, nil, err
	}
	policyVersion, err := usdbstate.ReadUint256(statedb, usdbstate.PricePolicyVersionSlot)
	if err != nil {
		return nil, nil, err
	}
	sourceKind, err := usdbstate.ReadUint256(statedb, usdbstate.PriceSourceKindSlot)
	if err != nil {
		return nil, nil, err
	}
	parentRangeID, err := usdb.FixedPriceRangeIDV1(config.ChainID, parentActivation.Block)
	if err != nil {
		return nil, nil, fmt.Errorf("derive parent fixed-price range id: %w", err)
	}
	if price.Cmp(expectedPrice) != 0 ||
		realPrice.Cmp(expectedPrice) != 0 ||
		policyVersion.Cmp(new(big.Int).SetUint64(uint64(usdb.PricePolicyVersionV1))) != 0 ||
		sourceKind.Cmp(new(big.Int).SetUint64(uint64(usdb.FixedPriceSourceKindV1))) != 0 ||
		statedb.GetState(usdbstate.SystemStateAddress, usdbstate.PricePolicyRangeIDSlot) != parentRangeID {
		return nil, nil, errors.New("parent USDB fixed-price state does not match active policy")
	}

	childRangeID, err := usdb.FixedPriceRangeIDV1(config.ChainID, activation.Block)
	if err != nil {
		return nil, nil, fmt.Errorf("derive child fixed-price range id: %w", err)
	}
	values := []struct {
		slot  common.Hash
		value *big.Int
	}{
		{usdbstate.PriceAtomsPerBTCSlot, expectedPrice},
		{usdbstate.RealPriceAtomsPerBTCSlot, expectedPrice},
		{usdbstate.PricePolicyVersionSlot, new(big.Int).SetUint64(uint64(usdb.PricePolicyVersionV1))},
		{usdbstate.PriceSourceKindSlot, new(big.Int).SetUint64(uint64(usdb.FixedPriceSourceKindV1))},
	}
	writes := make([]usdbSlotWrite, 0, 5)
	for _, value := range values {
		write, err := encodeUSDBSlotWrite(value.slot, value.value)
		if err != nil {
			return nil, nil, err
		}
		writes = append(writes, write)
	}
	writes = append(writes, usdbSlotWrite{
		slot:  usdbstate.PricePolicyRangeIDSlot,
		value: childRangeID,
	})
	return price, writes, nil
}

func prepareKTransition(
	policyVersion uint16,
	statedb *state.StateDB,
	currentEnergy *big.Int,
) (uint64, []usdbSlotWrite, error) {
	switch policyVersion {
	case 0:
		return usdb.KBpsBase, nil, nil
	case usdb.CollaborationEfficiencyPolicyVersionV1:
	default:
		return 0, nil, fmt.Errorf(
			"unsupported USDB collaboration efficiency policy version %d",
			policyVersion,
		)
	}
	if currentEnergy == nil {
		return 0, nil, errors.New("USDB collaboration energy is nil")
	}
	sumBefore, err := usdbstate.ReadUint256(statedb, usdbstate.KWindowSumSlot)
	if err != nil {
		return 0, nil, err
	}
	countBefore, err := readUSDBUint64(statedb, usdbstate.KWindowCountSlot, "K window count")
	if err != nil {
		return 0, nil, err
	}
	cursorBefore, err := readUSDBUint64(statedb, usdbstate.KWindowCursorSlot, "K window cursor")
	if err != nil {
		return 0, nil, err
	}
	if countBefore > usdb.KWindowBlocks || cursorBefore >= usdb.KWindowBlocks {
		return 0, nil, errors.New("USDB K window count or cursor is out of range")
	}
	if countBefore < usdb.KWindowBlocks && cursorBefore != countBefore {
		return 0, nil, errors.New("USDB K warmup cursor does not match sample count")
	}
	maxEnergy := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	maxWindowSum := new(big.Int).Mul(maxEnergy, new(big.Int).SetUint64(countBefore))
	if sumBefore.Cmp(maxWindowSum) > 0 {
		return 0, nil, errors.New("USDB K window sum exceeds initialized sample bounds")
	}

	averageEnergy := new(big.Int)
	kBps := usdb.KBpsBase
	if countBefore == usdb.KWindowBlocks {
		averageEnergy.Div(sumBefore, new(big.Int).SetUint64(usdb.KWindowBlocks))
		kBps, err = usdb.CalculateKBpsV1(currentEnergy, averageEnergy)
		if err != nil {
			return 0, nil, fmt.Errorf("calculate USDB K coefficient: %w", err)
		}
	} else if _, err := usdb.CalculateKBpsV1(currentEnergy, averageEnergy); err != nil {
		return 0, nil, fmt.Errorf("validate USDB collaboration energy: %w", err)
	}

	ringSlot := usdbstate.KCERingSlot(cursorBefore)
	oldSample := new(big.Int)
	if countBefore == usdb.KWindowBlocks {
		oldWord := statedb.GetState(usdbstate.SystemStateAddress, ringSlot)
		oldSample.SetBytes(oldWord[:])
		if oldSample.Cmp(maxEnergy) > 0 || oldSample.Cmp(sumBefore) > 0 {
			return 0, nil, errors.New("USDB K ring sample exceeds window sum")
		}
	} else if statedb.GetState(usdbstate.SystemStateAddress, ringSlot) != (common.Hash{}) {
		return 0, nil, errors.New("USDB K warmup next ring slot is already initialized")
	}
	sumAfter := new(big.Int).Sub(sumBefore, oldSample)
	sumAfter.Add(sumAfter, currentEnergy)
	countAfter := countBefore + 1
	if countAfter > usdb.KWindowBlocks {
		countAfter = usdb.KWindowBlocks
	}
	cursorAfter := (cursorBefore + 1) % usdb.KWindowBlocks

	values := []struct {
		slot  common.Hash
		value *big.Int
	}{
		{ringSlot, currentEnergy},
		{usdbstate.KWindowSumSlot, sumAfter},
		{usdbstate.KWindowCountSlot, new(big.Int).SetUint64(countAfter)},
		{usdbstate.KWindowCursorSlot, new(big.Int).SetUint64(cursorAfter)},
		{usdbstate.KLastCESlot, currentEnergy},
		{usdbstate.KLastAESlot, averageEnergy},
		{usdbstate.KLastKBpsSlot, new(big.Int).SetUint64(kBps)},
	}
	writes := make([]usdbSlotWrite, 0, len(values))
	for _, value := range values {
		write, err := encodeUSDBSlotWrite(value.slot, value.value)
		if err != nil {
			return 0, nil, fmt.Errorf("encode USDB K state: %w", err)
		}
		writes = append(writes, write)
	}
	return kBps, writes, nil
}

func readUSDBUint64(statedb *state.StateDB, slot common.Hash, name string) (uint64, error) {
	value, err := usdbstate.ReadUint256(statedb, slot)
	if err != nil {
		return 0, err
	}
	if !value.IsUint64() {
		return 0, fmt.Errorf("%s does not fit uint64", name)
	}
	return value.Uint64(), nil
}

func encodeUSDBSlotWrite(slot common.Hash, value *big.Int) (usdbSlotWrite, error) {
	word, err := usdbstate.EncodeUint256(value)
	if err != nil {
		return usdbSlotWrite{}, err
	}
	return usdbSlotWrite{slot: slot, value: word}, nil
}
