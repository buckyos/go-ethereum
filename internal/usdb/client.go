package usdb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

const DefaultQueryTimeout = 3 * time.Second

// QueryContext pins a USDB historical view for deterministic validator replay.
type QueryContext struct {
	RequestedHeight uint32             `json:"requested_height"`
	ExpectedState   QueryExpectedState `json:"expected_state"`
}

// QueryExpectedState is the subset of state selectors currently needed by ETHW.
type QueryExpectedState struct {
	SnapshotID    string `json:"snapshot_id,omitempty"`
	SystemStateID string `json:"system_state_id,omitempty"`
}

// SystemStateInfo is the subset of current USDB system state needed by ETHW payload generation.
type SystemStateInfo struct {
	LocalSyncedBlockHeight uint32 `json:"local_synced_block_height"`
	UpstreamSnapshotID     string `json:"upstream_snapshot_id"`
	SystemStateID          string `json:"system_state_id"`
}

// PassSnapshot is the subset of USDB pass data needed by ETHW reward validation.
type PassSnapshot struct {
	InscriptionID  string `json:"inscription_id"`
	State          string `json:"state"`
	Owner          string `json:"owner"`
	ResolvedHeight uint32 `json:"resolved_height"`
}

// PassEnergySnapshot is the subset of USDB energy data needed by ETHW reward validation.
type PassEnergySnapshot struct {
	InscriptionID  string `json:"inscription_id"`
	Energy         uint64 `json:"energy"`
	ResolvedHeight uint32 `json:"query_block_height"`
}

// Client is the minimal USDB RPC surface ETHW needs to generate and validate reward payloads.
type Client interface {
	GetSystemStateInfo(ctx context.Context) (*SystemStateInfo, error)
	GetPassSnapshot(ctx context.Context, passID PassID, query QueryContext) (*PassSnapshot, error)
	GetPassEnergy(ctx context.Context, passID PassID, query QueryContext) (*PassEnergySnapshot, error)
	Close()
}

// RPCClient is a small JSON-RPC adapter over usdb-indexer.
type RPCClient struct {
	client *gethrpc.Client
}

// DialRPC establishes a reusable client to one USDB RPC endpoint.
func DialRPC(ctx context.Context, endpoint string) (*RPCClient, error) {
	client, err := gethrpc.DialContext(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to dial usdb rpc %q: %w", endpoint, err)
	}
	return &RPCClient{client: client}, nil
}

func (c *RPCClient) Close() {
	if c != nil && c.client != nil {
		c.client.Close()
	}
}

func (c *RPCClient) GetSystemStateInfo(ctx context.Context) (*SystemStateInfo, error) {
	var raw json.RawMessage
	if err := c.client.CallContext(ctx, &raw, "get_system_state_info"); err != nil {
		return nil, fmt.Errorf("failed to call get_system_state_info: %w", err)
	}
	if isNullJSON(raw) {
		return nil, nil
	}
	var info SystemStateInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("failed to decode get_system_state_info result: %w", err)
	}
	return &info, nil
}

func (c *RPCClient) GetPassSnapshot(ctx context.Context, passID PassID, query QueryContext) (*PassSnapshot, error) {
	var raw json.RawMessage
	params := map[string]interface{}{
		"inscription_id": passID.String(),
		"at_height":      query.RequestedHeight,
		"context":        query,
	}
	if err := c.client.CallContext(ctx, &raw, "get_pass_snapshot", params); err != nil {
		return nil, fmt.Errorf("failed to call get_pass_snapshot: %w", err)
	}
	if isNullJSON(raw) {
		return nil, nil
	}
	var snapshot PassSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, fmt.Errorf("failed to decode get_pass_snapshot result: %w", err)
	}
	return &snapshot, nil
}

func (c *RPCClient) GetPassEnergy(ctx context.Context, passID PassID, query QueryContext) (*PassEnergySnapshot, error) {
	var raw json.RawMessage
	params := map[string]interface{}{
		"inscription_id": passID.String(),
		"block_height":   query.RequestedHeight,
		"context":        query,
		"mode":           "at_or_before",
	}
	if err := c.client.CallContext(ctx, &raw, "get_pass_energy", params); err != nil {
		return nil, fmt.Errorf("failed to call get_pass_energy: %w", err)
	}
	if isNullJSON(raw) {
		return nil, nil
	}
	var snapshot PassEnergySnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, fmt.Errorf("failed to decode get_pass_energy result: %w", err)
	}
	return &snapshot, nil
}

func isNullJSON(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}
