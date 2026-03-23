package usdb

import (
	"context"
	"fmt"
	"time"
)

// PayloadBuilder creates the current reward payload used by miner/header assembly.
type PayloadBuilder struct {
	client       Client
	passID       PassID
	queryTimeout time.Duration
}

// NewPayloadBuilder constructs a builder from an already-configured USDB client.
func NewPayloadBuilder(client Client, passID string, queryTimeout time.Duration) (*PayloadBuilder, error) {
	if client == nil {
		return nil, fmt.Errorf("nil usdb client")
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
		queryTimeout: queryTimeout,
	}, nil
}

// NewRPCPayloadBuilder dials one USDB endpoint and uses it to generate current reward payloads.
func NewRPCPayloadBuilder(endpoint, passID string, queryTimeout time.Duration) (*PayloadBuilder, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultQueryTimeout)
	defer cancel()

	client, err := DialRPC(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	builder, err := NewPayloadBuilder(client, passID, queryTimeout)
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

// BuildCurrentPayload fetches the latest current system state and emits the v1 header payload.
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
	query := QueryContext{
		RequestedHeight: systemState.LocalSyncedBlockHeight,
		ExpectedState: QueryExpectedState{
			SnapshotID:    systemState.UpstreamSnapshotID,
			SystemStateID: systemState.SystemStateID,
		},
	}
	passSnapshot, err := b.client.GetPassSnapshot(queryCtx, b.passID, query)
	if err != nil {
		return nil, err
	}
	if passSnapshot == nil {
		return nil, fmt.Errorf("configured pass %s not found in current usdb state", b.passID.String())
	}
	payload, err := NewRewardPayloadV1(
		systemState.LocalSyncedBlockHeight,
		systemState.UpstreamSnapshotID,
		systemState.SystemStateID,
		b.passID.String(),
	)
	if err != nil {
		return nil, err
	}
	return payload.MarshalBinary()
}
