package usdb

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/params"
)

func TestGeneratedBTCActivationGoldenMatchesRustRegistryIDs(t *testing.T) {
	const expectedSetID = "01d1d45f342994690d8ae27ac3d8538ad31e5f81f8e948c838067b3b52f94691"
	for _, test := range []struct {
		networkID  string
		registryID string
		revision   uint32
		current    bool
	}{
		{networkID: "btc-mainnet", registryID: BTCMainnetActivationRegistryIDV1, revision: 1, current: true},
		{networkID: "btc-regtest", registryID: BTCRegtestActivationRegistryIDV1, revision: 1, current: true},
		{networkID: "btc-regtest", registryID: BTCRegtestActivationRegistryIDRevision2, revision: 2},
	} {
		registry, err := loadBTCActivationRegistry(test.registryID)
		if err != nil {
			t.Fatalf("failed to load %s golden registry: %v", test.networkID, err)
		}
		if registry.NetworkID != test.networkID || registry.ActivationRegistryID != test.registryID ||
			registry.Revision != test.revision || registry.Current != test.current ||
			registry.StableLagBlocks != 5 {
			t.Fatalf("unexpected golden registry: %+v", registry)
		}
		for _, height := range []uint32{0, 1, ^uint32(0)} {
			activation, err := registry.lookup(height)
			if err != nil {
				t.Fatalf("failed to resolve %s at height %d: %v", test.networkID, height, err)
			}
			if activation.ActiveVersionSetID != expectedSetID {
				t.Fatalf("unexpected %s set id at height %d: %s", test.networkID, height, activation.ActiveVersionSetID)
			}
		}
	}
	if params.USDBChainConfig.USDB.Activations[0].BTCActivationRegistryID != BTCRegtestActivationRegistryIDV1 {
		t.Fatalf("built-in USDB chain config is not bound to the generated regtest registry: %s", params.USDBChainConfig.USDB.Activations[0].BTCActivationRegistryID)
	}
}

func TestBTCActivationGoldenRejectsUnknownRegistryAndTampering(t *testing.T) {
	if _, err := loadBTCActivationRegistry(strings.Repeat("f", 64)); !errors.Is(err, ErrBTCActivationRegistryNotSupported) {
		t.Fatalf("expected unknown registry to fail closed, got %v", err)
	}

	tampered := strings.Replace(
		string(btcActivationGoldenJSON),
		"01d1d45f342994690d8ae27ac3d8538ad31e5f81f8e948c838067b3b52f94691",
		strings.Repeat("a", 64),
		1,
	)
	if _, err := parseBTCActivationGolden([]byte(tampered)); err == nil {
		t.Fatal("expected tampered generated set id to fail")
	}
}

func TestBTCActivationLookupUsesPayloadHeight(t *testing.T) {
	v1 := newTestActiveVersionSet(t)
	v1ID, err := v1.ID()
	if err != nil {
		t.Fatalf("failed to identify v1 set: %v", err)
	}
	v2 := newTestActiveVersionSet(t)
	v2["energy_formula_version"] = json.RawMessage(`"uip-0003-pass-energy-formula:v2"`)
	v2ID, err := v2.ID()
	if err != nil {
		t.Fatalf("failed to identify v2 set: %v", err)
	}
	registry := &btcActivationRegistry{
		ActivationRegistryID: strings.Repeat("a", 64),
		Activations: []btcActivationPoint{
			{BTCHeight: 0, ActiveVersionSet: v1, ActiveVersionSetID: v1ID},
			{BTCHeight: 100, ActiveVersionSet: v2, ActiveVersionSetID: v2ID},
		},
	}
	for _, test := range []struct {
		height uint32
		wantID string
	}{
		{height: 99, wantID: v1ID},
		{height: 100, wantID: v2ID},
		{height: 101, wantID: v2ID},
	} {
		activation, err := registry.lookup(test.height)
		if err != nil {
			t.Fatalf("height %d lookup failed: %v", test.height, err)
		}
		if activation.ActiveVersionSetID != test.wantID {
			t.Fatalf("height %d returned %s, want %s", test.height, activation.ActiveVersionSetID, test.wantID)
		}
	}
}

func TestBTCActivationGoldenReloadPreservesCrossActivationReplay(t *testing.T) {
	v1 := newTestActiveVersionSet(t)
	v1ID, err := v1.ID()
	if err != nil {
		t.Fatalf("failed to identify v1 set: %v", err)
	}
	v2 := newTestActiveVersionSet(t)
	v2["energy_formula_version"] = json.RawMessage(`"uip-0003-pass-energy-formula:v2"`)
	v2ID, err := v2.ID()
	if err != nil {
		t.Fatalf("failed to identify v2 set: %v", err)
	}
	registryID := strings.Repeat("a", 64)
	artifact := btcActivationGoldenArtifact{
		SchemaVersion:               goActivationGoldenSchemaVersion,
		SourceRegistrySchemaVersion: btcActivationRegistrySchemaV2,
		Registries: []btcActivationRegistry{{
			NetworkID:            "btc-restart-test",
			Revision:             1,
			Current:              true,
			StableLagBlocks:      5,
			ActivationRegistryID: registryID,
			Activations: []btcActivationPoint{
				{BTCHeight: 0, ActiveVersionSet: v1, ActiveVersionSetID: v1ID},
				{BTCHeight: 100, ActiveVersionSet: v2, ActiveVersionSetID: v2ID},
			},
		}},
	}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("failed to encode synthetic golden artifact: %v", err)
	}
	reloaded, err := parseBTCActivationGolden(encoded)
	if err != nil {
		t.Fatalf("failed to reload synthetic golden artifact: %v", err)
	}
	registry := reloaded[registryID]
	for _, test := range []struct {
		name   string
		height uint32
		wantID string
	}{
		{name: "after activation", height: 101, wantID: v2ID},
		{name: "rollback before activation", height: 99, wantID: v1ID},
		{name: "replay activation boundary", height: 100, wantID: v2ID},
	} {
		t.Run(test.name, func(t *testing.T) {
			activation, err := registry.lookup(test.height)
			if err != nil {
				t.Fatalf("height %d lookup failed: %v", test.height, err)
			}
			if activation.ActiveVersionSetID != test.wantID {
				t.Fatalf("height %d returned %s, want %s", test.height, activation.ActiveVersionSetID, test.wantID)
			}
		})
	}
}

func TestBTCActivationGoldenCatalogRetainsImmutableRevisions(t *testing.T) {
	v1 := newTestActiveVersionSet(t)
	v1ID, err := v1.ID()
	if err != nil {
		t.Fatalf("failed to identify v1 set: %v", err)
	}
	oldID := strings.Repeat("a", 64)
	currentID := strings.Repeat("b", 64)
	artifact := btcActivationGoldenArtifact{
		SchemaVersion:               goActivationGoldenSchemaVersion,
		SourceRegistrySchemaVersion: btcActivationRegistrySchemaV2,
		Registries: []btcActivationRegistry{
			{
				NetworkID:            "btc-regtest-revisions",
				Revision:             1,
				StableLagBlocks:      5,
				ActivationRegistryID: oldID,
				Activations: []btcActivationPoint{{
					BTCHeight: 0, ActiveVersionSet: v1, ActiveVersionSetID: v1ID,
				}},
			},
			{
				NetworkID:            "btc-regtest-revisions",
				Revision:             2,
				Current:              true,
				StableLagBlocks:      5,
				ActivationRegistryID: currentID,
				Activations: []btcActivationPoint{{
					BTCHeight: 0, ActiveVersionSet: v1, ActiveVersionSetID: v1ID,
				}},
			},
		},
	}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("failed to encode revision catalog: %v", err)
	}
	registries, err := parseBTCActivationGolden(encoded)
	if err != nil {
		t.Fatalf("failed to parse revision catalog: %v", err)
	}
	if registries[oldID] == nil || registries[currentID] == nil {
		t.Fatalf("revision catalog did not retain both registry ids: %+v", registries)
	}

	rewritten := artifact
	rewritten.Registries = append([]btcActivationRegistry(nil), artifact.Registries...)
	rewritten.Registries[1].Activations = append([]btcActivationPoint(nil), artifact.Registries[1].Activations...)
	rewritten.Registries[1].Activations[0].BTCHeight = 1
	encoded, err = json.Marshal(rewritten)
	if err != nil {
		t.Fatalf("failed to encode rewritten catalog: %v", err)
	}
	if _, err := parseBTCActivationGolden(encoded); err == nil || !strings.Contains(err.Error(), "rewrites activation index") {
		t.Fatalf("expected historical revision rewrite to fail, got %v", err)
	}

	changedLag := artifact
	changedLag.Registries = append([]btcActivationRegistry(nil), artifact.Registries...)
	changedLag.Registries[1].StableLagBlocks++
	encoded, err = json.Marshal(changedLag)
	if err != nil {
		t.Fatalf("failed to encode changed-lag catalog: %v", err)
	}
	if _, err := parseBTCActivationGolden(encoded); err == nil || !strings.Contains(err.Error(), "changes stable_lag_blocks") {
		t.Fatalf("expected stable lag revision change to fail, got %v", err)
	}
}

func TestProfileFormulaDispatchRejectsUnsupportedActiveVersion(t *testing.T) {
	selector := newTestSelector(t, 123)
	profile := newTestProfileView(t, selector, "1", "0")
	versions := newTestActiveVersionSet(t)
	versions["level_formula_version"] = json.RawMessage(`"uip-0005-level-and-real-difficulty:v2"`)
	if _, _, _, _, _, err := resolveProfileFormulaValues(versions, profile.Pass); !errors.Is(err, ErrUnsupportedBTCFormulaVersion) {
		t.Fatalf("expected unsupported formula version to fail closed, got %v", err)
	}
}

func TestVerifierDispatchesFormulaFromPayloadHeight(t *testing.T) {
	v1 := newTestActiveVersionSet(t)
	v1ID, err := v1.ID()
	if err != nil {
		t.Fatalf("failed to identify v1 set: %v", err)
	}
	v2 := newTestActiveVersionSet(t)
	v2["energy_formula_version"] = json.RawMessage(`"uip-0003-pass-energy-formula:v2"`)
	v2ID, err := v2.ID()
	if err != nil {
		t.Fatalf("failed to identify v2 set: %v", err)
	}
	registryID := strings.Repeat("a", 64)
	registry := &btcActivationRegistry{
		ActivationRegistryID: registryID,
		StableLagBlocks:      5,
		Activations: []btcActivationPoint{
			{BTCHeight: 0, ActiveVersionSet: v1, ActiveVersionSetID: v1ID},
			{BTCHeight: 100, ActiveVersionSet: v2, ActiveVersionSetID: v2ID},
		},
	}

	before := newTestSelector(t, 99)
	beforeProfile := newTestProfileView(t, before, "1", "0")
	beforeProfile.ExternalState.ActivationRegistryID = registryID
	beforeClient := &stubProfileClient{profile: beforeProfile}
	if _, err := resolveConsensusProfile(context.Background(), beforeClient, registry, before); err != nil {
		t.Fatalf("v1 profile before activation boundary failed: %v", err)
	}
	if beforeClient.lastQuery.ExpectedState.ActiveVersionSetID != v1ID {
		t.Fatalf("pre-activation query used set %s, want %s", beforeClient.lastQuery.ExpectedState.ActiveVersionSetID, v1ID)
	}

	after := newTestSelector(t, 100)
	afterProfile := newTestProfileView(t, after, "1", "0")
	afterProfile.ExternalState.ActivationRegistryID = registryID
	afterProfile.ExternalState.ActiveVersionSet = v2
	afterProfile.ExternalState.ActiveVersionSetID = v2ID
	afterClient := &stubProfileClient{profile: afterProfile}
	if _, err := resolveConsensusProfile(context.Background(), afterClient, registry, after); !errors.Is(err, ErrUnsupportedBTCFormulaVersion) {
		t.Fatalf("expected v2 formula selected at activation boundary to fail closed, got %v", err)
	}
	if afterClient.lastQuery.ExpectedState.ActiveVersionSetID != v2ID {
		t.Fatalf("post-activation query used set %s, want %s", afterClient.lastQuery.ExpectedState.ActiveVersionSetID, v2ID)
	}
}
