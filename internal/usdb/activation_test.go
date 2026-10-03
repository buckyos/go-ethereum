package usdb

import (
	"encoding/json"
	"testing"
)

func TestActiveVersionSetIDIsStableAcrossMapOrder(t *testing.T) {
	first := newTestActiveVersionSet(t)
	second := make(ActiveVersionSet, len(first))
	second["scope"] = append(json.RawMessage(nil), first["scope"]...)
	for index := len(activeVersionFamilies) - 1; index >= 0; index-- {
		family := activeVersionFamilies[index]
		if value, ok := first[family]; ok {
			second[family] = append(json.RawMessage(nil), value...)
		}
	}
	firstID, err := first.ID()
	if err != nil {
		t.Fatalf("failed to identify first set: %v", err)
	}
	secondID, err := second.ID()
	if err != nil {
		t.Fatalf("failed to identify second set: %v", err)
	}
	if firstID != secondID || len(firstID) != 64 {
		t.Fatalf("active version set id is unstable: first=%q second=%q", firstID, secondID)
	}
	const expectedID = "91c2b5b1fe9622d6f8a61561d2ef89a9d206d0d8d7f91e83f3b34dcdab2cad8c"
	if firstID != expectedID {
		t.Fatalf("active version set id changed: have %q want %q", firstID, expectedID)
	}
}

func TestActiveVersionSetDecoderRejectsUnknownAndDuplicateFamilies(t *testing.T) {
	for _, input := range []string{
		`{"unknown_version":"v1"}`,
		`{"energy_formula_version":"v1","energy_formula_version":"v2"}`,
		`{"energy_formula_version":true}`,
	} {
		var set ActiveVersionSet
		if err := json.Unmarshal([]byte(input), &set); err == nil {
			t.Fatalf("expected invalid active version set to fail: %s", input)
		}
	}
}

func TestActiveVersionSetValidatesBTCProfileSurface(t *testing.T) {
	set := newTestActiveVersionSet(t)
	if err := set.ValidateBTCProfileSurface(); err != nil {
		t.Fatalf("expected current BTC set to validate: %v", err)
	}

	set["energy_formula_version"] = json.RawMessage(`"v999"`)
	if err := set.ValidateBTCProfileSurface(); err != nil {
		t.Fatalf("formula dispatch should decide version support: %v", err)
	}

	set = newTestActiveVersionSet(t)
	delete(set, "query_semantics_version")
	if err := set.ValidateBTCProfileSurface(); err == nil {
		t.Fatal("expected missing family to fail")
	}

	set = newTestActiveVersionSet(t)
	set["payload_version"] = json.RawMessage(`1`)
	if err := set.ValidateBTCProfileSurface(); err == nil {
		t.Fatal("expected extra family to fail")
	}
}

func TestScopedActiveVersionSetSeparatesRuleHistories(t *testing.T) {
	legacy := newTestActiveVersionSet(t)
	legacyID, err := legacy.ID()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{legacyID: true}
	for _, scope := range []string{
		`{"network_id":"btc-mainnet","rules_scope":"usdb-mainnet-fixture"}`,
		`{"network_id":"btc-mainnet","rules_scope":"usdb-testnet-fixture"}`,
		`{"network_id":"btc-regtest","rules_scope":"usdb-testnet-fixture"}`,
	} {
		set := newTestActiveVersionSet(t)
		set["scope"] = json.RawMessage(scope)
		if err := set.ValidateBTCProfileSurface(); err != nil {
			t.Fatal(err)
		}
		id, err := set.ID()
		if err != nil {
			t.Fatal(err)
		}
		if ids[id] {
			t.Fatalf("independent source/rules scope shared active version set id %s", id)
		}
		ids[id] = true
	}
	set := newTestActiveVersionSet(t)
	set["scope"] = json.RawMessage(`{"network_id":"btc-mainnet","rules_scope":"usdb-testnet-fixture"}`)
	firstID, _ := set.ID()
	set["scope"] = json.RawMessage(`{"rules_scope":"usdb-testnet-fixture","network_id":"btc-mainnet"}`)
	secondID, _ := set.ID()
	if firstID != secondID {
		t.Fatal("scope object key order changed canonical identity")
	}
}

func TestActiveVersionSetRejectsMalformedScope(t *testing.T) {
	for _, scope := range []string{
		`null`, `"legacy"`, `{}`,
		`{"network_id":"btc-mainnet"}`,
		`{"network_id":"btc-mainnet","rules_scope":"legacy"}`,
		`{"network_id":"btc-mainnet","rules_scope":"Testnet"}`,
		`{"network_id":"btc-mainnet","rules_scope":"test--net"}`,
		`{"network_id":"btc-mainnet","rules_scope":"-testnet"}`,
		`{"network_id":"btc-mainnet","rules_scope":"testnet-"}`,
		`{"network_id":"btc-mainnet","rules_scope":"test_net"}`,
		`{"network_id":"unknown","rules_scope":"testnet"}`,
		`{"network_id":"btc-mainnet","rules_scope":"testnet","unknown":1}`,
		`{"network_id":"btc-mainnet","network_id":"btc-regtest","rules_scope":"testnet"}`,
		`{"network_id":"btc-mainnet","rules_scope":"testnet","rules_scope":"mainnet"}`,
	} {
		t.Run(scope, func(t *testing.T) {
			var decoded ActiveVersionSet
			if err := json.Unmarshal([]byte(`{"scope":`+scope+`}`), &decoded); err == nil {
				t.Fatal("malformed scope passed wire decoder")
			}
			set := newTestActiveVersionSet(t)
			set["scope"] = json.RawMessage(scope)
			if _, err := set.ID(); err == nil {
				t.Fatal("malformed scope received an identity")
			}
			if err := set.ValidateBTCProfileSurface(); err == nil {
				t.Fatal("malformed scope passed BTC profile surface validation")
			}
		})
	}
}

// Only the current pair is executable; neither old nor partial pairs select a fallback.
func TestMinerPassProfileRulePairs(t *testing.T) {
	for _, tc := range []struct {
		schema, state string
		valid         bool
	}{
		{InscriptionSchemaVersionV1, PassStateMachineVersionV1, false},
		{InscriptionSchemaVersionV2, PassStateMachineVersionV2, true},
		{InscriptionSchemaVersionV1, PassStateMachineVersionV2, false},
		{InscriptionSchemaVersionV2, PassStateMachineVersionV1, false},
		{"", PassStateMachineVersionV1, false},
		{InscriptionSchemaVersionV1, "", false},
		{"v999", PassStateMachineVersionV2, false},
	} {
		set := newTestActiveVersionSet(t)
		set["inscription_schema_version"], _ = json.Marshal(tc.schema)
		set["pass_state_machine_version"], _ = json.Marshal(tc.state)
		if err := set.ValidateBTCProfileSurface(); (err == nil) != tc.valid {
			t.Fatalf("schema=%q state=%q expected_valid=%v error=%v", tc.schema, tc.state, tc.valid, err)
		}
	}
}
