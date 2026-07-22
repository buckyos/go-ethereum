package usdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

const (
	// DefaultQueryTimeout bounds one USDB companion-service query.
	DefaultQueryTimeout = 3 * time.Second
	// EconomicStateViewVersionV1 is the frozen UIP-0006 profile response contract.
	EconomicStateViewVersionV1 = "uip-0006-usdb-economic-state-view:v1"

	rpcErrPassNotFound             = -32011
	rpcErrInternalInvariantBroken  = -32017
	rpcErrHeightNotSynced          = -32040
	rpcErrSnapshotNotReady         = -32041
	rpcErrSnapshotIDMismatch       = -32042
	rpcErrBlockHashMismatch        = -32043
	rpcErrVersionMismatch          = -32044
	rpcErrLocalStateCommitMismatch = -32045
	rpcErrSystemStateIDMismatch    = -32046
	rpcErrNoRecord                 = -32047
	rpcErrStateNotRetained         = -32048
	rpcErrHistoryNotAvailable      = -32049
	rpcErrViewVersionMismatch      = -32050
	rpcErrProtocolVersionMismatch  = -32051
	rpcErrFormulaVersionMismatch   = -32052
)

var (
	ErrPassNotFound             = errors.New("usdb pass not found")
	ErrInternalInvariantBroken  = errors.New("usdb internal invariant broken")
	ErrHeightNotSynced          = errors.New("usdb height not synced")
	ErrSnapshotNotReady         = errors.New("usdb snapshot not ready")
	ErrSnapshotIDMismatch       = errors.New("usdb snapshot id mismatch")
	ErrBlockHashMismatch        = errors.New("usdb block hash mismatch")
	ErrVersionMismatch          = errors.New("usdb version mismatch")
	ErrLocalStateCommitMismatch = errors.New("usdb local state commit mismatch")
	ErrSystemStateIDMismatch    = errors.New("usdb system state id mismatch")
	ErrNoRecord                 = errors.New("usdb record not found")
	ErrStateNotRetained         = errors.New("usdb state not retained")
	ErrHistoryNotAvailable      = errors.New("usdb history not available")
	ErrViewVersionMismatch      = errors.New("usdb view version mismatch")
	ErrProtocolVersionMismatch  = errors.New("usdb protocol version mismatch")
	ErrFormulaVersionMismatch   = errors.New("usdb formula version mismatch")
)

// RPCError preserves the structured usdb-indexer code and data while exposing
// a stable errors.Is category to consensus callers.
type RPCError struct {
	Code    int
	Message string
	Data    interface{}
	Kind    error
	Cause   error
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("usdb rpc error %d (%s)", e.Code, e.Message)
}

func (e *RPCError) Unwrap() error {
	return e.Cause
}

func (e *RPCError) Is(target error) bool {
	return e.Kind != nil && target == e.Kind
}

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
		return nil, fmt.Errorf("failed to call get_system_state_info: %w", mapRPCError(err))
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
		return nil, fmt.Errorf("failed to call get_pass_economic_profile: %w", mapRPCError(err))
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

func mapRPCError(err error) error {
	var coded gethrpc.Error
	if !errors.As(err, &coded) {
		return err
	}
	mapped := &RPCError{
		Code:    coded.ErrorCode(),
		Message: coded.Error(),
		Kind:    rpcErrorKind(coded.ErrorCode()),
		Cause:   err,
	}
	var dataError gethrpc.DataError
	if errors.As(err, &dataError) {
		mapped.Data = dataError.ErrorData()
	}
	return mapped
}

func rpcErrorKind(code int) error {
	switch code {
	case rpcErrPassNotFound:
		return ErrPassNotFound
	case rpcErrInternalInvariantBroken:
		return ErrInternalInvariantBroken
	case rpcErrHeightNotSynced:
		return ErrHeightNotSynced
	case rpcErrSnapshotNotReady:
		return ErrSnapshotNotReady
	case rpcErrSnapshotIDMismatch:
		return ErrSnapshotIDMismatch
	case rpcErrBlockHashMismatch:
		return ErrBlockHashMismatch
	case rpcErrVersionMismatch:
		return ErrVersionMismatch
	case rpcErrLocalStateCommitMismatch:
		return ErrLocalStateCommitMismatch
	case rpcErrSystemStateIDMismatch:
		return ErrSystemStateIDMismatch
	case rpcErrNoRecord:
		return ErrNoRecord
	case rpcErrStateNotRetained:
		return ErrStateNotRetained
	case rpcErrHistoryNotAvailable:
		return ErrHistoryNotAvailable
	case rpcErrViewVersionMismatch:
		return ErrViewVersionMismatch
	case rpcErrProtocolVersionMismatch:
		return ErrProtocolVersionMismatch
	case rpcErrFormulaVersionMismatch:
		return ErrFormulaVersionMismatch
	default:
		return nil
	}
}
