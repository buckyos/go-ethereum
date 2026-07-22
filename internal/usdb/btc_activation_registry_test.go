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
	}{
		{networkID: "btc-mainnet", registryID: BTCMainnetActivationRegistryIDV1},
		{networkID: "btc-regtest", registryID: BTCRegtestActivationRegistryIDV1},
	} {
		registry, err := loadBTCActivationRegistry(test.registryID)
		if err != nil {
			t.Fatalf("failed to load %s golden registry: %v", test.networkID, err)
		}
		if registry.NetworkID != test.networkID || registry.ActivationRegistryID != test.registryID {
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
	if params.USDBChainConfig.USDB.BTCActivationRegistryID != BTCRegtestActivationRegistryIDV1 {
		t.Fatalf("built-in USDB chain config is not bound to the generated regtest registry: %s", params.USDBChainConfig.USDB.BTCActivationRegistryID)
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
		Activations: []btcActivationPoint{
			{BTCHeight: 0, ActiveVersionSet: v1, ActiveVersionSetID: v1ID},
			{BTCHeight: 100, ActiveVersionSet: v2, ActiveVersionSetID: v2ID},
		},
	}

	before := newTestSelector(t, 99)
	beforeProfile := newTestProfileView(t, before, "1", "0")
	beforeProfile.ExternalState.ActivationRegistryID = registryID
	beforeClient := &stubProfileClient{profile: beforeProfile}
	beforeVerifier := &Verifier{client: beforeClient, btcRegistry: registry, queryTimeout: DefaultQueryTimeout}
	if _, err := beforeVerifier.ResolveProfile(context.Background(), marshalTestSelector(t, before)); err != nil {
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
	afterVerifier := &Verifier{client: afterClient, btcRegistry: registry, queryTimeout: DefaultQueryTimeout}
	if _, err := afterVerifier.ResolveProfile(context.Background(), marshalTestSelector(t, after)); !errors.Is(err, ErrUnsupportedBTCFormulaVersion) {
		t.Fatalf("expected v2 formula selected at activation boundary to fail closed, got %v", err)
	}
	if afterClient.lastQuery.ExpectedState.ActiveVersionSetID != v2ID {
		t.Fatalf("post-activation query used set %s, want %s", afterClient.lastQuery.ExpectedState.ActiveVersionSetID, v2ID)
	}
}
