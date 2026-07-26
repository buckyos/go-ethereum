package usdb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	// RewardRuleVersionV1 identifies UIP-0011 reward recipient and state-transition rules.
	RewardRuleVersionV1 uint16 = 1
	// CoinbaseEmissionPolicyVersionV1 identifies the first UIP-0011 emission formula.
	CoinbaseEmissionPolicyVersionV1 uint16 = 1
	// FeeSplitPolicyVersionV1 identifies the per-transaction 60/40 fee split.
	FeeSplitPolicyVersionV1 uint16 = 1
	// CollaborationEfficiencyPolicyVersionV1 identifies the UIP-0012 rolling K formula.
	CollaborationEfficiencyPolicyVersionV1 uint16 = 1
	// PricePolicyVersionV1 identifies the UIP-0013 fixed-price policy.
	PricePolicyVersionV1 uint32 = 1

	// BTCSatsPerBTC converts satoshis into BTC for the target-supply formula.
	BTCSatsPerBTC uint64 = 100_000_000
	// EmissionBlocks is the UIP-0011 v1 release smoothing window.
	EmissionBlocks uint64 = 157_680
	// MinerFeeBps is the miner share under UIP-0011 fee policy v1.
	MinerFeeBps uint64 = 6_000
	// DAOFeeBps is the Dividend share under UIP-0011 fee policy v1.
	DAOFeeBps uint64 = 4_000

	// KBpsBase is the neutral UIP-0012 collaboration coefficient.
	KBpsBase uint64 = 10_000
	// KBpsMin is the minimum integer coefficient satisfying K > 0.8.
	KBpsMin uint64 = 8_001
	// KBpsMax is the UIP-0012 collaboration coefficient ceiling.
	KBpsMax uint64 = 20_000
	// KWindowBlocks is the UIP-0012 v1 rolling-window length.
	KWindowBlocks uint64 = 50_400

	// FixedPriceAtomsPerBTCDecimalV1 is 100,000 USDB native units per BTC.
	FixedPriceAtomsPerBTCDecimalV1 = "100000000000000000000000"
	// FixedPriceSourceKindV1 identifies UIP-0013's fixed-price source.
	FixedPriceSourceKindV1 uint32 = 1

	fixedPriceRangeDomainV1 = "usdb.price.policy.range:v1"
)

var (
	// ErrInvalidRewardInput indicates a missing, negative, or out-of-range formula input.
	ErrInvalidRewardInput = errors.New("invalid usdb reward input")
	// ErrRewardOverflow indicates that a formula result does not fit uint256.
	ErrRewardOverflow = errors.New("usdb reward uint256 overflow")
	maximumUint256    = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
)

// EmissionResult contains every auditable intermediate produced by UIP-0011 v1.
type EmissionResult struct {
	TargetSupplyAtoms     *big.Int
	RemainingTargetAtoms  *big.Int
	CoinbaseEmissionAtoms *big.Int
	IssuedUSDBAtomsAfter  *big.Int
}

// FeeSplit contains the complete per-transaction UIP-0011 v1 fee allocation.
type FeeSplit struct {
	MinerAtoms *big.Int
	DAOAtoms   *big.Int
}

// FixedPriceAtomsPerBTCV1 returns an independent copy of the UIP-0013 fixed price.
func FixedPriceAtomsPerBTCV1() *big.Int {
	value, ok := new(big.Int).SetString(FixedPriceAtomsPerBTCDecimalV1, 10)
	if !ok {
		panic("invalid built-in UIP-0013 fixed price")
	}
	return value
}

// FixedPriceRangeIDV1 derives the immutable identity of one v1 activation range.
func FixedPriceRangeIDV1(chainID *big.Int, startBlock uint64) (common.Hash, error) {
	if err := validateUint256Value("chain_id", chainID, false); err != nil {
		return common.Hash{}, err
	}
	encoded := make([]byte, len(fixedPriceRangeDomainV1)+1+32+8+4+4+32)
	offset := copy(encoded, []byte(fixedPriceRangeDomainV1))
	offset++
	chainID.FillBytes(encoded[offset : offset+32])
	offset += 32
	binary.BigEndian.PutUint64(encoded[offset:offset+8], startBlock)
	offset += 8
	binary.BigEndian.PutUint32(encoded[offset:offset+4], PricePolicyVersionV1)
	offset += 4
	binary.BigEndian.PutUint32(encoded[offset:offset+4], FixedPriceSourceKindV1)
	offset += 4
	FixedPriceAtomsPerBTCV1().FillBytes(encoded[offset : offset+32])
	return crypto.Keccak256Hash(encoded), nil
}

// CalculateCoinbaseEmissionV1 applies the UIP-0011 integer target-supply formula.
func CalculateCoinbaseEmissionV1(totalMinerBTCSats, priceAtomsPerBTC, issuedUSDBAtoms *big.Int, kBps uint64) (*EmissionResult, error) {
	if err := validateUint64Value("total_miner_btc_sats", totalMinerBTCSats); err != nil {
		return nil, err
	}
	if err := validateUint256Value("price_atoms_per_btc", priceAtomsPerBTC, false); err != nil {
		return nil, err
	}
	if err := validateUint256Value("issued_usdb_atoms", issuedUSDBAtoms, true); err != nil {
		return nil, err
	}
	if kBps < KBpsMin || kBps > KBpsMax {
		return nil, fmt.Errorf("%w: k_bps %d is outside [%d,%d]", ErrInvalidRewardInput, kBps, KBpsMin, KBpsMax)
	}

	// Use an unbounded intermediate so a valid uint256 target is not rejected
	// merely because total_sats * price temporarily exceeds 256 bits.
	targetSupply := new(big.Int).Mul(totalMinerBTCSats, priceAtomsPerBTC)
	targetSupply.Div(targetSupply, new(big.Int).SetUint64(BTCSatsPerBTC))
	if targetSupply.Cmp(maximumUint256) > 0 {
		return nil, fmt.Errorf("%w: target_supply_atoms", ErrRewardOverflow)
	}

	remainingTarget := new(big.Int)
	if targetSupply.Cmp(issuedUSDBAtoms) > 0 {
		remainingTarget.Sub(targetSupply, issuedUSDBAtoms)
	}
	emission := new(big.Int).Mul(remainingTarget, new(big.Int).SetUint64(kBps))
	emission.Div(emission, new(big.Int).SetUint64(EmissionBlocks*BasisPointDenominator))
	if emission.Cmp(remainingTarget) > 0 {
		emission.Set(remainingTarget)
	}
	issuedAfter := new(big.Int).Add(issuedUSDBAtoms, emission)
	if issuedAfter.Cmp(maximumUint256) > 0 {
		return nil, fmt.Errorf("%w: issued_usdb_atoms_after", ErrRewardOverflow)
	}
	return &EmissionResult{
		TargetSupplyAtoms:     targetSupply,
		RemainingTargetAtoms:  remainingTarget,
		CoinbaseEmissionAtoms: emission,
		IssuedUSDBAtomsAfter:  issuedAfter,
	}, nil
}

// SplitTransactionFeeV1 splits one refund-adjusted fee, assigning rounding to the miner.
func SplitTransactionFeeV1(transactionFeeAtoms *big.Int) (*FeeSplit, error) {
	if err := validateUint256Value("tx_fee_atoms", transactionFeeAtoms, true); err != nil {
		return nil, err
	}
	daoAtoms := new(big.Int).Mul(transactionFeeAtoms, new(big.Int).SetUint64(DAOFeeBps))
	daoAtoms.Div(daoAtoms, new(big.Int).SetUint64(BasisPointDenominator))
	return &FeeSplit{
		MinerAtoms: new(big.Int).Sub(transactionFeeAtoms, daoAtoms),
		DAOAtoms:   daoAtoms,
	}, nil
}

// CalculateKBpsV1 computes the UIP-0012 coefficient from current and average energy.
func CalculateKBpsV1(currentEnergy, averageEnergy *big.Int) (uint64, error) {
	if err := validateEnergyValue("current_energy", currentEnergy); err != nil {
		return 0, err
	}
	if err := validateEnergyValue("average_energy", averageEnergy); err != nil {
		return 0, err
	}
	if averageEnergy.Sign() == 0 {
		return KBpsBase, nil
	}
	if currentEnergy.Cmp(averageEnergy) < 0 {
		numerator := new(big.Int).Mul(new(big.Int).SetUint64(60_000), averageEnergy)
		denominator := new(big.Int).Mul(new(big.Int).SetUint64(5), averageEnergy)
		denominator.Add(denominator, currentEnergy)
		penalty := ceilDivide(numerator, denominator)
		k := new(big.Int).Sub(new(big.Int).SetUint64(KBpsMax), penalty)
		if k.Cmp(new(big.Int).SetUint64(KBpsMin)) < 0 {
			return KBpsMin, nil
		}
		return k.Uint64(), nil
	}
	k := new(big.Int).Mul(new(big.Int).SetUint64(KBpsBase), currentEnergy)
	k.Div(k, averageEnergy)
	if k.Cmp(new(big.Int).SetUint64(KBpsMax)) > 0 {
		return KBpsMax, nil
	}
	return k.Uint64(), nil
}

func validateUint64Value(name string, value *big.Int) error {
	if value == nil || value.Sign() < 0 || !value.IsUint64() {
		return fmt.Errorf("%w: %s must be uint64", ErrInvalidRewardInput, name)
	}
	return nil
}

func validateUint256Value(name string, value *big.Int, allowZero bool) error {
	if value == nil || value.Sign() < 0 || value.Cmp(maximumUint256) > 0 {
		return fmt.Errorf("%w: %s must be uint256", ErrInvalidRewardInput, name)
	}
	if !allowZero && value.Sign() == 0 {
		return fmt.Errorf("%w: %s must be positive", ErrInvalidRewardInput, name)
	}
	return nil
}

func validateEnergyValue(name string, value *big.Int) error {
	if value == nil || value.Sign() < 0 || value.Cmp(maximumEnergyValue) > 0 {
		return fmt.Errorf("%w: %s must be uint128", ErrInvalidRewardInput, name)
	}
	return nil
}

func ceilDivide(numerator, denominator *big.Int) *big.Int {
	adjusted := new(big.Int).Sub(denominator, big.NewInt(1))
	adjusted.Add(adjusted, numerator)
	return adjusted.Div(adjusted, denominator)
}
