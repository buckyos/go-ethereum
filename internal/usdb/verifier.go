package usdb

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"
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

// ResolvedReward is the current reward adapter over a verified consensus profile.
//
// UIP-0007 only establishes the profile boundary. The final reward policy remains
// versioned separately and will be replaced by the corresponding reward UIP.
type ResolvedReward struct {
	Profile       *ResolvedConsensusProfile
	MultiplierBps uint64
	BaseReward    *big.Int
	MinerReward   *big.Int
}

// Verifier resolves historical consensus profiles from the USDB RPC surface.
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

// NewRPCVerifier dials one USDB endpoint and uses it to resolve consensus profiles.
func NewRPCVerifier(endpoint string, queryTimeout time.Duration) (*Verifier, error) {
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

// ResolveProfile decodes header.Extra and validates its historical UIP-0006 profile.
func (v *Verifier) ResolveProfile(ctx context.Context, headerExtra []byte) (*ResolvedConsensusProfile, error) {
	if len(headerExtra) == 0 {
		return nil, ErrMissingProfileSelector
	}
	var selector ProfileSelectorPayload
	if err := selector.UnmarshalBinary(headerExtra); err != nil {
		return nil, err
	}

	queryCtx, cancel := context.WithTimeout(ctx, v.queryTimeout)
	defer cancel()
	return resolveConsensusProfile(queryCtx, v.client, selector)
}

// ResolveReward adapts a verified profile to the current development reward policy.
func (v *Verifier) ResolveReward(ctx context.Context, headerExtra []byte, blockNumber uint64) (*ResolvedReward, error) {
	profile, err := v.ResolveProfile(ctx, headerExtra)
	if err != nil {
		return nil, err
	}
	baseReward := BaseReward(blockNumber)
	minerReward := RewardForLevel(blockNumber, profile.Level)
	return &ResolvedReward{
		Profile:       profile,
		MultiplierBps: MultiplierBpsForLevel(profile.Level),
		BaseReward:    baseReward,
		MinerReward:   minerReward,
	}, nil
}

func resolveConsensusProfile(ctx context.Context, client Client, selector ProfileSelectorPayload) (*ResolvedConsensusProfile, error) {
	query := QueryContext{
		RequestedHeight: selector.BTCHeight,
		ExpectedState: QueryExpectedState{
			SnapshotID:    selector.SnapshotIDHex(),
			SystemStateID: selector.SystemStateIDHex(),
		},
	}
	view, err := client.GetPassEconomicProfile(ctx, selector.PassID, query)
	if err != nil {
		return nil, err
	}
	if view == nil {
		return nil, fmt.Errorf("%w: pass %s", ErrProfileNotFound, selector.PassID.String())
	}
	if err := validateProfileIdentity(selector, view); err != nil {
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

	rawEnergy, err := parseEnergyDecimal("raw_energy", view.Pass.RawEnergy)
	if err != nil {
		return nil, err
	}
	collabContribution, err := parseEnergyDecimal("collab_contribution", view.Pass.CollabContribution)
	if err != nil {
		return nil, err
	}
	effectiveEnergy, err := parseEnergyDecimal("effective_energy", view.Pass.EffectiveEnergy)
	if err != nil {
		return nil, err
	}
	expectedEffectiveEnergy := saturatingAddEnergy(rawEnergy, collabContribution)
	if effectiveEnergy.Cmp(expectedEffectiveEnergy) != 0 {
		return nil, fmt.Errorf(
			"%w: effective_energy have %s want %s",
			ErrProfileDerivedValueMismatch,
			effectiveEnergy,
			expectedEffectiveEnergy,
		)
	}
	level := LevelForEffectiveEnergy(effectiveEnergy)
	if view.Pass.Level != level {
		return nil, fmt.Errorf(
			"%w: level have %d want %d",
			ErrProfileDerivedValueMismatch,
			view.Pass.Level,
			level,
		)
	}
	difficultyFactorBps := DifficultyFactorBpsForLevel(level)
	if view.Pass.DifficultyFactorBps != difficultyFactorBps {
		return nil, fmt.Errorf(
			"%w: difficulty_factor_bps have %d want %d",
			ErrProfileDerivedValueMismatch,
			view.Pass.DifficultyFactorBps,
			difficultyFactorBps,
		)
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

func validateProfileIdentity(selector ProfileSelectorPayload, view *PassEconomicProfileView) error {
	if view.ViewVersion != EconomicStateViewVersionV1 {
		return fmt.Errorf(
			"%w: view_version have %q want %q",
			ErrProfileStateMismatch,
			view.ViewVersion,
			EconomicStateViewVersionV1,
		)
	}
	state := view.ExternalState
	if state.BTCHeight != selector.BTCHeight {
		return fmt.Errorf("%w: btc_height have %d want %d", ErrProfileStateMismatch, state.BTCHeight, selector.BTCHeight)
	}
	if state.SnapshotID != selector.SnapshotIDHex() {
		return fmt.Errorf("%w: snapshot_id have %q want %q", ErrProfileStateMismatch, state.SnapshotID, selector.SnapshotIDHex())
	}
	if state.SystemStateID != selector.SystemStateIDHex() {
		return fmt.Errorf("%w: system_state_id have %q want %q", ErrProfileStateMismatch, state.SystemStateID, selector.SystemStateIDHex())
	}
	if view.Pass.PassID != selector.PassID.String() {
		return fmt.Errorf("%w: pass_id have %q want %q", ErrProfileStateMismatch, view.Pass.PassID, selector.PassID.String())
	}
	if _, err := ParsePassID(view.Pass.PassID); err != nil {
		return fmt.Errorf("%w: response pass_id is not canonical: %v", ErrProfileStateMismatch, err)
	}
	if state.StableBlockHash == "" || state.LocalStateCommit == "" ||
		state.BalanceHistoryAPIVersion == "" || state.BalanceHistorySemanticsVersion == "" ||
		state.USDBIndexProtocolVersion == "" || state.USDBIndexFormulaVersion == "" {
		return fmt.Errorf("%w: external_state is incomplete", ErrProfileStateMismatch)
	}
	if _, err := parseCanonicalHex32("owner_script_hash", view.Pass.OwnerScriptHash); err != nil {
		return fmt.Errorf("%w: %v", ErrProfileStateMismatch, err)
	}
	return nil
}
