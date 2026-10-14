package usdb

import (
	"errors"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// MinerBuildStatus describes the last completed attempt, not necessarily sealing work.
// Errors are classified without exposing RPC endpoints or upstream response bodies.
type MinerBuildStatus struct {
	ObservedAt     time.Time             `json:"observed_at"`
	BlockNumber    uint64                `json:"block_number"`
	MinerAddress   common.Address        `json:"miner_address"`
	Stage          string                `json:"stage"`
	Failure        string                `json:"failure,omitempty"`
	LatestHeight   *uint32               `json:"latest_height,omitempty"`
	Selection      *MinerAnchorSelection `json:"selection,omitempty"`
	RequiredHeight *uint32               `json:"required_height,omitempty"`
	PassID         string                `json:"pass_id,omitempty"`
	AnchorAge      uint32                `json:"anchor_age"`
	MaxAnchorAge   uint32                `json:"max_anchor_age"`
	SnapshotID     string                `json:"snapshot_id,omitempty"`
	SystemStateID  string                `json:"system_state_id,omitempty"`
}

// Copy prevents callers from mutating the builder's published snapshot.
func (s MinerBuildStatus) Copy() MinerBuildStatus {
	if s.LatestHeight != nil {
		v := *s.LatestHeight
		s.LatestHeight = &v
	}
	if s.RequiredHeight != nil {
		v := *s.RequiredHeight
		s.RequiredHeight = &v
	}
	if s.Selection != nil {
		v := *s.Selection
		s.Selection = &v
	}
	return s
}

// LastBuildStatus returns an observation only; it never initiates an upstream query.
func (b *PayloadBuilder) LastBuildStatus() MinerBuildStatus {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.lastBuildStatus.Copy()
}

func minerBuildFailure(err error) string {
	switch {
	case errors.Is(err, ErrMinerCandidateNotFound):
		return "candidate_unavailable"
	case errors.Is(err, ErrBTCAnchorAgeExceeded):
		return "anchor_age_exhausted"
	case errors.Is(err, ErrBTCAnchorHeightRegression):
		return "parent_state_unavailable"
	case errors.Is(err, ErrStateNotRetained):
		return "state_not_retained"
	case errors.Is(err, ErrSnapshotNotReady):
		return "state_not_ready"
	case errors.Is(err, ErrBTCAnchorIdentityMismatch):
		return "anchor_identity_mismatch"
	default:
		return "build_failed"
	}
}
