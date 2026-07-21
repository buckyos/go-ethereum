package usdb

import (
	"context"
	"fmt"
	"time"
)

// PayloadBuilder creates the current UIP-0007 selector used by miner/header assembly.
type PayloadBuilder struct {
	client                  Client
	passID                  PassID
	difficultyPolicyVersion uint16
	queryTimeout            time.Duration
}

// NewPayloadBuilder constructs a builder from an already-configured USDB client.
func NewPayloadBuilder(client Client, passID string, difficultyPolicyVersion uint16, queryTimeout time.Duration) (*PayloadBuilder, error) {
	if client == nil {
		return nil, fmt.Errorf("nil usdb client")
	}
	if difficultyPolicyVersion == 0 {
		return nil, fmt.Errorf("difficulty policy version must be positive")
	}
	parsedPassID, err := ParsePassID(passID)
	if err != nil {
		return nil, err
	}
	if queryTimeout <= 0 {
		queryTimeout = DefaultQueryTimeout
	}
	return &PayloadBuilder{
		client:                  client,
		passID:                  parsedPassID,
		difficultyPolicyVersion: difficultyPolicyVersion,
		queryTimeout:            queryTimeout,
	}, nil
}

// NewRPCPayloadBuilder dials one USDB endpoint and uses it to generate current selectors.
func NewRPCPayloadBuilder(endpoint, passID string, difficultyPolicyVersion uint16, queryTimeout time.Duration) (*PayloadBuilder, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultQueryTimeout)
	defer cancel()

	client, err := DialRPC(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	builder, err := NewPayloadBuilder(client, passID, difficultyPolicyVersion, queryTimeout)
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

// BuildCurrentPayload emits a selector only after validating the configured pass in current state.
func (b *PayloadBuilder) BuildCurrentPayload(ctx context.Context) ([]byte, error) {
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
		b.difficultyPolicyVersion,
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
