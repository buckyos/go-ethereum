package usdb

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

// BuiltProfileSelector is the validated miner-side output for one USDB block.
type BuiltProfileSelector struct {
	Payload         []byte
	RewardRecipient common.Address
	AnchorSelection MinerAnchorSelection
	Diagnostics     MinerBuildStatus
}

// PayloadBuilder creates the current UIP-0007 selector used by miner/header assembly.
type PayloadBuilder struct {
	client       Client
	chainConfig  *params.ChainConfig
	queryTimeout time.Duration
	anchorGap    uint32

	stateMu           sync.RWMutex
	lastObservedState observedSystemState
	lastBuildStatus   MinerBuildStatus
}

// observedSystemState tracks the head observed by a successful build separately
// from its selected historical state. A pinned history must not trigger a loop.
type observedSystemState struct {
	height                                         uint32
	snapshotID, systemID, registryID, versionSetID string
}

func observedState(info *SystemStateInfo) observedSystemState {
	return observedSystemState{info.LocalSyncedBlockHeight, info.UpstreamSnapshotID, info.SystemStateID, info.ActivationRegistryID, info.ActiveVersionSetID}
}

// NewPayloadBuilder constructs a builder from an already-configured USDB client.
func NewPayloadBuilder(client Client, chainConfig *params.ChainConfig, queryTimeout time.Duration, anchorGap uint32) (*PayloadBuilder, error) {
	if client == nil {
		return nil, fmt.Errorf("nil usdb client")
	}
	if chainConfig == nil {
		return nil, fmt.Errorf("nil chain config")
	}
	if !chainConfig.HasUSDBConsensus() {
		return nil, fmt.Errorf("chain config has no usdb consensus configuration")
	}
	return &PayloadBuilder{
		client:       client,
		chainConfig:  chainConfig,
		queryTimeout: EffectiveQueryTimeout(queryTimeout),
		anchorGap:    anchorGap,
	}, nil
}

// NewRPCPayloadBuilder dials one usdb-indexer endpoint and uses it to generate current selectors.
func NewRPCPayloadBuilder(endpoint string, chainConfig *params.ChainConfig, queryTimeout time.Duration, anchorGap uint32) (*PayloadBuilder, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultQueryTimeout)
	defer cancel()

	client, err := DialRPC(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	builder, err := NewPayloadBuilder(client, chainConfig, queryTimeout, anchorGap)
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

// HasSystemStateChanged performs the lightweight half of work refresh. The
// cached identity advances only after BuildCurrentPayload validates and builds
// a complete selector, so a failed rebuild cannot hide an external-state
// transition from later polls.
func (b *PayloadBuilder) HasSystemStateChanged(ctx context.Context) (bool, error) {
	queryCtx, cancel := context.WithTimeout(ctx, b.queryTimeout)
	defer cancel()

	systemState, err := b.client.GetSystemStateInfo(queryCtx)
	if err != nil {
		return false, err
	}
	if systemState == nil {
		return false, fmt.Errorf("usdb returned no current system state")
	}
	if systemState.SystemStateID == "" {
		return false, fmt.Errorf("usdb returned an empty system_state_id")
	}
	b.stateMu.RLock()
	lastObservedState := b.lastObservedState
	b.stateMu.RUnlock()
	return lastObservedState.systemID == "" || observedState(systemState) != lastObservedState, nil
}

// BuildCurrentPayload emits a selector for blockNumber only after resolving its
// consensus policy, resolving a pass for usdbMain, deriving its age from
// parentExtra, and validating the profile at the locally selected historical height.
func (b *PayloadBuilder) BuildCurrentPayload(ctx context.Context, blockNumber uint64, parentExtra []byte, usdbMain common.Address) (built *BuiltProfileSelector, buildErr error) {
	diagnostic := MinerBuildStatus{BlockNumber: blockNumber, MinerAddress: usdbMain, Stage: "policy"}
	defer func() {
		diagnostic.ObservedAt = time.Now().UTC()
		if buildErr != nil {
			diagnostic.Failure = minerBuildFailure(buildErr)
		}
		if built != nil {
			built.Diagnostics = diagnostic.Copy()
		}
		b.stateMu.Lock()
		b.lastBuildStatus = diagnostic.Copy()
		b.stateMu.Unlock()
	}()
	activation, err := b.chainConfig.USDBActivationAt(blockNumber)
	if err != nil {
		return nil, err
	}
	if activation == nil {
		return nil, fmt.Errorf("usdb consensus is not active at block %d", blockNumber)
	}
	if usdbMain == (common.Address{}) {
		return nil, fmt.Errorf("usdb miner address is not configured")
	}
	diagnostic.MaxAnchorAge = activation.BTCAnchorMaxAgeBlocks
	policy := &activation.Versions
	if policy.PayloadVersion != ProfileSelectorPayloadVersionV1 {
		return nil, fmt.Errorf("%w: chain config expects %d, builder supports %d", ErrProfileSelectorVersion, policy.PayloadVersion, ProfileSelectorPayloadVersionV1)
	}
	parentSelector, err := b.parentSelector(blockNumber, parentExtra)
	if err != nil {
		return nil, err
	}
	btcRegistry, err := loadBTCActivationRegistry(activation.BTCActivationRegistryID)
	if err != nil {
		return nil, fmt.Errorf("invalid chain-config BTC activation registry at block %d: %w", blockNumber, err)
	}

	queryCtx, cancel := context.WithTimeout(ctx, b.queryTimeout)
	defer cancel()

	diagnostic.Stage = "latest_state"
	systemState, err := b.client.GetSystemStateInfo(queryCtx)
	if err != nil {
		return nil, err
	}
	if systemState == nil {
		return nil, fmt.Errorf("usdb returned no current system state")
	}
	latest := systemState.LocalSyncedBlockHeight
	diagnostic.LatestHeight = &latest
	observed := observedState(systemState)
	if _, err := parseCanonicalHex32("current system_state_id", systemState.SystemStateID); err != nil {
		return nil, err
	}
	if _, err := parseCanonicalHex32("current snapshot_id", systemState.UpstreamSnapshotID); err != nil {
		return nil, err
	}
	if err := validateCurrentActivationIdentity(
		b.chainConfig.USDB.BTCIndexOriginHeight,
		systemState.LocalSyncedBlockHeight,
		systemState.ActiveVersionSet,
		systemState.ActiveVersionSetID,
		systemState.ActivationRegistryID,
		btcRegistry,
	); err != nil {
		return nil, fmt.Errorf("invalid current usdb activation identity: %w", err)
	}
	diagnostic.Stage = "select_height"
	selection, err := SelectMinerAnchorHeight(systemState.LocalSyncedBlockHeight,
		b.chainConfig.USDB.BTCIndexOriginHeight, b.anchorGap, parentSelector,
		policy.BTCAnchorPolicyVersion, activation.BTCAnchorMaxAgeBlocks)
	if err != nil {
		if errors.Is(err, ErrBTCAnchorHeightRegression) && parentSelector != nil {
			required := parentSelector.BTCHeight
			diagnostic.RequiredHeight = &required
		} else if errors.Is(err, ErrBTCAnchorAgeExceeded) && parentSelector != nil && parentSelector.BTCHeight < ^uint32(0) {
			required := parentSelector.BTCHeight + 1
			diagnostic.RequiredHeight = &required
		} else if errors.Is(err, ErrStateNotRetained) && systemState.LocalSyncedBlockHeight < b.chainConfig.USDB.BTCIndexOriginHeight {
			required := b.chainConfig.USDB.BTCIndexOriginHeight
			diagnostic.RequiredHeight = &required
		}
		return nil, fmt.Errorf("cannot select miner BTC anchor: block=%d latest_height=%d gap=%d: %w", blockNumber, systemState.LocalSyncedBlockHeight, b.anchorGap, err)
	}
	diagnostic.Selection = &selection
	expectedActivation, err := btcRegistry.lookup(selection.SelectedHeight)
	if err != nil {
		return nil, err
	}
	query := QueryContext{
		RequestedHeight: selection.SelectedHeight,
		ExpectedState: QueryExpectedState{
			ActivationRegistryID: btcRegistry.ActivationRegistryID,
			ActiveVersionSetID:   expectedActivation.ActiveVersionSetID,
		},
	}
	if selection.SelectedHeight == systemState.LocalSyncedBlockHeight {
		query.ExpectedState.SnapshotID = systemState.UpstreamSnapshotID
		query.ExpectedState.SystemStateID = systemState.SystemStateID
	}
	if parentSelector != nil && selection.SelectedHeight == parentSelector.BTCHeight {
		query.ExpectedState.SnapshotID = parentSelector.SnapshotIDHex()
		query.ExpectedState.SystemStateID = parentSelector.SystemStateIDHex()
		if selection.SelectedHeight == systemState.LocalSyncedBlockHeight &&
			(query.ExpectedState.SnapshotID != systemState.UpstreamSnapshotID || query.ExpectedState.SystemStateID != systemState.SystemStateID) {
			return nil, fmt.Errorf("%w: height=%d current_snapshot=%s parent_snapshot=%s current_system=%s parent_system=%s", ErrBTCAnchorIdentityMismatch,
				selection.SelectedHeight, systemState.UpstreamSnapshotID, query.ExpectedState.SnapshotID, systemState.SystemStateID, query.ExpectedState.SystemStateID)
		}
	}
	diagnostic.Stage = "historical_state"
	stateRef, err := b.client.GetStateRefAtHeight(queryCtx, query)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve miner historical state: block=%d latest_height=%d selected_height=%d gap=%d: %w", blockNumber, selection.LatestHeight, selection.SelectedHeight, b.anchorGap, err)
	}
	if err := validateMinerStateRef(btcRegistry, query, stateRef); err != nil {
		return nil, fmt.Errorf("invalid miner historical state: block=%d selected_height=%d: %w", blockNumber, selection.SelectedHeight, err)
	}
	selectedState := &stateRef.SystemStateInfo
	query.ExpectedState.SnapshotID = selectedState.UpstreamSnapshotID
	query.ExpectedState.SystemStateID = selectedState.SystemStateID
	query.ExpectedState.LocalStateCommit = selectedState.LocalStateCommit
	query.ExpectedState.StableBlockHash = stateRef.SnapshotInfo.StableBlockHash
	diagnostic.SnapshotID, diagnostic.SystemStateID = selectedState.UpstreamSnapshotID, selectedState.SystemStateID
	diagnostic.Stage = "candidate"
	candidate, err := b.client.ResolveMinerCandidate(queryCtx, usdbMain, query)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve miner candidate: usdb_main=%s selected_height=%d latest_height=%d gap=%d: %w", usdbMain, selection.SelectedHeight, selection.LatestHeight, b.anchorGap, err)
	}
	if candidate == nil {
		return nil, fmt.Errorf("%w: usdb_main=%s selected_height=%d latest_height=%d gap=%d", ErrMinerCandidateNotFound, usdbMain, selection.SelectedHeight, selection.LatestHeight, b.anchorGap)
	}
	if candidate.SelectionRule != MinerCandidateSelectionRuleV1 || candidate.MatchingCandidateCount == 0 {
		return nil, fmt.Errorf(
			"invalid miner candidate selection metadata: rule=%q matching_candidate_count=%d",
			candidate.SelectionRule,
			candidate.MatchingCandidateCount,
		)
	}
	diagnostic.Stage = "validate_profile"
	payload, err := NewProfileSelectorPayload(
		policy.DifficultyPolicyVersion,
		selection.SelectedHeight,
		0,
		selectedState.UpstreamSnapshotID,
		selectedState.SystemStateID,
		candidate.Pass.PassID,
	)
	if err != nil {
		return nil, err
	}
	payload.BTCAnchorAgeBlocks, err = ExpectedBTCAnchorAgeBlocks(
		parentSelector,
		*payload,
		policy.BTCAnchorPolicyVersion,
		activation.BTCAnchorMaxAgeBlocks,
	)
	if err != nil {
		return nil, fmt.Errorf("cannot extend parent BTC anchor: %w", err)
	}
	view := &PassEconomicProfileView{
		ViewVersion:    candidate.ViewVersion,
		ExternalState:  candidate.ExternalState,
		Pass:           candidate.Pass,
		MinerAggregate: candidate.MinerAggregate,
	}
	profile, err := resolveConsensusProfileView(btcRegistry, *payload, view)
	if err != nil {
		return nil, fmt.Errorf("resolved pass %s is not valid at selected_height=%d: %w", candidate.Pass.PassID, selection.SelectedHeight, err)
	}
	external := profile.View.ExternalState
	if external.ActiveVersionSetID != selectedState.ActiveVersionSetID ||
		external.LocalStateCommit != selectedState.LocalStateCommit || external.StableBlockHash != stateRef.SnapshotInfo.StableBlockHash {
		return nil, fmt.Errorf("historical state changed while building payload: height=%d expected_version=%s actual_version=%s expected_commit=%s actual_commit=%s expected_block=%s actual_block=%s",
			selection.SelectedHeight, selectedState.ActiveVersionSetID, external.ActiveVersionSetID, selectedState.LocalStateCommit, external.LocalStateCommit, stateRef.SnapshotInfo.StableBlockHash, external.StableBlockHash)
	}
	if profile.RewardRecipient != usdbMain {
		return nil, fmt.Errorf(
			"resolved pass reward recipient %s does not match configured usdb_main %s",
			profile.RewardRecipient,
			usdbMain,
		)
	}
	diagnostic.PassID = candidate.Pass.PassID
	diagnostic.AnchorAge = payload.BTCAnchorAgeBlocks
	encoded, err := payload.MarshalBinary()
	if err != nil {
		return nil, err
	}
	b.stateMu.Lock()
	b.lastObservedState = observed
	diagnostic.Stage = "validated"
	b.stateMu.Unlock()
	return &BuiltProfileSelector{
		Payload:         encoded,
		RewardRecipient: profile.RewardRecipient,
		AnchorSelection: selection,
	}, nil
}

func (b *PayloadBuilder) parentSelector(blockNumber uint64, parentExtra []byte) (*ProfileSelectorPayload, error) {
	if blockNumber <= 1 {
		return nil, nil
	}
	parentActivation, err := b.chainConfig.USDBActivationAt(blockNumber - 1)
	if err != nil {
		return nil, err
	}
	if parentActivation == nil {
		return nil, nil
	}
	parentPolicy := &parentActivation.Versions
	if err := ValidateProfileSelectorPayload(
		parentExtra,
		parentPolicy.PayloadVersion,
		parentPolicy.DifficultyPolicyVersion,
	); err != nil {
		return nil, fmt.Errorf("invalid parent usdb profile selector: %w", err)
	}
	var parent ProfileSelectorPayload
	if err := parent.UnmarshalBinary(parentExtra); err != nil {
		return nil, fmt.Errorf("decode parent usdb profile selector: %w", err)
	}
	return &parent, nil
}

func validateCurrentActivationIdentity(
	origin uint32,
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
	// Equal formula versions at one height do not make independent rule
	// histories interchangeable. Revision compatibility is only meaningful
	// within the same BTC source and USDB rules scope.
	if actualRegistry.NetworkID != expectedRegistry.NetworkID || actualRegistry.RulesScope != expectedRegistry.RulesScope {
		return fmt.Errorf("%w: service scope %s/%s differs from chain-config scope %s/%s", ErrBTCActivationRegistryMismatch,
			actualRegistry.NetworkID, actualRegistry.RulesScope, expectedRegistry.NetworkID, expectedRegistry.RulesScope)
	}
	if err := actualRegistry.ensureSameHistory(expectedRegistry, origin, btcHeight); err != nil {
		return err
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
