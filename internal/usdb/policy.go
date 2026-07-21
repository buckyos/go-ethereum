package usdb

import (
	"math/big"
)

const (
	MinimumRewardLevel uint8 = 1
	MaximumRewardLevel uint8 = MaximumLevel

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

// MultiplierBpsForLevel maps levels into the v1 linear reward multiplier band.
func MultiplierBpsForLevel(level uint8) uint64 {
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
func RewardForLevel(blockNumber uint64, level uint8) *big.Int {
	reward := BaseReward(blockNumber)
	reward.Mul(reward, new(big.Int).SetUint64(MultiplierBpsForLevel(level)))
	reward.Div(reward, new(big.Int).SetUint64(10_000))
	return reward
}
