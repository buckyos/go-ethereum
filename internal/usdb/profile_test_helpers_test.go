package usdb

import (
	"context"
	"encoding/json"
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
		0,
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

func newTestActiveVersionSet(t *testing.T) ActiveVersionSet {
	t.Helper()
	values := map[string]string{
		"inscription_schema_version":        InscriptionSchemaVersionV1,
		"pass_state_machine_version":        PassStateMachineVersionV1,
		"energy_formula_version":            EnergyFormulaVersionV1,
		"effective_energy_formula_version":  EffectiveEnergyFormulaVersionV1,
		"level_formula_version":             LevelFormulaVersionV1,
		"query_semantics_version":           QuerySemanticsVersionV1,
		"state_view_version":                EconomicStateViewVersionV1,
		"commit_protocol_version":           CommitProtocolVersionV1,
		"balance_history_semantics_version": BalanceHistorySemanticsVersionV1,
	}
	set := make(ActiveVersionSet, len(values))
	for family, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("failed to encode %s: %v", family, err)
		}
		set[family] = encoded
	}
	return set
}

func newTestSystemStateInfo(t *testing.T, selector ProfileSelectorPayload) *SystemStateInfo {
	t.Helper()
	activeVersionSet := newTestActiveVersionSet(t)
	activeVersionSetID, err := activeVersionSet.ID()
	if err != nil {
		t.Fatalf("failed to identify test active version set: %v", err)
	}
	return &SystemStateInfo{
		ActivationRegistryID:   BTCRegtestActivationRegistryIDV1,
		ActiveVersionSet:       activeVersionSet,
		ActiveVersionSetID:     activeVersionSetID,
		LocalSyncedBlockHeight: selector.BTCHeight,
		UpstreamSnapshotID:     selector.SnapshotIDHex(),
		SystemStateID:          selector.SystemStateIDHex(),
	}
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
	activeVersionSet := newTestActiveVersionSet(t)
	activeVersionSetID, err := activeVersionSet.ID()
	if err != nil {
		t.Fatalf("failed to identify test active version set: %v", err)
	}
	usdbMain := "0x1111111111111111111111111111111111111111"
	return &PassEconomicProfileView{
		ViewVersion: EconomicStateViewVersionV1,
		ExternalState: EconomicExternalState{
			BTCHeight:                      selector.BTCHeight,
			SnapshotID:                     selector.SnapshotIDHex(),
			StableBlockHash:                repeatHex("44", 32),
			StableLag:                      5,
			LocalStateCommit:               repeatHex("55", 32),
			SystemStateID:                  selector.SystemStateIDHex(),
			BalanceHistoryAPIVersion:       "1.0.0",
			BalanceHistorySemanticsVersion: "balance-snapshot-at-or-before:v1",
			ActivationRegistryID:           BTCRegtestActivationRegistryIDV1,
			ActiveVersionSet:               activeVersionSet,
			ActiveVersionSetID:             activeVersionSetID,
		},
		Pass: PassEconomicProfile{
			PassID:               selector.PassID.String(),
			OwnerScriptHash:      repeatHex("66", 32),
			State:                passStateActive,
			PassKind:             passKindStandard,
			USDBMain:             &usdbMain,
			RawEnergy:            rawEnergy,
			CollabContribution:   collabContribution,
			EffectiveEnergy:      effective.String(),
			Level:                level,
			DifficultyFactorBps:  DifficultyFactorBpsForLevel(level),
			CollabBreakdownCount: 1,
		},
		MinerAggregate: MinerEconomicAggregate{
			TotalMinerBTCSats:     "2100000000000000",
			ActiveMinerOwnerCount: 1,
		},
	}
}
