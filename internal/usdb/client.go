package usdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

const (
	// DefaultQueryTimeout bounds one usdb-indexer query.
	DefaultQueryTimeout = 3 * time.Second
	// EconomicStateViewVersionV1 is the frozen UIP-0006 profile response contract.
	EconomicStateViewVersionV1 = "uip-0006-usdb-economic-state-view:v1"
	// MinerCandidateSelectionRuleV1 deterministically selects among passes sharing one usdb_main.
	MinerCandidateSelectionRuleV1 = "uip-0006:effective-energy-desc-pass-id-asc:v1"

	rpcErrPassNotFound             = -32011
	rpcErrInternalInvariantBroken  = -32017
	rpcErrMinerCandidateNotFound   = -32018
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
	rpcErrFormulaVersionMismatch   = -32052
	rpcErrActivationRecordNotFound = -32053
	rpcErrActivationRecordConflict = -32054
	rpcErrVersionNotSupported      = -32055
	rpcErrActiveVersionSetMismatch = -32056
	rpcErrCommitProtocolMismatch   = -32057
)

var (
	ErrPassNotFound             = errors.New("usdb pass not found")
	ErrInternalInvariantBroken  = errors.New("usdb internal invariant broken")
	ErrMinerCandidateNotFound   = errors.New("usdb miner candidate not found")
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
	ErrFormulaVersionMismatch   = errors.New("usdb formula version mismatch")
	ErrActivationRecordNotFound = errors.New("usdb activation record not found")
	ErrActivationRecordConflict = errors.New("usdb activation record conflict")
	ErrVersionNotSupported      = errors.New("usdb version not supported")
	ErrActiveVersionSetMismatch = errors.New("usdb active version set mismatch")
	ErrCommitProtocolMismatch   = errors.New("usdb commit protocol version mismatch")
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
	return fmt.Sprintf("usdb-indexer rpc error %d (%s)", e.Code, e.Message)
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

// QueryExpectedState contains optional UIP-0006 historical-state selectors.
// UIP-0007 commits snapshot/system IDs; callers with a frozen external state may
// additionally pin the activation registry and active version set.
type QueryExpectedState struct {
	SnapshotID           string `json:"snapshot_id,omitempty"`
	ActivationRegistryID string `json:"activation_registry_id,omitempty"`
	ActiveVersionSetID   string `json:"active_version_set_id,omitempty"`
	SystemStateID        string `json:"system_state_id,omitempty"`
}

// SystemStateInfo is the current USDB state needed for miner selector generation.
type SystemStateInfo struct {
	ActivationRegistryID   string           `json:"activation_registry_id"`
	ActiveVersionSet       ActiveVersionSet `json:"active_version_set"`
	ActiveVersionSetID     string           `json:"active_version_set_id"`
	LocalSyncedBlockHeight uint32           `json:"local_synced_block_height"`
	UpstreamSnapshotID     string           `json:"upstream_snapshot_id"`
	SystemStateID          string           `json:"system_state_id"`
}

// EconomicExternalState is the exact historical state identity returned by UIP-0006.
type EconomicExternalState struct {
	BTCHeight                      uint32           `json:"btc_height"`
	SnapshotID                     string           `json:"snapshot_id"`
	StableBlockHash                string           `json:"stable_block_hash"`
	StableLag                      uint32           `json:"stable_lag"`
	LocalStateCommit               string           `json:"local_state_commit"`
	SystemStateID                  string           `json:"system_state_id"`
	BalanceHistoryAPIVersion       string           `json:"balance_history_api_version"`
	BalanceHistorySemanticsVersion string           `json:"balance_history_semantics_version"`
	ActivationRegistryID           string           `json:"activation_registry_id"`
	ActiveVersionSet               ActiveVersionSet `json:"active_version_set"`
	ActiveVersionSetID             string           `json:"active_version_set_id"`
}

// PassEconomicProfile is one pass and its UIP-0003 through UIP-0005 derived fields.
type PassEconomicProfile struct {
	PassID               string  `json:"pass_id"`
	OwnerScriptHash      string  `json:"owner_script_hash"`
	OwnerBTCAddress      *string `json:"owner_btc_addr"`
	State                string  `json:"state"`
	PassKind             string  `json:"pass_kind"`
	USDBMain             *string `json:"usdb_main"`
	RawEnergy            string  `json:"raw_energy"`
	CollabContribution   string  `json:"collab_contribution"`
	EffectiveEnergy      string  `json:"effective_energy"`
	Level                uint8   `json:"level"`
	DifficultyFactorBps  uint64  `json:"difficulty_factor_bps"`
	CollabBreakdownCount uint64  `json:"collab_breakdown_count"`
}

// MinerEconomicAggregate is the selector-bound BTC asset input for UIP-0011.
type MinerEconomicAggregate struct {
	TotalMinerBTCSats     string `json:"total_miner_btc_sats"`
	ActiveMinerOwnerCount uint64 `json:"active_miner_owner_count"`
}

// PassEconomicProfileView is the frozen UIP-0006 response consumed by USDB-chain consensus.
type PassEconomicProfileView struct {
	ViewVersion    string                 `json:"view_version"`
	ExternalState  EconomicExternalState  `json:"external_state"`
	Pass           PassEconomicProfile    `json:"pass"`
	MinerAggregate MinerEconomicAggregate `json:"miner_aggregate"`
}

// MinerCandidateProfileView is one atomically selected pass profile for a stable usdb_main.
type MinerCandidateProfileView struct {
	ViewVersion            string                 `json:"view_version"`
	ExternalState          EconomicExternalState  `json:"external_state"`
	SelectionRule          string                 `json:"selection_rule"`
	MatchingCandidateCount uint64                 `json:"matching_candidate_count"`
	Pass                   PassEconomicProfile    `json:"pass"`
	MinerAggregate         MinerEconomicAggregate `json:"miner_aggregate"`
}

type passEconomicProfileParams struct {
	ViewVersion string       `json:"view_version"`
	PassID      string       `json:"pass_id"`
	BlockHeight uint32       `json:"block_height"`
	Context     QueryContext `json:"context"`
}

type resolveMinerCandidateParams struct {
	ViewVersion string       `json:"view_version"`
	USDBMain    string       `json:"usdb_main"`
	BlockHeight uint32       `json:"block_height"`
	Context     QueryContext `json:"context"`
}

// Client is the minimal usdb-indexer RPC surface needed to build and resolve selectors.
type Client interface {
	GetSystemStateInfo(ctx context.Context) (*SystemStateInfo, error)
	GetPassEconomicProfile(ctx context.Context, passID PassID, query QueryContext) (*PassEconomicProfileView, error)
	ResolveMinerCandidate(ctx context.Context, usdbMain common.Address, query QueryContext) (*MinerCandidateProfileView, error)
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

// DialRPC establishes a reusable client to one usdb-indexer RPC endpoint.
func DialRPC(ctx context.Context, endpoint string) (*RPCClient, error) {
	client, err := gethrpc.DialContext(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to dial usdb-indexer rpc %q: %w", endpoint, err)
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

// ResolveMinerCandidate atomically selects one active standard pass and profile by usdb_main.
func (c *RPCClient) ResolveMinerCandidate(ctx context.Context, usdbMain common.Address, query QueryContext) (*MinerCandidateProfileView, error) {
	var raw json.RawMessage
	params := resolveMinerCandidateParams{
		ViewVersion: EconomicStateViewVersionV1,
		USDBMain:    usdbMain.Hex(),
		BlockHeight: query.RequestedHeight,
		Context:     query,
	}
	if err := c.client.CallContext(ctx, &raw, "resolve_miner_candidate", params); err != nil {
		return nil, fmt.Errorf("failed to call resolve_miner_candidate: %w", mapRPCError(err))
	}
	if isNullJSON(raw) {
		return nil, nil
	}
	var candidate MinerCandidateProfileView
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return nil, fmt.Errorf("failed to decode resolve_miner_candidate result: %w", err)
	}
	return &candidate, nil
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
	case rpcErrMinerCandidateNotFound:
		return ErrMinerCandidateNotFound
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
	case rpcErrFormulaVersionMismatch:
		return ErrFormulaVersionMismatch
	case rpcErrActivationRecordNotFound:
		return ErrActivationRecordNotFound
	case rpcErrActivationRecordConflict:
		return ErrActivationRecordConflict
	case rpcErrVersionNotSupported:
		return ErrVersionNotSupported
	case rpcErrActiveVersionSetMismatch:
		return ErrActiveVersionSetMismatch
	case rpcErrCommitProtocolMismatch:
		return ErrCommitProtocolMismatch
	default:
		return nil
	}
}
