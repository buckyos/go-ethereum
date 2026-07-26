package usdb

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

// BuiltProfileSelector is the validated miner-side output for one USDB block.
type BuiltProfileSelector struct {
	Payload         []byte
	RewardRecipient common.Address
}

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
	if !chainConfig.HasUSDBConsensus() {
		return nil, fmt.Errorf("chain config has no usdb consensus configuration")
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

// NewRPCPayloadBuilder dials one usdb-indexer endpoint and uses it to generate current selectors.
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
func (b *PayloadBuilder) BuildCurrentPayload(ctx context.Context, blockNumber uint64) (*BuiltProfileSelector, error) {
	activation, err := b.chainConfig.USDBActivationAt(blockNumber)
	if err != nil {
		return nil, err
	}
	if activation == nil {
		return nil, fmt.Errorf("usdb consensus is not active at block %d", blockNumber)
	}
	policy := &activation.Versions
	if policy.PayloadVersion != ProfileSelectorPayloadVersionV1 {
		return nil, fmt.Errorf("%w: chain config expects %d, builder supports %d", ErrProfileSelectorVersion, policy.PayloadVersion, ProfileSelectorPayloadVersionV1)
	}
	btcRegistry, err := loadBTCActivationRegistry(activation.BTCActivationRegistryID)
	if err != nil {
		return nil, fmt.Errorf("invalid chain-config BTC activation registry at block %d: %w", blockNumber, err)
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
	if err := validateCurrentActivationIdentity(
		systemState.LocalSyncedBlockHeight,
		systemState.ActiveVersionSet,
		systemState.ActiveVersionSetID,
		systemState.ActivationRegistryID,
		btcRegistry,
	); err != nil {
		return nil, fmt.Errorf("invalid current usdb activation identity: %w", err)
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
	profile, err := resolveConsensusProfile(queryCtx, b.client, btcRegistry, *payload)
	if err != nil {
		return nil, fmt.Errorf("configured pass %s is not valid in current usdb state: %w", b.passID.String(), err)
	}
	if profile.View.ExternalState.ActiveVersionSetID != systemState.ActiveVersionSetID {
		return nil, fmt.Errorf("current usdb activation identity changed while building payload")
	}
	encoded, err := payload.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return &BuiltProfileSelector{
		Payload:         encoded,
		RewardRecipient: profile.RewardRecipient,
	}, nil
}

func validateCurrentActivationIdentity(
	btcHeight uint32,
	activeVersionSet ActiveVersionSet,
	activeVersionSetID string,
	actualRegistryID string,
	expectedRegistry *btcActivationRegistry,
) error {
	actualRegistry, err := loadBTCActivationRegistry(actualRegistryID)
	if err != nil {
		return fmt.Errorf("current service registry is not in the local catalog: %w", err)
	}
	if _, err := actualRegistry.validateIdentity(
		btcHeight,
		actualRegistryID,
		activeVersionSet,
		activeVersionSetID,
	); err != nil {
		return err
	}
	if _, err := expectedRegistry.validateIdentity(
		btcHeight,
		expectedRegistry.ActivationRegistryID,
		activeVersionSet,
		activeVersionSetID,
	); err != nil {
		return fmt.Errorf("chain-config registry does not preserve current BTC state: %w", err)
	}
	return nil
}
