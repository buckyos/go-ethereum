package usdb

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/params"
)

const (
	passStateActive  = "active"
	passKindStandard = "standard"
)

var (
	// ErrProfileNotFound indicates that the selected pass has no UIP-0006 profile.
	ErrProfileNotFound = errors.New("usdb economic profile not found")
	// ErrProfileStateMismatch indicates that returned history does not match header selectors.
	ErrProfileStateMismatch = errors.New("usdb economic profile state mismatch")
	// ErrSelectedPassNotCandidate indicates that the selected pass is not Active and Standard.
	ErrSelectedPassNotCandidate = errors.New("selected pass is not a usdb candidate")
	// ErrProfileDerivedValueMismatch indicates invalid derived energy, level, or factor fields.
	ErrProfileDerivedValueMismatch = errors.New("usdb economic profile derived value mismatch")
)

// ResolvedConsensusProfile is a selector-bound and locally verified UIP-0006 profile.
type ResolvedConsensusProfile struct {
	Selector            ProfileSelectorPayload
	View                PassEconomicProfileView
	RawEnergy           *big.Int
	CollabContribution  *big.Int
	EffectiveEnergy     *big.Int
	Level               uint8
	DifficultyFactorBps uint64
}

// Verifier resolves historical consensus profiles from the usdb-indexer RPC surface.
type Verifier struct {
	client       Client
	queryTimeout time.Duration
}

// NewVerifier constructs a verifier from an already-configured USDB client.
func NewVerifier(client Client, queryTimeout time.Duration) (*Verifier, error) {
	if client == nil {
		return nil, fmt.Errorf("nil usdb client")
	}
	if queryTimeout <= 0 {
		queryTimeout = DefaultQueryTimeout
	}
	return &Verifier{
		client:       client,
		queryTimeout: queryTimeout,
	}, nil
}

// NewRPCVerifier dials one usdb-indexer endpoint and uses it to resolve consensus profiles.
func NewRPCVerifier(endpoint string, chainConfig *params.ChainConfig, queryTimeout time.Duration) (*Verifier, error) {
	if chainConfig == nil || !chainConfig.HasUSDBConsensus() {
		return nil, fmt.Errorf("chain config has no usdb consensus configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultQueryTimeout)
	defer cancel()

	client, err := DialRPC(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	verifier, err := NewVerifier(client, queryTimeout)
	if err != nil {
		client.Close()
		return nil, err
	}
	return verifier, nil
}

// Close releases any verifier-owned RPC connection.
func (v *Verifier) Close() {
	if v != nil && v.client != nil {
		v.client.Close()
	}
}

// ResolveProfile decodes header.Extra and validates its historical UIP-0006
// profile against the BTC registry revision selected by the USDB block.
func (v *Verifier) ResolveProfile(ctx context.Context, btcActivationRegistryID string, headerExtra []byte) (*ResolvedConsensusProfile, error) {
	if len(headerExtra) == 0 {
		return nil, ErrMissingProfileSelector
	}
	btcRegistry, err := loadBTCActivationRegistry(btcActivationRegistryID)
	if err != nil {
		return nil, err
	}
	var selector ProfileSelectorPayload
	if err := selector.UnmarshalBinary(headerExtra); err != nil {
		return nil, err
	}

	queryCtx, cancel := context.WithTimeout(ctx, v.queryTimeout)
	defer cancel()
	return resolveConsensusProfile(queryCtx, v.client, btcRegistry, selector)
}

func resolveConsensusProfile(ctx context.Context, client Client, btcRegistry *btcActivationRegistry, selector ProfileSelectorPayload) (*ResolvedConsensusProfile, error) {
	expectedActivation, err := btcRegistry.lookup(selector.BTCHeight)
	if err != nil {
		return nil, err
	}
	query := QueryContext{
		RequestedHeight: selector.BTCHeight,
		ExpectedState: QueryExpectedState{
			SnapshotID:           selector.SnapshotIDHex(),
			ActivationRegistryID: btcRegistry.ActivationRegistryID,
			ActiveVersionSetID:   expectedActivation.ActiveVersionSetID,
			SystemStateID:        selector.SystemStateIDHex(),
		},
	}
	view, err := client.GetPassEconomicProfile(ctx, selector.PassID, query)
	if err != nil {
		return nil, err
	}
	if view == nil {
		return nil, fmt.Errorf("%w: pass %s", ErrProfileNotFound, selector.PassID.String())
	}
	activation, err := validateProfileIdentity(selector, view, btcRegistry)
	if err != nil {
		return nil, err
	}
	if view.Pass.State != passStateActive || view.Pass.PassKind != passKindStandard {
		return nil, fmt.Errorf(
			"%w: pass %s has state=%q pass_kind=%q",
			ErrSelectedPassNotCandidate,
			selector.PassID.String(),
			view.Pass.State,
			view.Pass.PassKind,
		)
	}

	rawEnergy, collabContribution, effectiveEnergy, level, difficultyFactorBps, err :=
		resolveProfileFormulaValues(activation.ActiveVersionSet, view.Pass)
	if err != nil {
		return nil, err
	}

	return &ResolvedConsensusProfile{
		Selector:            selector,
		View:                *view,
		RawEnergy:           rawEnergy,
		CollabContribution:  collabContribution,
		EffectiveEnergy:     effectiveEnergy,
		Level:               level,
		DifficultyFactorBps: difficultyFactorBps,
	}, nil
}

func validateProfileIdentity(selector ProfileSelectorPayload, view *PassEconomicProfileView, btcRegistry *btcActivationRegistry) (*btcActivationPoint, error) {
	if view.ViewVersion != EconomicStateViewVersionV1 {
		return nil, fmt.Errorf(
			"%w: view_version have %q want %q",
			ErrProfileStateMismatch,
			view.ViewVersion,
			EconomicStateViewVersionV1,
		)
	}
	state := view.ExternalState
	if state.BTCHeight != selector.BTCHeight {
		return nil, fmt.Errorf("%w: btc_height have %d want %d", ErrProfileStateMismatch, state.BTCHeight, selector.BTCHeight)
	}
	if state.SnapshotID != selector.SnapshotIDHex() {
		return nil, fmt.Errorf("%w: snapshot_id have %q want %q", ErrProfileStateMismatch, state.SnapshotID, selector.SnapshotIDHex())
	}
	if state.SystemStateID != selector.SystemStateIDHex() {
		return nil, fmt.Errorf("%w: system_state_id have %q want %q", ErrProfileStateMismatch, state.SystemStateID, selector.SystemStateIDHex())
	}
	if view.Pass.PassID != selector.PassID.String() {
		return nil, fmt.Errorf("%w: pass_id have %q want %q", ErrProfileStateMismatch, view.Pass.PassID, selector.PassID.String())
	}
	if _, err := ParsePassID(view.Pass.PassID); err != nil {
		return nil, fmt.Errorf("%w: response pass_id is not canonical: %v", ErrProfileStateMismatch, err)
	}
	if state.StableBlockHash == "" || state.LocalStateCommit == "" ||
		state.BalanceHistoryAPIVersion == "" || state.BalanceHistorySemanticsVersion == "" ||
		state.ActivationRegistryID == "" || len(state.ActiveVersionSet) == 0 ||
		state.ActiveVersionSetID == "" {
		return nil, fmt.Errorf("%w: external_state is incomplete", ErrProfileStateMismatch)
	}
	activation, err := btcRegistry.validateIdentity(
		selector.BTCHeight,
		state.ActivationRegistryID,
		state.ActiveVersionSet,
		state.ActiveVersionSetID,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProfileStateMismatch, err)
	}
	if _, err := parseCanonicalHex32("owner_script_hash", view.Pass.OwnerScriptHash); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProfileStateMismatch, err)
	}
	return activation, nil
}
