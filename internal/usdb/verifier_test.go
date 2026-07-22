package usdb

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestVerifierResolveProfileValidatesUIP0006View(t *testing.T) {
	selector := newTestSelector(t, 123)
	client := &stubProfileClient{profile: newTestProfileView(t, selector, "1000000", "500000")}
	verifier, err := NewVerifier(client, 0)
	if err != nil {
		t.Fatalf("failed to build verifier: %v", err)
	}

	resolved, err := verifier.ResolveProfile(context.Background(), marshalTestSelector(t, selector))
	if err != nil {
		t.Fatalf("failed to resolve profile: %v", err)
	}
	if resolved.Selector != selector {
		t.Fatalf("unexpected resolved selector: have %+v want %+v", resolved.Selector, selector)
	}
	if resolved.RawEnergy.String() != "1000000" ||
		resolved.CollabContribution.String() != "500000" ||
		resolved.EffectiveEnergy.String() != "1500000" {
		t.Fatalf("unexpected resolved energy values: raw=%s contribution=%s effective=%s", resolved.RawEnergy, resolved.CollabContribution, resolved.EffectiveEnergy)
	}
	if resolved.Level != 1 || resolved.DifficultyFactorBps != 9_900 {
		t.Fatalf("unexpected level/factor: level=%d factor=%d", resolved.Level, resolved.DifficultyFactorBps)
	}
	if client.lastQuery.RequestedHeight != selector.BTCHeight ||
		client.lastQuery.ExpectedState.SnapshotID != selector.SnapshotIDHex() ||
		client.lastQuery.ExpectedState.SystemStateID != selector.SystemStateIDHex() {
		t.Fatalf("historical query was not pinned to selector: %+v", client.lastQuery)
	}
}

func TestVerifierResolveProfileRejectsMissingPayloadAndProfile(t *testing.T) {
	verifier, err := NewVerifier(&stubProfileClient{}, 0)
	if err != nil {
		t.Fatalf("failed to build verifier: %v", err)
	}
	if _, err := verifier.ResolveProfile(context.Background(), nil); !errors.Is(err, ErrMissingProfileSelector) {
		t.Fatalf("expected missing selector error, got %v", err)
	}
	selector := newTestSelector(t, 123)
	if _, err := verifier.ResolveProfile(context.Background(), marshalTestSelector(t, selector)); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("expected missing profile error, got %v", err)
	}
}

func TestVerifierResolveProfileRejectsSelectorIdentityMismatch(t *testing.T) {
	selector := newTestSelector(t, 123)
	tests := []struct {
		name   string
		mutate func(*PassEconomicProfileView)
	}{
		{name: "view version", mutate: func(view *PassEconomicProfileView) { view.ViewVersion = "v999" }},
		{name: "btc height", mutate: func(view *PassEconomicProfileView) { view.ExternalState.BTCHeight++ }},
		{name: "snapshot id", mutate: func(view *PassEconomicProfileView) { view.ExternalState.SnapshotID = repeatHex("aa", 32) }},
		{name: "system state id", mutate: func(view *PassEconomicProfileView) { view.ExternalState.SystemStateID = repeatHex("bb", 32) }},
		{name: "pass id", mutate: func(view *PassEconomicProfileView) { view.Pass.PassID = repeatHex("cc", 32) + "i7" }},
		{name: "incomplete external state", mutate: func(view *PassEconomicProfileView) { view.ExternalState.LocalStateCommit = "" }},
		{name: "missing owner", mutate: func(view *PassEconomicProfileView) { view.Pass.OwnerScriptHash = "" }},
		{name: "non-canonical owner", mutate: func(view *PassEconomicProfileView) { view.Pass.OwnerScriptHash = repeatHex("AA", 32) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := newTestProfileView(t, selector, "1000000", "0")
			test.mutate(profile)
			verifier, err := NewVerifier(&stubProfileClient{profile: profile}, 0)
			if err != nil {
				t.Fatalf("failed to build verifier: %v", err)
			}
			if _, err := verifier.ResolveProfile(context.Background(), marshalTestSelector(t, selector)); !errors.Is(err, ErrProfileStateMismatch) {
				t.Fatalf("expected state mismatch, got %v", err)
			}
		})
	}
}

func TestVerifierResolveProfileRejectsNonCandidatePass(t *testing.T) {
	selector := newTestSelector(t, 123)
	tests := []struct {
		state string
		kind  string
	}{
		{state: "dormant", kind: passKindStandard},
		{state: "consumed", kind: passKindStandard},
		{state: "burned", kind: passKindStandard},
		{state: "invalid", kind: passKindStandard},
		{state: passStateActive, kind: "collab"},
	}
	for _, test := range tests {
		profile := newTestProfileView(t, selector, "0", "0")
		profile.Pass.State = test.state
		profile.Pass.PassKind = test.kind
		verifier, err := NewVerifier(&stubProfileClient{profile: profile}, 0)
		if err != nil {
			t.Fatalf("failed to build verifier: %v", err)
		}
		if _, err := verifier.ResolveProfile(context.Background(), marshalTestSelector(t, selector)); !errors.Is(err, ErrSelectedPassNotCandidate) {
			t.Fatalf("state=%s kind=%s: expected candidate error, got %v", test.state, test.kind, err)
		}
	}
}

func TestVerifierResolveProfileAcceptsZeroEnergyActiveStandardCandidate(t *testing.T) {
	selector := newTestSelector(t, 123)
	profile := newTestProfileView(t, selector, "0", "0")
	verifier, err := NewVerifier(&stubProfileClient{profile: profile}, 0)
	if err != nil {
		t.Fatalf("failed to build verifier: %v", err)
	}
	resolved, err := verifier.ResolveProfile(context.Background(), marshalTestSelector(t, selector))
	if err != nil {
		t.Fatalf("zero-energy active standard should remain a candidate: %v", err)
	}
	if resolved.Level != 0 || resolved.DifficultyFactorBps != BasisPointDenominator {
		t.Fatalf("unexpected zero-energy level/factor: level=%d factor=%d", resolved.Level, resolved.DifficultyFactorBps)
	}
}

func TestVerifierResolveProfileRejectsInvalidEnergyEncoding(t *testing.T) {
	selector := newTestSelector(t, 123)
	overflow := new(big.Int).Add(maximumEnergyValue, big.NewInt(1)).String()
	tests := []struct {
		name   string
		mutate func(*PassEconomicProfileView)
	}{
		{name: "raw leading zero", mutate: func(view *PassEconomicProfileView) { view.Pass.RawEnergy = "01" }},
		{name: "contribution negative", mutate: func(view *PassEconomicProfileView) { view.Pass.CollabContribution = "-1" }},
		{name: "effective overflow", mutate: func(view *PassEconomicProfileView) { view.Pass.EffectiveEnergy = overflow }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := newTestProfileView(t, selector, "1", "1")
			test.mutate(profile)
			verifier, err := NewVerifier(&stubProfileClient{profile: profile}, 0)
			if err != nil {
				t.Fatalf("failed to build verifier: %v", err)
			}
			if _, err := verifier.ResolveProfile(context.Background(), marshalTestSelector(t, selector)); !errors.Is(err, ErrInvalidProfileEnergy) {
				t.Fatalf("expected invalid energy error, got %v", err)
			}
		})
	}
}

func TestVerifierResolveProfileCrossChecksDerivedValues(t *testing.T) {
	selector := newTestSelector(t, 123)
	tests := []struct {
		name   string
		mutate func(*PassEconomicProfileView)
	}{
		{name: "effective energy", mutate: func(view *PassEconomicProfileView) { view.Pass.EffectiveEnergy = "1" }},
		{name: "level", mutate: func(view *PassEconomicProfileView) { view.Pass.Level++ }},
		{name: "difficulty factor", mutate: func(view *PassEconomicProfileView) { view.Pass.DifficultyFactorBps++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := newTestProfileView(t, selector, "1000000", "500000")
			test.mutate(profile)
			verifier, err := NewVerifier(&stubProfileClient{profile: profile}, 0)
			if err != nil {
				t.Fatalf("failed to build verifier: %v", err)
			}
			if _, err := verifier.ResolveProfile(context.Background(), marshalTestSelector(t, selector)); !errors.Is(err, ErrProfileDerivedValueMismatch) {
				t.Fatalf("expected derived value mismatch, got %v", err)
			}
		})
	}
}

func TestVerifierResolveProfileAcceptsSaturatedEffectiveEnergy(t *testing.T) {
	selector := newTestSelector(t, 123)
	profile := newTestProfileView(t, selector, maximumEnergyValue.String(), "1")
	verifier, err := NewVerifier(&stubProfileClient{profile: profile}, 0)
	if err != nil {
		t.Fatalf("failed to build verifier: %v", err)
	}
	resolved, err := verifier.ResolveProfile(context.Background(), marshalTestSelector(t, selector))
	if err != nil {
		t.Fatalf("failed to resolve saturated profile: %v", err)
	}
	if resolved.EffectiveEnergy.Cmp(maximumEnergyValue) != 0 || resolved.Level != MaximumLevel {
		t.Fatalf("unexpected saturated profile: effective=%s level=%d", resolved.EffectiveEnergy, resolved.Level)
	}
}

func TestVerifierHistoricalReplayIgnoresCurrentHead(t *testing.T) {
	selector := newTestSelector(t, 123)
	client := &stubProfileClient{
		system:  &SystemStateInfo{LocalSyncedBlockHeight: 500},
		profile: newTestProfileView(t, selector, "1000000", "500000"),
	}
	verifier, err := NewVerifier(client, 0)
	if err != nil {
		t.Fatalf("failed to build verifier: %v", err)
	}
	encoded := marshalTestSelector(t, selector)
	first, err := verifier.ResolveProfile(context.Background(), encoded)
	if err != nil {
		t.Fatalf("initial historical replay failed: %v", err)
	}
	client.system.LocalSyncedBlockHeight = 900
	second, err := verifier.ResolveProfile(context.Background(), encoded)
	if err != nil {
		t.Fatalf("historical replay after head advance failed: %v", err)
	}
	if first.Selector != second.Selector || first.EffectiveEnergy.Cmp(second.EffectiveEnergy) != 0 {
		t.Fatalf("historical replay changed after current head advance: first=%+v second=%+v", first, second)
	}
	if client.lastQuery.RequestedHeight != selector.BTCHeight {
		t.Fatalf("historical replay used current height instead of selector: %+v", client.lastQuery)
	}
}

func TestVerifierRejectsSameHeightReorgStateReplacement(t *testing.T) {
	selector := newTestSelector(t, 123)
	client := &stubProfileClient{profile: newTestProfileView(t, selector, "1000000", "0")}
	verifier, err := NewVerifier(client, 0)
	if err != nil {
		t.Fatalf("failed to build verifier: %v", err)
	}
	encoded := marshalTestSelector(t, selector)
	if _, err := verifier.ResolveProfile(context.Background(), encoded); err != nil {
		t.Fatalf("canonical state rejected before reorg: %v", err)
	}
	replaced := *client.profile
	replaced.ExternalState = client.profile.ExternalState
	replaced.ExternalState.SnapshotID = repeatHex("aa", 32)
	replaced.ExternalState.SystemStateID = repeatHex("bb", 32)
	client.profile = &replaced
	if _, err := verifier.ResolveProfile(context.Background(), encoded); !errors.Is(err, ErrProfileStateMismatch) {
		t.Fatalf("same-height replacement did not invalidate old selector: %v", err)
	}
}

func TestVerifierRejectsTamperedSelectorFields(t *testing.T) {
	selector := newTestSelector(t, 123)
	profile := newTestProfileView(t, selector, "1000000", "0")
	encoded := marshalTestSelector(t, selector)
	tests := []struct {
		name   string
		offset int
	}{
		{name: "btc height", offset: btcHeightOffset + 3},
		{name: "snapshot id", offset: snapshotIDOffset},
		{name: "system state id", offset: systemStateIDOffset},
		{name: "pass txid", offset: passIDOffset},
		{name: "pass index", offset: passIDOffset + common.HashLength + 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := append([]byte(nil), encoded...)
			tampered[test.offset] ^= 0x01
			verifier, err := NewVerifier(&stubProfileClient{profile: profile}, 0)
			if err != nil {
				t.Fatalf("failed to build verifier: %v", err)
			}
			if _, err := verifier.ResolveProfile(context.Background(), tampered); !errors.Is(err, ErrProfileStateMismatch) {
				t.Fatalf("tampered %s did not fail identity validation: %v", test.name, err)
			}
		})
	}
}

func TestVerifierPropagatesHistoricalAndServiceFailures(t *testing.T) {
	selector := newTestSelector(t, 123)
	encoded := marshalTestSelector(t, selector)
	for _, expected := range []error{ErrSnapshotIDMismatch, ErrHistoryNotAvailable, ErrStateNotRetained, context.DeadlineExceeded} {
		verifier, err := NewVerifier(&stubProfileClient{profileErr: expected}, 0)
		if err != nil {
			t.Fatalf("failed to build verifier: %v", err)
		}
		if _, err := verifier.ResolveProfile(context.Background(), encoded); !errors.Is(err, expected) {
			t.Fatalf("expected %v to propagate, got %v", expected, err)
		}
	}
}
