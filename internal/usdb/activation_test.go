package usdb

import (
	"encoding/json"
	"testing"
)

func TestActiveVersionSetIDIsStableAcrossMapOrder(t *testing.T) {
	first := newTestActiveVersionSet(t)
	second := make(ActiveVersionSet, len(first))
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
	const expectedID = "01d1d45f342994690d8ae27ac3d8538ad31e5f81f8e948c838067b3b52f94691"
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
		t.Fatalf("expected BTC v1 set to validate: %v", err)
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
