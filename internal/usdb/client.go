package usdb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

const (
	// DefaultQueryTimeout bounds one USDB companion-service query.
	DefaultQueryTimeout = 3 * time.Second
	// EconomicStateViewVersionV1 is the frozen UIP-0006 profile response contract.
	EconomicStateViewVersionV1 = "uip-0006-usdb-economic-state-view:v1"
)

// QueryContext pins a USDB historical view for deterministic validator replay.
type QueryContext struct {
	RequestedHeight uint32             `json:"requested_height"`
	ExpectedState   QueryExpectedState `json:"expected_state"`
}

// QueryExpectedState contains the selectors committed by UIP-0007 header.Extra.
type QueryExpectedState struct {
	SnapshotID    string `json:"snapshot_id,omitempty"`
	SystemStateID string `json:"system_state_id,omitempty"`
}

// SystemStateInfo is the current USDB state needed for miner selector generation.
type SystemStateInfo struct {
	LocalSyncedBlockHeight uint32 `json:"local_synced_block_height"`
	UpstreamSnapshotID     string `json:"upstream_snapshot_id"`
	SystemStateID          string `json:"system_state_id"`
}

// EconomicExternalState is the exact historical state identity returned by UIP-0006.
type EconomicExternalState struct {
	BTCHeight                      uint32 `json:"btc_height"`
	SnapshotID                     string `json:"snapshot_id"`
	StableBlockHash                string `json:"stable_block_hash"`
	LocalStateCommit               string `json:"local_state_commit"`
	SystemStateID                  string `json:"system_state_id"`
	BalanceHistoryAPIVersion       string `json:"balance_history_api_version"`
	BalanceHistorySemanticsVersion string `json:"balance_history_semantics_version"`
	USDBIndexProtocolVersion       string `json:"usdb_index_protocol_version"`
	USDBIndexFormulaVersion        string `json:"usdb_index_formula_version"`
}

// PassEconomicProfile is one pass and its UIP-0003 through UIP-0005 derived fields.
type PassEconomicProfile struct {
	PassID               string  `json:"pass_id"`
	OwnerScriptHash      string  `json:"owner_script_hash"`
	OwnerBTCAddress      *string `json:"owner_btc_addr"`
	State                string  `json:"state"`
	PassKind             string  `json:"pass_kind"`
	RawEnergy            string  `json:"raw_energy"`
	CollabContribution   string  `json:"collab_contribution"`
	EffectiveEnergy      string  `json:"effective_energy"`
	Level                uint8   `json:"level"`
	DifficultyFactorBps  uint64  `json:"difficulty_factor_bps"`
	CollabBreakdownCount uint64  `json:"collab_breakdown_count"`
}

// PassEconomicProfileView is the frozen UIP-0006 response consumed by ETHW.
type PassEconomicProfileView struct {
	ViewVersion   string                `json:"view_version"`
	ExternalState EconomicExternalState `json:"external_state"`
	Pass          PassEconomicProfile   `json:"pass"`
}

type passEconomicProfileParams struct {
	ViewVersion string       `json:"view_version"`
	PassID      string       `json:"pass_id"`
	BlockHeight uint32       `json:"block_height"`
	Context     QueryContext `json:"context"`
}

// Client is the minimal USDB RPC surface needed to build and resolve selectors.
type Client interface {
	GetSystemStateInfo(ctx context.Context) (*SystemStateInfo, error)
	GetPassEconomicProfile(ctx context.Context, passID PassID, query QueryContext) (*PassEconomicProfileView, error)
	Close()
}

type jsonRPCClient interface {
	CallContext(ctx context.Context, result interface{}, method string, args ...interface{}) error
	Close()
}

// RPCClient is a small JSON-RPC adapter over usdb-indexer.
type RPCClient struct {
	client jsonRPCClient
}

// DialRPC establishes a reusable client to one USDB RPC endpoint.
func DialRPC(ctx context.Context, endpoint string) (*RPCClient, error) {
	client, err := gethrpc.DialContext(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to dial usdb rpc %q: %w", endpoint, err)
	}
	return &RPCClient{client: client}, nil
}

// Close releases the underlying JSON-RPC connection.
func (c *RPCClient) Close() {
	if c != nil && c.client != nil {
		c.client.Close()
	}
}

// GetSystemStateInfo returns the current durable USDB system state.
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

// GetPassEconomicProfile resolves one pass under the selector-pinned UIP-0006 state view.
func (c *RPCClient) GetPassEconomicProfile(ctx context.Context, passID PassID, query QueryContext) (*PassEconomicProfileView, error) {
	var raw json.RawMessage
	params := passEconomicProfileParams{
		ViewVersion: EconomicStateViewVersionV1,
		PassID:      passID.String(),
		BlockHeight: query.RequestedHeight,
		Context:     query,
	}
	if err := c.client.CallContext(ctx, &raw, "get_pass_economic_profile", params); err != nil {
		return nil, fmt.Errorf("failed to call get_pass_economic_profile: %w", err)
	}
	if isNullJSON(raw) {
		return nil, nil
	}
	var profile PassEconomicProfileView
	if err := json.Unmarshal(raw, &profile); err != nil {
		return nil, fmt.Errorf("failed to decode get_pass_economic_profile result: %w", err)
	}
	return &profile, nil
}

func isNullJSON(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}
