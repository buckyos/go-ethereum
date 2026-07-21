package usdb

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/params"
)

// PayloadBuilder creates the current UIP-0007 selector used by miner/header assembly.
type PayloadBuilder struct {
	client       Client
	passID       PassID
	chainConfig  *params.ChainConfig
	queryTimeout time.Duration
}

// NewPayloadBuilder constructs a builder from an already-configured USDB client.
func NewPayloadBuilder(client Client, passID string, chainConfig *params.ChainConfig, queryTimeout time.Duration) (*PayloadBuilder, error) {
	if client == nil {
		return nil, fmt.Errorf("nil usdb client")
	}
	if chainConfig == nil {
		return nil, fmt.Errorf("nil chain config")
	}
	parsedPassID, err := ParsePassID(passID)
	if err != nil {
		return nil, err
	}
	if queryTimeout <= 0 {
		queryTimeout = DefaultQueryTimeout
	}
	return &PayloadBuilder{
		client:       client,
		passID:       parsedPassID,
		chainConfig:  chainConfig,
		queryTimeout: queryTimeout,
	}, nil
}

// NewRPCPayloadBuilder dials one USDB endpoint and uses it to generate current selectors.
func NewRPCPayloadBuilder(endpoint, passID string, chainConfig *params.ChainConfig, queryTimeout time.Duration) (*PayloadBuilder, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultQueryTimeout)
	defer cancel()

	client, err := DialRPC(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	builder, err := NewPayloadBuilder(client, passID, chainConfig, queryTimeout)
	if err != nil {
		client.Close()
		return nil, err
	}
	return builder, nil
}

// Close releases the underlying RPC client when the builder owns one.
func (b *PayloadBuilder) Close() {
	if b != nil && b.client != nil {
		b.client.Close()
	}
}

// BuildCurrentPayload emits a selector for blockNumber only after resolving its
// consensus policy and validating the configured pass in current state.
func (b *PayloadBuilder) BuildCurrentPayload(ctx context.Context, blockNumber uint64) ([]byte, error) {
	policy, err := b.chainConfig.USDBConsensusAt(blockNumber)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		return nil, fmt.Errorf("usdb consensus is not active at block %d", blockNumber)
	}
	if policy.PayloadVersion != ProfileSelectorPayloadVersionV1 {
		return nil, fmt.Errorf("%w: chain config expects %d, builder supports %d", ErrProfileSelectorVersion, policy.PayloadVersion, ProfileSelectorPayloadVersionV1)
	}

	queryCtx, cancel := context.WithTimeout(ctx, b.queryTimeout)
	defer cancel()

	systemState, err := b.client.GetSystemStateInfo(queryCtx)
	if err != nil {
		return nil, err
	}
	if systemState == nil {
		return nil, fmt.Errorf("usdb returned no current system state")
	}
	payload, err := NewProfileSelectorPayload(
		policy.DifficultyPolicyVersion,
		systemState.LocalSyncedBlockHeight,
		systemState.UpstreamSnapshotID,
		systemState.SystemStateID,
		b.passID.String(),
	)
	if err != nil {
		return nil, err
	}
	if _, err := resolveConsensusProfile(queryCtx, b.client, *payload); err != nil {
		return nil, fmt.Errorf("configured pass %s is not valid in current usdb state: %w", b.passID.String(), err)
	}
	return payload.MarshalBinary()
}
