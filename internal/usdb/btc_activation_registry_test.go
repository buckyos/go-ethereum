package usdb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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
			registry.StableLagBlocks != 10 {
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

func TestDescribeBTCActivationRegistry(t *testing.T) {
	descriptor, err := DescribeBTCActivationRegistry(BTCMainnetActivationRegistryIDV1)
	if err != nil {
		t.Fatalf("failed to describe BTC mainnet registry: %v", err)
	}
	if descriptor.NetworkID != "btc-mainnet" || descriptor.Revision != 1 || !descriptor.Current ||
		descriptor.StableLagBlocks != 10 || descriptor.ActivationRegistryID != BTCMainnetActivationRegistryIDV1 {
		t.Fatalf("unexpected BTC mainnet registry descriptor: %+v", descriptor)
	}
	if _, err := DescribeBTCActivationRegistry(strings.Repeat("f", 64)); !errors.Is(err, ErrBTCActivationRegistryNotSupported) {
		t.Fatalf("expected unknown registry descriptor to fail closed, got %v", err)
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
	delete(v1, "scope")
	v1ID, err := v1.ID()
	if err != nil {
		t.Fatalf("failed to identify v1 set: %v", err)
	}
	v2 := newTestActiveVersionSet(t)
	delete(v2, "scope")
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
	delete(v1, "scope")
	v1ID, err := v1.ID()
	if err != nil {
		t.Fatalf("failed to identify v1 set: %v", err)
	}
	v2 := newTestActiveVersionSet(t)
	delete(v2, "scope")
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
			StableLagBlocks:      10,
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
	delete(v1, "scope")
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
				StableLagBlocks:      10,
				ActivationRegistryID: oldID,
				Activations: []btcActivationPoint{{
					BTCHeight: 0, ActiveVersionSet: v1, ActiveVersionSetID: v1ID,
				}},
			},
			{
				NetworkID:            "btc-regtest-revisions",
				Revision:             2,
				Current:              true,
				StableLagBlocks:      10,
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
		StableLagBlocks:      10,
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

func TestCurrentActivationIdentityRejectsDifferentRuleScopes(t *testing.T) {
	actual, err := loadBTCActivationRegistry(BTCMainnetActivationRegistryIDV1)
	if err != nil {
		t.Fatal(err)
	}
	point, err := actual.lookup(123)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		network string
		scope   string
	}{
		{name: "different BTC network", network: "btc-regtest"},
		{name: "same BTC network different rules", network: "btc-mainnet", scope: "usdb-testnet-fixture"},
	} {
		t.Run(test.name, func(t *testing.T) {
			expected := *actual
			expected.NetworkID = test.network
			expected.RulesScope = test.scope
			if err := validateCurrentActivationIdentity(1, 123, point.ActiveVersionSet, point.ActiveVersionSetID, actual.ActivationRegistryID, &expected); !errors.Is(err, ErrBTCActivationRegistryMismatch) {
				t.Fatalf("different rule history accepted despite matching formula versions: %v", err)
			}
		})
	}
	// Preparing a newer revision in the same supported scope remains possible.
	actual, err = loadBTCActivationRegistry(BTCRegtestMinerPassV2RegistryID)
	if err != nil {
		t.Fatal(err)
	}
	point, err = actual.lookup(123)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := loadBTCActivationRegistry(BTCRegtestMinerPassV2StagedRegistryID)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCurrentActivationIdentity(1, 123, point.ActiveVersionSet, point.ActiveVersionSetID, actual.ActivationRegistryID, expected); err != nil {
		t.Fatal(err)
	}

}

// This separate generated fixture does not activate experimental scopes in the
// production catalog. It checks the Rust-to-Go identity contract and independent
// revision histories on the same BTC source.
func TestScopedBTCActivationGoldenMatchesRustAndIsolatesCatalogs(t *testing.T) {
	blob, err := os.ReadFile("testdata/btc_activation_scoped_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	registries, err := parseBTCActivationGolden(blob)
	if err != nil {
		t.Fatal(err)
	}
	// The frozen vectors prove cross-language metadata identity, not executable V1 support.
	// Upgrade only this in-memory positive fixture to the current pair, preserving scopes.
	for _, registry := range registries {
		for i := range registry.Activations {
			point := &registry.Activations[i]
			if err := point.ActiveVersionSet.ValidateBTCProfileSurface(); err == nil {
				t.Fatal("historical fixture unexpectedly executable")
			}
			point.ActiveVersionSet["inscription_schema_version"], _ = json.Marshal(InscriptionSchemaVersionV1)
			point.ActiveVersionSet["pass_state_machine_version"], _ = json.Marshal(PassStateMachineVersionV2)
			point.ActiveVersionSetID, err = point.ActiveVersionSet.ID()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	scopes := make(map[string]bool)
	currentScopes := make(map[string]int)
	setScopes := make(map[string]string)
	for _, registry := range registries {
		if registry.NetworkID != "btc-mainnet" {
			t.Fatalf("fixture must share one real BTC source: %s", registry.NetworkID)
		}
		scopes[registry.RulesScope] = true
		if registry.Current {
			currentScopes[registry.RulesScope]++
		}
		// Looking up old heights after a later lookup models deterministic
		// replay without silently selecting another scope's current revision.
		for _, height := range []uint32{101, 99, 100, 0} {
			point, err := registry.lookup(height)
			if err != nil {
				t.Fatal(err)
			}
			if previous, exists := setScopes[point.ActiveVersionSetID]; exists && previous != registry.RulesScope {
				t.Fatalf("scopes %s and %s share version identity", previous, registry.RulesScope)
			}
			setScopes[point.ActiveVersionSetID] = registry.RulesScope
			if _, err := registry.validateIdentity(height, registry.ActivationRegistryID, point.ActiveVersionSet, point.ActiveVersionSetID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(scopes) < 2 {
		t.Fatal("fixture must cover independent rule scopes")
	}
	for scope := range scopes {
		if currentScopes[scope] != 1 {
			t.Fatalf("scope %s has %d current revisions", scope, currentScopes[scope])
		}
	}

	for _, registry := range registries {
		if !registry.Current {
			continue
		}
		for _, height := range []uint32{99, 100, 101, 99} {
			selector := newTestSelector(t, height)
			point, err := registry.lookup(height)
			if err != nil {
				t.Fatal(err)
			}
			view := newTestProfileView(t, selector, "1", "0")
			view.ExternalState.ActivationRegistryID = registry.ActivationRegistryID
			view.ExternalState.ActiveVersionSet = point.ActiveVersionSet
			view.ExternalState.ActiveVersionSetID = point.ActiveVersionSetID
			_, err = resolveConsensusProfile(context.Background(), &stubProfileClient{profile: view}, registry, selector)
			if registry.RulesScope == "usdb-testnet-fixture" && height >= 100 {
				if !errors.Is(err, ErrUnsupportedBTCFormulaVersion) {
					t.Fatalf("test-scope unsupported activation at %d must fail closed: %v", height, err)
				}
			} else if err != nil {
				t.Fatalf("test-scope upgrade affected %s at %d: %v", registry.RulesScope, height, err)
			}
		}
	}

	var first, other *btcActivationRegistry
	for _, registry := range registries {
		if first == nil {
			first = registry
		} else if registry.RulesScope != first.RulesScope {
			other = registry
		}
	}
	selector := newTestSelector(t, 0)
	point, err := first.lookup(selector.BTCHeight)
	if err != nil {
		t.Fatal(err)
	}
	view := newTestProfileView(t, selector, "1", "0")
	view.ExternalState.ActivationRegistryID = first.ActivationRegistryID
	view.ExternalState.ActiveVersionSet = point.ActiveVersionSet
	view.ExternalState.ActiveVersionSetID = point.ActiveVersionSetID
	client := &stubProfileClient{profile: view}
	if _, err := resolveConsensusProfile(context.Background(), client, first, selector); err != nil {
		t.Fatalf("scoped consensus profile failed: %v", err)
	}
	if client.lastQuery.ExpectedState.ActivationRegistryID != first.ActivationRegistryID || client.lastQuery.ExpectedState.ActiveVersionSetID != point.ActiveVersionSetID {
		t.Fatal("RPC query failed to pin scoped registry/version identities")
	}
	if _, err := resolveConsensusProfile(context.Background(), client, other, selector); !errors.Is(err, ErrProfileStateMismatch) {
		t.Fatalf("same BTC state from other rule scope accepted: %v", err)
	}
	// Relabeling a foreign response with the requested registry ID must still
	// fail because the active-version-set hash independently commits its scope.
	view.ExternalState.ActivationRegistryID = other.ActivationRegistryID
	if _, err := resolveConsensusProfile(context.Background(), client, other, selector); !errors.Is(err, ErrProfileStateMismatch) {
		t.Fatalf("foreign scoped version set accepted after registry relabeling: %v", err)
	}

	for _, mutate := range []struct {
		name  string
		apply func(*btcActivationGoldenArtifact)
	}{
		{name: "registry scope substitution", apply: func(a *btcActivationGoldenArtifact) { a.Registries[0].RulesScope = "different-scope" }},
		{name: "source substitution", apply: func(a *btcActivationGoldenArtifact) { a.Registries[0].NetworkID = "btc-regtest" }},
		{name: "scope omitted", apply: func(a *btcActivationGoldenArtifact) { a.Registries[0].RulesScope = "" }},
		{name: "legacy schema downgrade", apply: func(a *btcActivationGoldenArtifact) {
			a.SchemaVersion = goActivationGoldenSchemaVersion
			a.SourceRegistrySchemaVersion = btcActivationRegistrySchemaV2
		}},
		{name: "invalid current revision", apply: func(a *btcActivationGoldenArtifact) {
			for i := range a.Registries {
				a.Registries[i].Current = false
			}
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			var artifact btcActivationGoldenArtifact
			if err := json.Unmarshal(blob, &artifact); err != nil {
				t.Fatal(err)
			}
			mutate.apply(&artifact)
			tampered, err := json.Marshal(artifact)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseBTCActivationGolden(tampered); err == nil {
				t.Fatal("invalid scoped golden accepted")
			}
		})
	}
}

func TestMinerPassV2GoldenUsesHistoricalHeightAndPreservesEmbeddedRegistry(t *testing.T) {
	blob, err := os.ReadFile("testdata/miner_pass_v2_activation_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	registries, err := parseBTCActivationGolden(blob)
	if err != nil {
		t.Fatal(err)
	}
	for _, registry := range registries {
		for _, height := range []uint32{11, 9, 10, 0} {
			point, err := registry.lookup(height)
			if err != nil {
				t.Fatal(err)
			}
			expected := PassStateMachineVersionV2
			state, err := point.ActiveVersionSet.requireStringVersion("pass_state_machine_version")
			if err != nil || state != expected {
				t.Fatalf("height=%d state=%s err=%v", height, state, err)
			}
			id, err := point.ActiveVersionSet.ID()
			if err != nil || id != point.ActiveVersionSetID {
				t.Fatalf("Rust/Go identity mismatch at %d: %v", height, err)
			}
		}
		if _, err := loadBTCActivationRegistry(registry.ActivationRegistryID); err != nil {
			t.Fatal(err)
		}
		if params.USDBChainConfig.USDB.Activations[0].BTCActivationRegistryID == registry.ActivationRegistryID {
			t.Fatal("development scope must require explicit selection")
		}
	}
}

// Frozen IDs remain readable for diagnostics, but cannot authorize a profile.
func TestLegacyMinerPassRegistryCannotExecute(t *testing.T) {
	for _, id := range []string{BTCMainnetActivationRegistryIDV1, BTCRegtestActivationRegistryIDV1, BTCRegtestActivationRegistryIDRevision2} {
		registry, err := loadBTCActivationRegistry(id)
		if err != nil {
			t.Fatal(err)
		}
		point, err := registry.lookup(123)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := registry.validateIdentity(123, id, point.ActiveVersionSet, point.ActiveVersionSetID); err == nil {
			t.Fatalf("legacy registry executed: %s", id)
		}
	}
}

func TestTestnetV1RegistryIsIndependentOnBTCMainnet(t *testing.T) {
	registry, err := loadBTCActivationRegistry(BTCTestnetV1ActivationRegistryID)
	if err != nil {
		t.Fatal(err)
	}
	if registry.NetworkID != "btc-mainnet" || registry.RulesScope != "usdb-testnet-v1" || registry.StableLagBlocks != 10 {
		t.Fatalf("unexpected testnet-v1 scope: %+v", registry)
	}
	legacy, err := loadBTCActivationRegistry(BTCMainnetActivationRegistryIDV1)
	if err != nil {
		t.Fatal(err)
	}
	for _, height := range []uint32{0, 963800, 963810, ^uint32(0)} {
		point, err := registry.lookup(height)
		if err != nil {
			t.Fatal(err)
		}
		if string(point.ActiveVersionSet["inscription_schema_version"]) != `"uip-0001-miner-pass-inscription:v1"` ||
			string(point.ActiveVersionSet["pass_state_machine_version"]) != `"uip-0002-pass-state-machine:v2"` {
			t.Fatalf("V2 is not active at height %d: %+v", height, point)
		}
		old, err := legacy.lookup(height)
		if err != nil {
			t.Fatal(err)
		}
		if old.ActiveVersionSetID == point.ActiveVersionSetID || legacy.RulesScope != "" {
			t.Fatal("v1 scope leaked into legacy registry")
		}
	}
}
