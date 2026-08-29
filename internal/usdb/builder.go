package usdb

import (
	"context"
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
}

// PayloadBuilder creates the current UIP-0007 selector used by miner/header assembly.
type PayloadBuilder struct {
	client       Client
	chainConfig  *params.ChainConfig
	queryTimeout time.Duration

	stateMu           sync.RWMutex
	lastSystemStateID string
}

// NewPayloadBuilder constructs a builder from an already-configured USDB client.
func NewPayloadBuilder(client Client, chainConfig *params.ChainConfig, queryTimeout time.Duration) (*PayloadBuilder, error) {
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
	}, nil
}

// NewRPCPayloadBuilder dials one usdb-indexer endpoint and uses it to generate current selectors.
func NewRPCPayloadBuilder(endpoint string, chainConfig *params.ChainConfig, queryTimeout time.Duration) (*PayloadBuilder, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultQueryTimeout)
	defer cancel()

	client, err := DialRPC(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	builder, err := NewPayloadBuilder(client, chainConfig, queryTimeout)
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
	lastSystemStateID := b.lastSystemStateID
	b.stateMu.RUnlock()
	return lastSystemStateID == "" || systemState.SystemStateID != lastSystemStateID, nil
}

// BuildCurrentPayload emits a selector for blockNumber only after resolving its
// consensus policy, resolving a pass for usdbMain, deriving its age from
// parentExtra, and validating the selected profile in current state.
func (b *PayloadBuilder) BuildCurrentPayload(ctx context.Context, blockNumber uint64, parentExtra []byte, usdbMain common.Address) (*BuiltProfileSelector, error) {
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

	systemState, err := b.client.GetSystemStateInfo(queryCtx)
	if err != nil {
		return nil, err
	}
	if systemState == nil {
		return nil, fmt.Errorf("usdb returned no current system state")
	}
	if err := validateCurrentActivationIdentity(
		systemState.LocalSyncedBlockHeight,
		systemState.ActiveVersionSet,
		systemState.ActiveVersionSetID,
		systemState.ActivationRegistryID,
		btcRegistry,
	); err != nil {
		return nil, fmt.Errorf("invalid current usdb activation identity: %w", err)
	}
	expectedActivation, err := btcRegistry.lookup(systemState.LocalSyncedBlockHeight)
	if err != nil {
		return nil, err
	}
	query := QueryContext{
		RequestedHeight: systemState.LocalSyncedBlockHeight,
		ExpectedState: QueryExpectedState{
			SnapshotID:           systemState.UpstreamSnapshotID,
			ActivationRegistryID: btcRegistry.ActivationRegistryID,
			ActiveVersionSetID:   expectedActivation.ActiveVersionSetID,
			SystemStateID:        systemState.SystemStateID,
		},
	}
	candidate, err := b.client.ResolveMinerCandidate(queryCtx, usdbMain, query)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve miner candidate for usdb_main %s: %w", usdbMain, err)
	}
	if candidate == nil {
		return nil, fmt.Errorf("%w: usdb_main %s", ErrMinerCandidateNotFound, usdbMain)
	}
	if candidate.SelectionRule != MinerCandidateSelectionRuleV1 || candidate.MatchingCandidateCount == 0 {
		return nil, fmt.Errorf(
			"invalid miner candidate selection metadata: rule=%q matching_candidate_count=%d",
			candidate.SelectionRule,
			candidate.MatchingCandidateCount,
		)
	}
	payload, err := NewProfileSelectorPayload(
		policy.DifficultyPolicyVersion,
		systemState.LocalSyncedBlockHeight,
		0,
		systemState.UpstreamSnapshotID,
		systemState.SystemStateID,
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
		return nil, fmt.Errorf("resolved pass %s is not valid in current usdb state: %w", candidate.Pass.PassID, err)
	}
	if profile.View.ExternalState.ActiveVersionSetID != systemState.ActiveVersionSetID {
		return nil, fmt.Errorf("current usdb activation identity changed while building payload")
	}
	if profile.RewardRecipient != usdbMain {
		return nil, fmt.Errorf(
			"resolved pass reward recipient %s does not match configured usdb_main %s",
			profile.RewardRecipient,
			usdbMain,
		)
	}
	encoded, err := payload.MarshalBinary()
	if err != nil {
		return nil, err
	}
	b.stateMu.Lock()
	b.lastSystemStateID = systemState.SystemStateID
	b.stateMu.Unlock()
	return &BuiltProfileSelector{
		Payload:         encoded,
		RewardRecipient: profile.RewardRecipient,
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
