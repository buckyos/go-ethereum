//go:build usdb_miner_pass_conformance
// +build usdb_miner_pass_conformance

package usdb

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMinerPassUpgradeRustRPCVectors(t *testing.T) {
	data := loadUpgradeVectors(t)
	for index, original := range data.Profiles {
		t.Run(fmt.Sprint(original.ExternalState.BTCHeight), func(t *testing.T) {
			selector := selectorForUpgradeView(t, original)
			point, err := data.Registry.lookup(selector.BTCHeight)
			if err != nil {
				t.Fatal(err)
			}
			// Exercise the actual Go HTTP JSON-RPC client against serialized Rust responses.
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					ID     json.RawMessage             `json:"id"`
					Method string                      `json:"method"`
					Params []passEconomicProfileParams `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					http.Error(w, "bad request", 400)
					return
				}
				if request.Method != "get_pass_economic_profile" || len(request.Params) != 1 {
					t.Error("unexpected RPC request")
					http.Error(w, "bad method", 400)
					return
				}
				query := request.Params[0]
				if query.PassID != original.Pass.PassID || query.Context.RequestedHeight != selector.BTCHeight || query.Context.ExpectedState.ActivationRegistryID != data.Registry.ActivationRegistryID || query.Context.ExpectedState.ActiveVersionSetID != point.ActiveVersionSetID {
					t.Errorf("wrong historical selector: %+v", query)
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": request.ID, "result": original})
			}))
			defer endpoint.Close()
			client, err := DialRPC(context.Background(), endpoint.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			resolved, err := resolveConsensusProfile(context.Background(), client, &data.Registry, selector)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.RawEnergy.String() != original.Pass.RawEnergy || resolved.Level != original.Pass.Level {
				t.Fatal("wire profile changed")
			}
			breakdown := data.Breakdowns[index]
			weight := int64(5000)
			if selector.BTCHeight >= 13 {
				weight = 2500
			}
			total := new(big.Int)
			for _, item := range breakdown.Items {
				raw, ok := new(big.Int).SetString(item.Raw, 10)
				if !ok {
					t.Fatal("invalid raw")
				}
				expected := new(big.Int).Div(new(big.Int).Mul(raw, big.NewInt(weight)), big.NewInt(10000))
				if item.Weight != uint64(weight) || item.Contribution != expected.String() {
					t.Fatal("Rust collab weight/rounding mismatch")
				}
				total.Add(total, expected)
			}
			if total.String() != breakdown.Aggregate || total.Cmp(resolved.CollabContribution) != 0 {
				t.Fatal("Rust/Go collab aggregate mismatch")
			}
			for _, mutate := range []func(*PassEconomicProfileView){
				func(v *PassEconomicProfileView) { v.Pass.RawEnergy = "01" },
				func(v *PassEconomicProfileView) { v.Pass.RawEnergy = new(big.Int).Lsh(big.NewInt(1), 128).String() },
				func(v *PassEconomicProfileView) { v.Pass.EffectiveEnergy = "0" },
				func(v *PassEconomicProfileView) { v.Pass.Level++ },
				func(v *PassEconomicProfileView) { v.Pass.DifficultyFactorBps++ },
				func(v *PassEconomicProfileView) { v.ExternalState.ActivationRegistryID = strings.Repeat("a", 64) },
				func(v *PassEconomicProfileView) { v.ExternalState.ActiveVersionSetID = strings.Repeat("b", 64) },
				func(v *PassEconomicProfileView) { v.ExternalState.BTCHeight++ },
				func(v *PassEconomicProfileView) { v.ExternalState.SystemStateID = strings.Repeat("c", 64) },
			} {
				bad := original
				mutate(&bad)
				if _, err := resolveConsensusProfileView(&data.Registry, selector, &bad); err == nil {
					t.Fatal("accepted tampered Rust profile")
				}
			}
		})
	}
}

func TestMinerPassUpgradeRulesStayScopedAndBounded(t *testing.T) {
	data := loadUpgradeVectors(t)
	original := data.Profiles[len(data.Profiles)-1]
	for _, scope := range []string{
		`{"network_id":"btc-mainnet","rules_scope":"miner-pass-upgrade-conformance"}`,
		`{"network_id":"btc-regtest","rules_scope":"some-other-network"}`,
	} {
		encoded, _ := json.Marshal(original.ExternalState.ActiveVersionSet)
		var set ActiveVersionSet
		json.Unmarshal(encoded, &set)
		set["scope"] = json.RawMessage(scope)
		if _, _, _, _, _, err := resolveProfileFormulaValues(set, original.Pass); err == nil {
			t.Fatal("conformance rules escaped their scope")
		}
	}
	profile := original.Pass
	profile.RawEnergy = maximumEnergyValue.String()
	profile.CollabContribution = "1"
	profile.EffectiveEnergy = maximumEnergyValue.String()
	profile.Level = 50
	profile.DifficultyFactorBps = 5000
	if _, _, _, _, _, err := resolveProfileFormulaValues(original.ExternalState.ActiveVersionSet, profile); err != nil {
		t.Fatal(err)
	}
	for _, energy := range []uint64{0, 999, 1000, 49999, 50000} {
		profile.RawEnergy = fmt.Sprint(energy)
		profile.CollabContribution = "0"
		profile.EffectiveEnergy = profile.RawEnergy
		level := energy / 1000
		if level > 50 {
			level = 50
		}
		profile.Level = uint8(level)
		profile.DifficultyFactorBps = 10000 - 100*level
		if _, _, _, _, _, err := resolveProfileFormulaValues(original.ExternalState.ActiveVersionSet, profile); err != nil {
			t.Fatal(err)
		}
	}
}
