//go:build !usdb_miner_pass_conformance
// +build !usdb_miner_pass_conformance

package usdb

import (
	"errors"
	"testing"
)

func TestProductionRejectsMinerPassUpgradeFormulas(t *testing.T) {
	data := loadUpgradeVectors(t)
	for _, view := range data.Profiles {
		_, err := resolveConsensusProfileView(&data.Registry, selectorForUpgradeView(t, view), &view)
		if view.ExternalState.BTCHeight < 10 {
			if err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, ErrUnsupportedBTCFormulaVersion) {
			t.Fatalf("height %d: %v", view.ExternalState.BTCHeight, err)
		}
	}
}

func TestProductionRejectsMinerPassServiceCatalog(t *testing.T) {
	registry := loadMinerPassServiceRegistry(t)
	if _, err := loadBTCActivationRegistry(registry.ActivationRegistryID); !errors.Is(err, ErrBTCActivationRegistryNotSupported) {
		t.Fatalf("ordinary binary registered the service-test catalog: %v", err)
	}
}
