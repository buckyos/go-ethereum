//go:build usdb_miner_pass_conformance
// +build usdb_miner_pass_conformance

package usdb

import (
	_ "embed"
	"fmt"
)

// Only the explicitly tagged regtest acceptance binary knows these formula names.
func resolveMinerPassUpgradeFormulaValues(set ActiveVersionSet, profile PassEconomicProfile) (*profileFormulaValues, error) {
	scope, err := set.rulesScope()
	if err != nil {
		return nil, err
	}
	if scope == nil || scope.NetworkID != "btc-regtest" || scope.RulesScope != "miner-pass-upgrade-conformance" {
		return nil, nil
	}
	for _, contract := range []struct {
		family    string
		supported map[string]bool
	}{
		{"energy_formula_version", map[string]bool{EnergyFormulaVersionV1: true, "conformance-energy:double": true, "conformance-energy:triple": true}},
		{"effective_energy_formula_version", map[string]bool{EffectiveEnergyFormulaVersionV1: true, "conformance-effective:quarter-collab": true}},
		{"level_formula_version", map[string]bool{LevelFormulaVersionV1: true, "conformance-level:thousands": true}},
	} {
		family, supported := contract.family, contract.supported
		value, err := set.requireStringVersion(family)
		if err != nil {
			return nil, err
		}
		if !supported[value] {
			return nil, fmt.Errorf("%w: %s=%q", ErrUnsupportedBTCFormulaVersion, family, value)
		}
	}
	values := &profileFormulaValues{}
	if values.raw, err = parseEnergyDecimal("raw_energy", profile.RawEnergy); err != nil {
		return nil, err
	}
	if values.collab, err = parseEnergyDecimal("collab_contribution", profile.CollabContribution); err != nil {
		return nil, err
	}
	if values.effective, err = parseEnergyDecimal("effective_energy", profile.EffectiveEnergy); err != nil {
		return nil, err
	}
	if values.effective.Cmp(saturatingAddEnergy(values.raw, values.collab)) != 0 {
		return nil, fmt.Errorf("%w: effective_energy have %s want %s", ErrProfileDerivedValueMismatch, values.effective, saturatingAddEnergy(values.raw, values.collab))
	}
	levelVersion, _ := set.requireStringVersion("level_formula_version")
	if levelVersion == LevelFormulaVersionV1 {
		values.level = LevelForEffectiveEnergy(values.effective)
	} else if !values.effective.IsUint64() || values.effective.Uint64() >= 50000 {
		values.level = 50
	} else {
		values.level = uint8(values.effective.Uint64() / 1000)
	}
	values.factor = 10000 - 100*uint64(values.level)
	if profile.Level != values.level || profile.DifficultyFactorBps != values.factor {
		return nil, fmt.Errorf("%w: level have %d want %d; difficulty_factor_bps have %d want %d", ErrProfileDerivedValueMismatch, profile.Level, values.level, profile.DifficultyFactorBps, values.factor)
	}
	return values, nil
}

//go:embed testdata/miner_pass_live_activation_golden.json
var minerPassLiveGolden []byte

// Only this tagged binary loads the immutable service-test catalog.
func minerPassConformanceCatalogs() [][]byte { return [][]byte{minerPassLiveGolden} }
func supportsMinerPassConformanceContract(set ActiveVersionSet, family, value string) bool {
	scope, err := set.rulesScope()
	if err != nil || scope == nil || scope.NetworkID != "btc-regtest" || scope.RulesScope != "miner-pass-upgrade-conformance" {
		return false
	}
	return family == "inscription_schema_version" && value == "conformance-miner-pass-schema:901" ||
		family == "pass_state_machine_version" && value == "conformance-miner-pass-state:no-new-collab"
}
