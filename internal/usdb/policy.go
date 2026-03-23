package usdb

import (
	"math/big"
)

const (
	DefaultLevelBaseEnergy uint64 = 1_000_000
	DefaultLevelRatioNum   uint64 = 118
	DefaultLevelRatioDen   uint64 = 100

	MinimumRewardLevel uint32 = 1
	MaximumRewardLevel uint32 = 50

	MinimumMultiplierBps uint64 = 5_000
	MaximumMultiplierBps uint64 = 20_000
)

var (
	weiPerETH = big.NewInt(1_000_000_000_000_000_000)

	// DefaultBaseReward is the initial USDB chain base miner reward for v1.
	DefaultBaseReward = new(big.Int).Mul(big.NewInt(5), weiPerETH)
)

// BaseReward returns the v1 base miner reward. The first version keeps this constant.
func BaseReward(_ uint64) *big.Int {
	return new(big.Int).Set(DefaultBaseReward)
}

// LevelForEnergy deterministically derives a mock level from historical pass energy.
//
// The implementation intentionally avoids floating-point arithmetic in consensus logic.
// It follows the agreed geometric-threshold model using the exact rational ratio 1.18.
func LevelForEnergy(energy uint64) uint32 {
	if energy == 0 {
		return 0
	}
	remaining := new(big.Rat).SetInt(new(big.Int).SetUint64(energy))
	increment := new(big.Rat).SetInt(new(big.Int).SetUint64(DefaultLevelBaseEnergy))
	ratio := new(big.Rat).SetFrac(
		new(big.Int).SetUint64(DefaultLevelRatioNum),
		new(big.Int).SetUint64(DefaultLevelRatioDen),
	)
	level := uint32(0)
	for remaining.Cmp(increment) >= 0 {
		remaining.Sub(remaining, increment)
		level++
		increment.Mul(increment, ratio)
	}
	return level
}

// MultiplierBpsForLevel maps levels into the v1 linear reward multiplier band.
func MultiplierBpsForLevel(level uint32) uint64 {
	if level <= MinimumRewardLevel {
		return MinimumMultiplierBps
	}
	if level >= MaximumRewardLevel {
		return MaximumMultiplierBps
	}
	span := MaximumMultiplierBps - MinimumMultiplierBps
	offset := uint64(level - MinimumRewardLevel)
	steps := uint64(MaximumRewardLevel - MinimumRewardLevel)
	return MinimumMultiplierBps + (span*offset)/steps
}

// RewardForLevel applies the v1 multiplier band to the current base reward.
func RewardForLevel(blockNumber uint64, level uint32) *big.Int {
	reward := BaseReward(blockNumber)
	reward.Mul(reward, new(big.Int).SetUint64(MultiplierBpsForLevel(level)))
	reward.Div(reward, new(big.Int).SetUint64(10_000))
	return reward
}
