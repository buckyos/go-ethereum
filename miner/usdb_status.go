package miner

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/internal/usdb"
)

// USDBSealingWork binds the builder decision to work submitted to the engine.
type USDBSealingWork struct {
	Build      usdb.MinerBuildStatus `json:"build"`
	ParentHash common.Hash           `json:"parent_hash"`
	SealHash   common.Hash           `json:"seal_hash"`
}

// USDBWorkStatus separates the last attempt from the last submitted work.
// WorkCurrent is only a local parent/mining check, not a proof of active hashing.
type USDBWorkStatus struct {
	RetryPending  bool                   `json:"retry_pending"`
	SchemaVersion string                 `json:"schema_version"`
	ConfiguredGap uint32                 `json:"configured_gap"`
	Mining        bool                   `json:"mining"`
	HeadHash      common.Hash            `json:"head_hash"`
	Attempt       *usdb.MinerBuildStatus `json:"attempt,omitempty"`
	Work          *USDBSealingWork       `json:"work,omitempty"`
	WorkCurrent   bool                   `json:"work_current"`
}

// USDBWorkStatus returns bounded in-memory diagnostics with no indexer RPC or replay.
func (miner *Miner) USDBWorkStatus() USDBWorkStatus {
	w := miner.worker
	w.mu.RLock()
	gap, builder := w.config.USDB.EffectiveAnchorGap(), w.usdbPayloadBuilder
	w.mu.RUnlock()
	status := USDBWorkStatus{SchemaVersion: "usdb-miner-work:v1", ConfiguredGap: gap, Mining: w.isRunning()}
	status.RetryPending = w.usdbWorkFailure.shouldRetry()
	status.HeadHash = w.chain.CurrentBlock().Hash()
	if source, ok := builder.(interface{ LastBuildStatus() usdb.MinerBuildStatus }); ok {
		attempt := source.LastBuildStatus()
		if !attempt.ObservedAt.IsZero() {
			status.Attempt = &attempt
		}
	}
	w.usdbWorkMu.RLock()
	if w.usdbSealingWork != nil {
		work := *w.usdbSealingWork
		work.Build = work.Build.Copy()
		status.Work = &work
		status.WorkCurrent = status.Mining && work.ParentHash == status.HeadHash
	}
	w.usdbWorkMu.RUnlock()
	return status
}

// recordUSDBSealingWork publishes only successful engine submissions; failures
// clear the previous report because the task loop already interrupted that work.
func (w *worker) recordUSDBSealingWork(block *types.Block, build *usdb.MinerBuildStatus) {
	w.usdbWorkMu.Lock()
	defer w.usdbWorkMu.Unlock()
	w.usdbSealingWork = nil
	if w.isRunning() && block != nil && build != nil && build.Stage == "validated" {
		w.usdbSealingWork = &USDBSealingWork{Build: build.Copy(), ParentHash: block.ParentHash(), SealHash: w.engine.SealHash(block.Header())}
	}
}
