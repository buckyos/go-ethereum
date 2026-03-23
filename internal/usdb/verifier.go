package usdb

import (
	"context"
	"fmt"
	"math/big"
	"time"
)

// ResolvedReward is the fully deterministic reward input reconstructed from USDB.
//
// The consensus layer only needs the final miner reward and the static base reward.
// The remaining fields are kept to make testing and future diagnostics easier.
type ResolvedReward struct {
	Payload       RewardPayloadV1
	PassSnapshot  *PassSnapshot
	PassEnergy    *PassEnergySnapshot
	Level         uint32
	MultiplierBps uint64
	BaseReward    *big.Int
	MinerReward   *big.Int
}

// Verifier resolves historical reward inputs from the USDB RPC surface.
type Verifier struct {
	client       Client
	queryTimeout time.Duration
}

// NewVerifier constructs a verifier from an already-configured USDB client.
func NewVerifier(client Client, queryTimeout time.Duration) (*Verifier, error) {
	if client == nil {
		return nil, fmt.Errorf("nil usdb client")
	}
	if queryTimeout <= 0 {
		queryTimeout = DefaultQueryTimeout
	}
	return &Verifier{
		client:       client,
		queryTimeout: queryTimeout,
	}, nil
}

// NewRPCVerifier dials one USDB endpoint and uses it to resolve reward inputs.
func NewRPCVerifier(endpoint string, queryTimeout time.Duration) (*Verifier, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultQueryTimeout)
	defer cancel()

	client, err := DialRPC(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	verifier, err := NewVerifier(client, queryTimeout)
	if err != nil {
		client.Close()
		return nil, err
	}
	return verifier, nil
}

// Close releases the underlying RPC client when the verifier owns one.
func (v *Verifier) Close() {
	if v != nil && v.client != nil {
		v.client.Close()
	}
}

// ResolveReward reconstructs the historical reward input from one header payload.
func (v *Verifier) ResolveReward(ctx context.Context, headerExtra []byte, blockNumber uint64) (*ResolvedReward, error) {
	if len(headerExtra) == 0 {
		return nil, fmt.Errorf("missing usdb reward payload")
	}
	var payload RewardPayloadV1
	if err := payload.UnmarshalBinary(headerExtra); err != nil {
		return nil, err
	}

	queryCtx, cancel := context.WithTimeout(ctx, v.queryTimeout)
	defer cancel()

	query := QueryContext{
		RequestedHeight: payload.BTCHeight,
		ExpectedState: QueryExpectedState{
			SnapshotID:    payload.SnapshotIDHex(),
			SystemStateID: payload.SystemStateIDHex(),
		},
	}
	passSnapshot, err := v.client.GetPassSnapshot(queryCtx, payload.PassID, query)
	if err != nil {
		return nil, err
	}
	if passSnapshot == nil {
		return nil, fmt.Errorf("pass %s not found in usdb state", payload.PassID.String())
	}
	passEnergy, err := v.client.GetPassEnergy(queryCtx, payload.PassID, query)
	if err != nil {
		return nil, err
	}
	if passEnergy == nil {
		return nil, fmt.Errorf("pass %s energy not found in usdb state", payload.PassID.String())
	}

	level := LevelForEnergy(passEnergy.Energy)
	baseReward := BaseReward(blockNumber)
	minerReward := RewardForLevel(blockNumber, level)
	return &ResolvedReward{
		Payload:       payload,
		PassSnapshot:  passSnapshot,
		PassEnergy:    passEnergy,
		Level:         level,
		MultiplierBps: MultiplierBpsForLevel(level),
		BaseReward:    baseReward,
		MinerReward:   minerReward,
	}, nil
}
