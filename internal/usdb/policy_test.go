package usdb

import (
	"math/big"
	"testing"
)

func TestLevelForEnergyThresholds(t *testing.T) {
	if level := LevelForEnergy(0); level != 0 {
		t.Fatalf("unexpected level for zero energy: have %d want 0", level)
	}
	if level := LevelForEnergy(DefaultLevelBaseEnergy - 1); level != 0 {
		t.Fatalf("unexpected level below first threshold: have %d want 0", level)
	}
	if level := LevelForEnergy(DefaultLevelBaseEnergy); level != 1 {
		t.Fatalf("unexpected level at first threshold: have %d want 1", level)
	}
	if level := LevelForEnergy(10_000_000); level <= 1 {
		t.Fatalf("expected higher level for larger energy, have %d", level)
	}
}

func TestMultiplierBpsForLevelClampsAndInterpolates(t *testing.T) {
	if got := MultiplierBpsForLevel(0); got != MinimumMultiplierBps {
		t.Fatalf("unexpected min multiplier for level 0: have %d want %d", got, MinimumMultiplierBps)
	}
	if got := MultiplierBpsForLevel(1); got != MinimumMultiplierBps {
		t.Fatalf("unexpected min multiplier for level 1: have %d want %d", got, MinimumMultiplierBps)
	}
	if got := MultiplierBpsForLevel(50); got != MaximumMultiplierBps {
		t.Fatalf("unexpected max multiplier for level 50: have %d want %d", got, MaximumMultiplierBps)
	}
	if got := MultiplierBpsForLevel(99); got != MaximumMultiplierBps {
		t.Fatalf("unexpected clamped max multiplier: have %d want %d", got, MaximumMultiplierBps)
	}
	if got := MultiplierBpsForLevel(25); got <= MinimumMultiplierBps || got >= MaximumMultiplierBps {
		t.Fatalf("expected interpolated multiplier between bounds, have %d", got)
	}
}

func TestRewardForLevelUsesBaseReward(t *testing.T) {
	reward := RewardForLevel(0, 1)
	expected := new(big.Int).Mul(DefaultBaseReward, big.NewInt(int64(MinimumMultiplierBps)))
	expected.Div(expected, big.NewInt(10_000))
	if reward.Cmp(expected) != 0 {
		t.Fatalf("unexpected reward: have %s want %s", reward.String(), expected.String())
	}
	if base := BaseReward(1); base.Cmp(DefaultBaseReward) != 0 {
		t.Fatalf("unexpected base reward: have %s want %s", base.String(), DefaultBaseReward.String())
	}
}
