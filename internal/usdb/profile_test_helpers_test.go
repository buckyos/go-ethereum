package usdb

import (
	"context"
	"testing"
)

type stubProfileClient struct {
	system     *SystemStateInfo
	profile    *PassEconomicProfileView
	systemErr  error
	profileErr error
	lastPassID PassID
	lastQuery  QueryContext
}

func (s *stubProfileClient) GetSystemStateInfo(context.Context) (*SystemStateInfo, error) {
	return s.system, s.systemErr
}

func (s *stubProfileClient) GetPassEconomicProfile(_ context.Context, passID PassID, query QueryContext) (*PassEconomicProfileView, error) {
	s.lastPassID = passID
	s.lastQuery = query
	return s.profile, s.profileErr
}

func (s *stubProfileClient) Close() {}

func newTestSelector(t *testing.T, height uint32) ProfileSelectorPayload {
	t.Helper()
	selector, err := NewProfileSelectorPayload(
		DifficultyPolicyVersionV1,
		height,
		repeatHex("11", 32),
		repeatHex("22", 32),
		repeatHex("33", 32)+"i7",
	)
	if err != nil {
		t.Fatalf("failed to create test selector: %v", err)
	}
	return *selector
}

func marshalTestSelector(t *testing.T, selector ProfileSelectorPayload) []byte {
	t.Helper()
	encoded, err := selector.MarshalBinary()
	if err != nil {
		t.Fatalf("failed to encode test selector: %v", err)
	}
	return encoded
}

func newTestProfileView(t *testing.T, selector ProfileSelectorPayload, rawEnergy, collabContribution string) *PassEconomicProfileView {
	t.Helper()
	raw, err := parseEnergyDecimal("raw_energy", rawEnergy)
	if err != nil {
		t.Fatalf("invalid test raw energy: %v", err)
	}
	collab, err := parseEnergyDecimal("collab_contribution", collabContribution)
	if err != nil {
		t.Fatalf("invalid test collab contribution: %v", err)
	}
	effective := saturatingAddEnergy(raw, collab)
	level := LevelForEffectiveEnergy(effective)
	return &PassEconomicProfileView{
		ViewVersion: EconomicStateViewVersionV1,
		ExternalState: EconomicExternalState{
			BTCHeight:                      selector.BTCHeight,
			SnapshotID:                     selector.SnapshotIDHex(),
			StableBlockHash:                repeatHex("44", 32),
			LocalStateCommit:               repeatHex("55", 32),
			SystemStateID:                  selector.SystemStateIDHex(),
			BalanceHistoryAPIVersion:       "1.0.0",
			BalanceHistorySemanticsVersion: "balance-snapshot-at-or-before:v1",
			USDBIndexProtocolVersion:       "1.0.0",
			USDBIndexFormulaVersion:        "pass-energy-formula:v1",
		},
		Pass: PassEconomicProfile{
			PassID:               selector.PassID.String(),
			OwnerScriptHash:      repeatHex("66", 32),
			State:                passStateActive,
			PassKind:             passKindStandard,
			RawEnergy:            rawEnergy,
			CollabContribution:   collabContribution,
			EffectiveEnergy:      effective.String(),
			Level:                level,
			DifficultyFactorBps:  DifficultyFactorBpsForLevel(level),
			CollabBreakdownCount: 1,
		},
	}
}
