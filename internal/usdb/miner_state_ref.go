package usdb

import "fmt"

// validateMinerStateRef rejects mixed heights, identities and rule sets before
// querying a pass. Service-side context validation is also required, but a local
// malformed response must never silently redirect the miner to another state.
func validateMinerStateRef(registry *btcActivationRegistry, query QueryContext, ref *HistoricalStateRefInfo) error {
	if ref == nil {
		return fmt.Errorf("missing state ref: requested_height=%d", query.RequestedHeight)
	}
	h := query.RequestedHeight
	snapshot, local, system := ref.SnapshotInfo, ref.LocalStateCommitInfo, ref.SystemStateInfo
	if ref.BlockHeight != h || snapshot.LocalSyncedBlockHeight != h || snapshot.BalanceHistoryStableHeight != h || local.LocalSyncedBlockHeight != h || system.LocalSyncedBlockHeight != h {
		return fmt.Errorf("state ref height mismatch: requested=%d ref=%d snapshot_local=%d snapshot_stable=%d local=%d system=%d", h, ref.BlockHeight, snapshot.LocalSyncedBlockHeight, snapshot.BalanceHistoryStableHeight, local.LocalSyncedBlockHeight, system.LocalSyncedBlockHeight)
	}
	for _, identity := range []struct{ name, value string }{
		{"snapshot_id", snapshot.SnapshotID}, {"stable_block_hash", snapshot.StableBlockHash},
		{"local_state_commit", local.LocalStateCommit}, {"system_state_id", system.SystemStateID},
	} {
		if _, err := parseCanonicalHex32(identity.name, identity.value); err != nil {
			return err
		}
	}
	if snapshot.SnapshotID != local.UpstreamSnapshotID || snapshot.SnapshotID != system.UpstreamSnapshotID || local.LocalStateCommit != system.LocalStateCommit {
		return fmt.Errorf("state ref identity mismatch: height=%d snapshot=%s local_snapshot=%s system_snapshot=%s local_commit=%s system_commit=%s", h, snapshot.SnapshotID, local.UpstreamSnapshotID, system.UpstreamSnapshotID, local.LocalStateCommit, system.LocalStateCommit)
	}
	if query.ExpectedState.SnapshotID != "" && query.ExpectedState.SnapshotID != system.UpstreamSnapshotID ||
		query.ExpectedState.SystemStateID != "" && query.ExpectedState.SystemStateID != system.SystemStateID {
		return fmt.Errorf("%w: height=%d expected_snapshot=%s actual_snapshot=%s expected_system=%s actual_system=%s", ErrBTCAnchorIdentityMismatch, h, query.ExpectedState.SnapshotID, system.UpstreamSnapshotID, query.ExpectedState.SystemStateID, system.SystemStateID)
	}
	if _, err := registry.validateIdentity(h, local.ActivationRegistryID, local.ActiveVersionSet, local.ActiveVersionSetID); err != nil {
		return fmt.Errorf("historical local activation mismatch: height=%d: %w", h, err)
	}
	if _, err := registry.validateIdentity(h, system.ActivationRegistryID, system.ActiveVersionSet, system.ActiveVersionSetID); err != nil {
		return fmt.Errorf("historical system activation mismatch: height=%d: %w", h, err)
	}
	return nil
}
