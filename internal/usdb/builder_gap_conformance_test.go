//go:build usdb_miner_pass_conformance
// +build usdb_miner_pass_conformance

package usdb

import (
	"context"
	"fmt"
	"testing"
)

func TestPayloadBuilderGapSeparatesBTCAndUSDBActivation(t *testing.T) {
	registry := loadMinerPassServiceRegistry(t)
	for _, latest := range []uint32{161, 164, 167, 171, 173, 181, 211} {
		t.Run(fmt.Sprint(latest), func(t *testing.T) {
			selected := newTestSelector(t, latest-2)
			head := newTestSelector(t, latest)
			head.SnapshotID[0]++
			head.SystemStateID[0]++
			stateAt := func(selector ProfileSelectorPayload) *SystemStateInfo {
				point, err := registry.lookup(selector.BTCHeight)
				if err != nil {
					t.Fatal(err)
				}
				info := newTestSystemStateInfo(t, selector)
				info.ActivationRegistryID = registry.ActivationRegistryID
				info.ActiveVersionSet, info.ActiveVersionSetID = point.ActiveVersionSet, point.ActiveVersionSetID
				return info
			}
			historical, current := stateAt(selected), stateAt(head)
			if historical.ActiveVersionSetID == current.ActiveVersionSetID {
				t.Fatal("fixture must cross BTC activation")
			}
			profile := newTestProfileView(t, selected, "1000", "0")
			profile.ExternalState.ActivationRegistryID = registry.ActivationRegistryID
			profile.ExternalState.ActiveVersionSet = historical.ActiveVersionSet
			profile.ExternalState.ActiveVersionSetID = historical.ActiveVersionSetID
			if selected.BTCHeight >= 166 {
				profile.Pass.Level = 1
				profile.Pass.DifficultyFactorBps = 9900
			}
			client := &stubProfileClient{system: current, stateRef: testHistoricalStateRef(*historical), profile: profile}
			config := testBuilderChainConfig(1, 1)
			config.USDB.Activations[0].BTCActivationRegistryID = registry.ActivationRegistryID
			after := config.USDB.Activations[0]
			after.Block = 42
			after.Versions.DifficultyPolicyVersion = DifficultyPolicyVersionActivationConformance
			config.USDB.Activations = append(config.USDB.Activations, after)
			builder, err := NewPayloadBuilder(client, config, 0, 2)
			if err != nil {
				t.Fatal(err)
			}
			for _, step := range []struct {
				block   uint64
				version uint16
			}{{41, 1}, {42, DifficultyPolicyVersionActivationConformance}} {
				built, err := builder.BuildCurrentPayload(context.Background(), step.block, testBuilderParentExtra(t, selected, 1, 0), testBuilderUSDBMain())
				if err != nil {
					t.Fatal(err)
				}
				var payload ProfileSelectorPayload
				if err := payload.UnmarshalBinary(built.Payload); err != nil {
					t.Fatal(err)
				}
				if payload.BTCHeight != latest-2 || payload.DifficultyPolicyVersion != step.version || client.lastQuery.ExpectedState.ActiveVersionSetID != historical.ActiveVersionSetID {
					t.Fatalf("activation domains mixed: payload=%+v query=%+v", payload, client.lastQuery)
				}
			}
		})
	}
}

func TestPayloadBuilderGapDoesNotHideConflictingRegistryAtLatest(t *testing.T) {
	registry := loadMinerPassServiceRegistry(t)
	selected := newTestSelector(t, 159)
	latest := newTestSelector(t, 161)
	point, err := registry.lookup(161)
	if err != nil {
		t.Fatal(err)
	}
	current := newTestSystemStateInfo(t, latest)
	current.ActivationRegistryID = registry.ActivationRegistryID
	current.ActiveVersionSet, current.ActiveVersionSetID = point.ActiveVersionSet, point.ActiveVersionSetID
	client := &stubProfileClient{system: current}
	config := testBuilderChainConfig(1, 1)
	// The old catalog agrees at selected=159 but not through the observed tip=161.
	config.USDB.Activations[0].BTCActivationRegistryID = "96a345bae6b7c1c6c8c67b1dbb7e70ea6700b7a784ef94f7dceecc71bc9db092"
	builder, err := NewPayloadBuilder(client, config, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.BuildCurrentPayload(context.Background(), 42, testBuilderParentExtra(t, selected, 1, 0), testBuilderUSDBMain()); err == nil {
		t.Fatal("gap hid conflicting execution history")
	}
	if len(client.stateRefQueries) != 0 || client.candidateCalls != 0 {
		t.Fatal("conflicting registry reached history queries")
	}
}
