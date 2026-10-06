package usdb

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestCurrentIdentityRejectsDivergentIntermediateHistory(t *testing.T) {
	actual, err := loadBTCActivationRegistry(BTCRegtestMinerPassV2RegistryID)
	if err != nil {
		t.Fatal(err)
	}
	base, err := actual.lookup(22)
	if err != nil {
		t.Fatal(err)
	}
	changed := newTestActiveVersionSet(t)
	changed["effective_energy_formula_version"] = json.RawMessage(`"test-only-intermediate-version"`)
	changedID, err := changed.ID()
	if err != nil {
		t.Fatal(err)
	}
	expected := *actual
	expected.ActivationRegistryID = strings.Repeat("c", 64)
	expected.Activations = []btcActivationPoint{actual.Activations[0], {BTCHeight: 10, ActiveVersionSet: changed, ActiveVersionSetID: changedID}, {BTCHeight: 20, ActiveVersionSet: base.ActiveVersionSet, ActiveVersionSetID: base.ActiveVersionSetID}}
	for _, test := range []struct {
		origin, height uint32
		wantError      bool
	}{{1, 9, false}, {1, 10, true}, {1, 19, true}, {1, 20, true}, {1, 22, true}, {20, 22, false}, {20, math.MaxUint32, false}} {
		err := validateCurrentActivationIdentity(test.origin, test.height, base.ActiveVersionSet, base.ActiveVersionSetID, actual.ActivationRegistryID, &expected)
		if errors.Is(err, ErrBTCActivationRegistryMismatch) != test.wantError || !test.wantError && err != nil {
			t.Fatalf("origin=%d height=%d: %v", test.origin, test.height, err)
		}
		if test.wantError && !strings.Contains(err.Error(), "first_difference_height=10") {
			t.Fatal(err)
		}
	}
	expected.StableLagBlocks++
	if err := actual.ensureSameHistory(&expected, 20, 22); !errors.Is(err, ErrBTCActivationRegistryMismatch) {
		t.Fatal(err)
	}
}
