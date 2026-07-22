package usdb

import (
	"errors"
	"fmt"
	"math/big"
)

const (
	// MaximumLevel is the UIP-0005 v1 level ceiling.
	MaximumLevel uint8 = 50
	// BasisPointDenominator is the shared UIP-0004/UIP-0005 bps denominator.
	BasisPointDenominator uint64 = 10_000
	// LevelDiscountBps is the difficulty discount contributed by each level.
	LevelDiscountBps uint64 = 100
	// MinimumDifficultyFactorBps limits the maximum difficulty discount to 50%.
	MinimumDifficultyFactorBps uint64 = 5_000
)

var (
	// ErrInvalidProfileEnergy indicates a non-canonical or out-of-range energy value.
	ErrInvalidProfileEnergy = errors.New("invalid usdb profile energy")
	// ErrInvalidBaseDifficulty indicates a missing or non-positive ETHW base difficulty.
	ErrInvalidBaseDifficulty = errors.New("invalid usdb base difficulty")
	// ErrInvalidDifficultyFactor indicates a factor outside the UIP-0005 v1 range.
	ErrInvalidDifficultyFactor = errors.New("invalid usdb difficulty factor")
	maximumEnergyValue         = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
)

// UIP-0005 freezes these integer thresholds so every implementation has identical boundaries.
var levelThresholds = [...]uint64{
	0,
	1_000_000,
	2_180_000,
	3_572_400,
	5_215_432,
	7_154_210,
	9_441_968,
	12_141_522,
	15_326_996,
	19_085_855,
	23_521_309,
	28_755_145,
	34_931_071,
	42_218_663,
	50_818_023,
	60_965_267,
	72_939_014,
	87_068_037,
	103_740_283,
	123_413_534,
	146_627_971,
	174_021_005,
	206_344_786,
	244_486_847,
	289_494_480,
	342_603_486,
	405_272_113,
	479_221_094,
	566_480_891,
	669_447_451,
	790_947_992,
	934_318_630,
	1_103_495_984,
	1_303_125_261,
	1_538_687_807,
	1_816_651_613,
	2_144_648_903,
	2_531_685_705,
	2_988_389_132,
	3_527_299_176,
	4_163_213_027,
	4_913_591_372,
	5_799_037_819,
	6_843_864_626,
	8_076_760_259,
	9_531_577_106,
	11_248_260_984,
	13_273_947_962,
	15_664_258_595,
	18_484_825_142,
	21_813_093_667,
}

// LevelForEffectiveEnergy derives the UIP-0005 level from validated effective energy.
func LevelForEffectiveEnergy(effectiveEnergy *big.Int) uint8 {
	if effectiveEnergy == nil || effectiveEnergy.Sign() <= 0 {
		return 0
	}
	if !effectiveEnergy.IsUint64() {
		return MaximumLevel
	}
	value := effectiveEnergy.Uint64()
	level := uint8(0)
	for candidateLevel, threshold := range levelThresholds {
		if value < threshold {
			break
		}
		level = uint8(candidateLevel)
	}
	return level
}

// DifficultyFactorBpsForLevel derives the UIP-0005 nominal difficulty factor.
func DifficultyFactorBpsForLevel(level uint8) uint64 {
	if level > MaximumLevel {
		level = MaximumLevel
	}
	factor := BasisPointDenominator - uint64(level)*LevelDiscountBps
	if factor < MinimumDifficultyFactorBps {
		return MinimumDifficultyFactorBps
	}
	return factor
}

// RealDifficultyV1 applies the UIP-0005 factor to an ETHW base difficulty with
// consensus-safe ceiling division.
func RealDifficultyV1(baseDifficulty *big.Int, difficultyFactorBps uint64) (*big.Int, error) {
	if baseDifficulty == nil || baseDifficulty.Sign() <= 0 {
		return nil, ErrInvalidBaseDifficulty
	}
	if difficultyFactorBps < MinimumDifficultyFactorBps || difficultyFactorBps > BasisPointDenominator {
		return nil, fmt.Errorf("%w: %d", ErrInvalidDifficultyFactor, difficultyFactorBps)
	}
	numerator := new(big.Int).Mul(baseDifficulty, new(big.Int).SetUint64(difficultyFactorBps))
	numerator.Add(numerator, new(big.Int).SetUint64(BasisPointDenominator-1))
	return numerator.Div(numerator, new(big.Int).SetUint64(BasisPointDenominator)), nil
}

func parseEnergyDecimal(field, value string) (*big.Int, error) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return nil, fmt.Errorf("%w: %s is not canonical decimal", ErrInvalidProfileEnergy, field)
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return nil, fmt.Errorf("%w: %s is not canonical decimal", ErrInvalidProfileEnergy, field)
		}
	}
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok || parsed.Cmp(maximumEnergyValue) > 0 {
		return nil, fmt.Errorf("%w: %s exceeds uint128", ErrInvalidProfileEnergy, field)
	}
	return parsed, nil
}

func saturatingAddEnergy(left, right *big.Int) *big.Int {
	sum := new(big.Int).Add(left, right)
	if sum.Cmp(maximumEnergyValue) > 0 {
		return new(big.Int).Set(maximumEnergyValue)
	}
	return sum
}
