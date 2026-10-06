//go:build !usdb_miner_pass_conformance
// +build !usdb_miner_pass_conformance

package usdb

// Ordinary binaries never execute the synthetic BTC upgrade formulas.
func resolveMinerPassUpgradeFormulaValues(set ActiveVersionSet, profile PassEconomicProfile) (*profileFormulaValues, error) {
	return nil, nil
}
