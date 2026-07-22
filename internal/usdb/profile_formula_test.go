package usdb

import (
	"errors"
	"math/big"
	"testing"
)

func TestLevelForEffectiveEnergyCoversEveryThreshold(t *testing.T) {
	if level := LevelForEffectiveEnergy(big.NewInt(0)); level != 0 {
		t.Fatalf("unexpected level for zero energy: have %d want 0", level)
	}
	for level := uint8(1); level <= MaximumLevel; level++ {
		threshold := levelThresholds[level]
		below := new(big.Int).SetUint64(threshold - 1)
		at := new(big.Int).SetUint64(threshold)
		if got := LevelForEffectiveEnergy(below); got != level-1 {
			t.Fatalf("level %d below threshold: have %d want %d", level, got, level-1)
		}
		if got := LevelForEffectiveEnergy(at); got != level {
			t.Fatalf("level %d at threshold: have %d want %d", level, got, level)
		}
	}
	if level := LevelForEffectiveEnergy(maximumEnergyValue); level != MaximumLevel {
		t.Fatalf("unexpected level at uint128 max: have %d want %d", level, MaximumLevel)
	}
}

func TestDifficultyFactorBpsForLevelClamps(t *testing.T) {
	tests := []struct {
		level uint8
		want  uint64
	}{
		{level: 0, want: 10_000},
		{level: 1, want: 9_900},
		{level: 49, want: 5_100},
		{level: 50, want: 5_000},
		{level: 51, want: 5_000},
		{level: 255, want: 5_000},
	}
	for _, test := range tests {
		if got := DifficultyFactorBpsForLevel(test.level); got != test.want {
			t.Fatalf("level %d: have %d want %d", test.level, got, test.want)
		}
	}
}

func TestRealDifficultyV1UsesCeilingDivision(t *testing.T) {
	tests := []struct {
		base   int64
		factor uint64
		want   int64
	}{
		{base: 101, factor: 9_900, want: 100},
		{base: 8_192, factor: 10_000, want: 8_192},
		{base: 8_193, factor: 5_000, want: 4_097},
	}
	for _, test := range tests {
		got, err := RealDifficultyV1(big.NewInt(test.base), test.factor)
		if err != nil {
			t.Fatalf("base=%d factor=%d failed: %v", test.base, test.factor, err)
		}
		if got.Cmp(big.NewInt(test.want)) != 0 {
			t.Fatalf("base=%d factor=%d: have %s want %d", test.base, test.factor, got, test.want)
		}
	}
}

func TestRealDifficultyV1RejectsInvalidInputs(t *testing.T) {
	for _, base := range []*big.Int{nil, big.NewInt(0), big.NewInt(-1)} {
		if _, err := RealDifficultyV1(base, BasisPointDenominator); !errors.Is(err, ErrInvalidBaseDifficulty) {
			t.Fatalf("base=%v: expected invalid base difficulty, got %v", base, err)
		}
	}
	for _, factor := range []uint64{MinimumDifficultyFactorBps - 1, BasisPointDenominator + 1} {
		if _, err := RealDifficultyV1(big.NewInt(1), factor); !errors.Is(err, ErrInvalidDifficultyFactor) {
			t.Fatalf("factor=%d: expected invalid factor, got %v", factor, err)
		}
	}
}

func TestParseEnergyDecimalRequiresCanonicalUint128(t *testing.T) {
	valid := []string{"0", "1", maximumEnergyValue.String()}
	for _, value := range valid {
		parsed, err := parseEnergyDecimal("energy", value)
		if err != nil {
			t.Fatalf("expected %q to be valid: %v", value, err)
		}
		if parsed.String() != value {
			t.Fatalf("unexpected parsed value: have %s want %s", parsed, value)
		}
	}

	overflow := new(big.Int).Add(maximumEnergyValue, big.NewInt(1)).String()
	for _, value := range []string{"", "00", "01", "+1", "-1", "1.0", overflow} {
		if _, err := parseEnergyDecimal("energy", value); !errors.Is(err, ErrInvalidProfileEnergy) {
			t.Fatalf("expected %q to fail as invalid energy, got %v", value, err)
		}
	}
}

func TestSaturatingAddEnergyClampsAtUint128(t *testing.T) {
	if got := saturatingAddEnergy(big.NewInt(10), big.NewInt(20)); got.Cmp(big.NewInt(30)) != 0 {
		t.Fatalf("unexpected regular sum: %s", got)
	}
	if got := saturatingAddEnergy(maximumEnergyValue, big.NewInt(1)); got.Cmp(maximumEnergyValue) != 0 {
		t.Fatalf("unexpected saturated sum: have %s want %s", got, maximumEnergyValue)
	}
}
