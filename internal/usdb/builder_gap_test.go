package usdb

import (
	"context"
	"errors"
	"testing"
)

func newHistoricalBuilderFixture(t *testing.T) (*PayloadBuilder, *stubProfileClient, ProfileSelectorPayload) {
	t.Helper()
	selected := newTestSelector(t, 100)
	latest := newTestSelector(t, 102)
	latest.SystemStateID[0]++
	latest.SnapshotID[0]++
	client := &stubProfileClient{
		system:   newTestSystemStateInfo(t, latest),
		stateRef: testHistoricalStateRef(*newTestSystemStateInfo(t, selected)),
		profile:  newTestProfileView(t, selected, "1000000", "500000"),
	}
	builder, err := NewPayloadBuilder(client, testBuilderChainConfig(1, 1), 0, DefaultMinerAnchorGap)
	if err != nil {
		t.Fatal(err)
	}
	return builder, client, selected
}

func TestPayloadBuilderGapPinsHistoricalProfileAndRefreshHead(t *testing.T) {
	builder, client, selected := newHistoricalBuilderFixture(t)
	parent := testBuilderParentExtra(t, selected, 1, 0)
	built, err := builder.BuildCurrentPayload(context.Background(), 42, parent, testBuilderUSDBMain())
	if err != nil {
		t.Fatal(err)
	}
	var payload ProfileSelectorPayload
	if err := payload.UnmarshalBinary(built.Payload); err != nil {
		t.Fatal(err)
	}
	if payload.BTCHeight != 100 || payload.SystemStateID != selected.SystemStateID || payload.SnapshotID != selected.SnapshotID || payload.BTCAnchorAgeBlocks != 1 || built.AnchorSelection.ActualGap != 2 {
		t.Fatalf("wrong historical selector: %+v selection=%+v", payload, built.AnchorSelection)
	}
	if len(client.stateRefQueries) != 1 || client.stateRefQueries[0].ExpectedState.SystemStateID != selected.SystemStateIDHex() || client.lastQuery.ExpectedState.SystemStateID != selected.SystemStateIDHex() || client.lastQuery.ExpectedState.LocalStateCommit != client.stateRef.SystemStateInfo.LocalStateCommit {
		t.Fatal("historical identity was not pinned through both RPCs")
	}
	for i := 0; i < 3; i++ {
		if changed, err := builder.HasSystemStateChanged(context.Background()); err != nil || changed {
			t.Fatalf("historical selection caused refresh loop: %v %v", changed, err)
		}
	}
	if client.candidateCalls != 1 || len(client.stateRefQueries) != 1 {
		t.Fatal("lightweight polls queried history")
	}

	client.system.SystemStateID = repeatHex("88", 32)
	client.stateRefErr = ErrSnapshotNotReady
	if _, err := builder.BuildCurrentPayload(context.Background(), 43, built.Payload, testBuilderUSDBMain()); !errors.Is(err, ErrSnapshotNotReady) {
		t.Fatal(err)
	}
	if changed, err := builder.HasSystemStateChanged(context.Background()); err != nil || !changed {
		t.Fatalf("failed history query consumed refresh: %v %v", changed, err)
	}
	client.stateRefErr = nil
	if _, err := builder.BuildCurrentPayload(context.Background(), 43, built.Payload, testBuilderUSDBMain()); err != nil {
		t.Fatal(err)
	}
	if changed, err := builder.HasSystemStateChanged(context.Background()); err != nil || changed {
		t.Fatalf("successful retry did not settle: %v %v", changed, err)
	}
}

func TestPayloadBuilderGapRejectsMixedHistoryWithoutSearching(t *testing.T) {
	for _, test := range []struct {
		name           string
		mutate         func(*stubProfileClient)
		candidateCalls int
	}{
		{"ref height", func(c *stubProfileClient) { c.stateRef.BlockHeight++ }, 0},
		{"snapshot height", func(c *stubProfileClient) { c.stateRef.SnapshotInfo.LocalSyncedBlockHeight++ }, 0},
		{"upstream height", func(c *stubProfileClient) { c.stateRef.SnapshotInfo.BalanceHistoryStableHeight++ }, 0},
		{"local height", func(c *stubProfileClient) { c.stateRef.LocalStateCommitInfo.LocalSyncedBlockHeight++ }, 0},
		{"system height", func(c *stubProfileClient) { c.stateRef.SystemStateInfo.LocalSyncedBlockHeight++ }, 0},
		{"missing ID", func(c *stubProfileClient) { c.stateRef.SystemStateInfo.SystemStateID = "" }, 0},
		{"local snapshot", func(c *stubProfileClient) { c.stateRef.LocalStateCommitInfo.UpstreamSnapshotID = repeatHex("aa", 32) }, 0},
		{"system snapshot", func(c *stubProfileClient) { c.stateRef.SystemStateInfo.UpstreamSnapshotID = repeatHex("aa", 32) }, 0},
		{"local commit", func(c *stubProfileClient) { c.stateRef.LocalStateCommitInfo.LocalStateCommit = repeatHex("aa", 32) }, 0},
		{"parent identity", func(c *stubProfileClient) { c.stateRef.SystemStateInfo.SystemStateID = repeatHex("aa", 32) }, 0},
		{"local registry", func(c *stubProfileClient) {
			c.stateRef.LocalStateCommitInfo.ActivationRegistryID = BTCRegtestMinerPassV2StagedRegistryID
		}, 0},
		{"system version", func(c *stubProfileClient) { c.stateRef.SystemStateInfo.ActiveVersionSetID = repeatHex("aa", 32) }, 0},
		{"current profile substituted", func(c *stubProfileClient) {
			c.profile.ExternalState.BTCHeight = 102
			c.profile.ExternalState.SystemStateID = c.system.SystemStateID
		}, 1},
		{"profile commit changed", func(c *stubProfileClient) { c.profile.ExternalState.LocalStateCommit = repeatHex("aa", 32) }, 1},
		{"profile block changed", func(c *stubProfileClient) { c.profile.ExternalState.StableBlockHash = repeatHex("aa", 32) }, 1},
		{"no candidate", func(c *stubProfileClient) { c.profile = nil }, 1},
		{"recovery during candidate", func(c *stubProfileClient) { c.candidateErr = ErrSnapshotNotReady }, 1},
		{"history not retained", func(c *stubProfileClient) { c.stateRefErr = ErrStateNotRetained }, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder, client, selected := newHistoricalBuilderFixture(t)
			test.mutate(client)
			if _, err := builder.BuildCurrentPayload(context.Background(), 42, testBuilderParentExtra(t, selected, 1, 0), testBuilderUSDBMain()); err == nil {
				t.Fatal("invalid history accepted")
			}
			if len(client.stateRefQueries) != 1 || client.candidateCalls != test.candidateCalls {
				t.Fatal("builder searched another height")
			}
			if changed, err := builder.HasSystemStateChanged(context.Background()); err != nil || !changed {
				t.Fatal("failure consumed refresh baseline")
			}
		})
	}
}

func TestPayloadBuilderGapAllowsHeadProgressDuringHistoricalQuery(t *testing.T) {
	builder, client, selected := newHistoricalBuilderFixture(t)
	client.stateRefHook = func() {
		next := *client.system
		next.LocalSyncedBlockHeight++
		next.SystemStateID = repeatHex("88", 32)
		client.system = &next
	}
	if _, err := builder.BuildCurrentPayload(context.Background(), 42, testBuilderParentExtra(t, selected, 1, 0), testBuilderUSDBMain()); err != nil {
		t.Fatal(err)
	}
	if changed, err := builder.HasSystemStateChanged(context.Background()); err != nil || !changed {
		t.Fatal("mid-query head advance was swallowed")
	}
}

func TestPayloadBuilderGapRecomputesForNewBranchParent(t *testing.T) {
	builder, client, selected := newHistoricalBuilderFixture(t)
	higher := newTestSelector(t, 101)
	client.profile = newTestProfileView(t, higher, "0", "0")
	client.stateRef = testHistoricalStateRef(*newTestSystemStateInfo(t, higher))
	if _, err := builder.BuildCurrentPayload(context.Background(), 42, testBuilderParentExtra(t, higher, 1, 0), testBuilderUSDBMain()); err != nil {
		t.Fatal(err)
	}
	// A different legal branch may have a lower parent; there is no global high-water mark.
	client.profile = newTestProfileView(t, selected, "0", "0")
	client.stateRef = testHistoricalStateRef(*newTestSystemStateInfo(t, selected))
	built, err := builder.BuildCurrentPayload(context.Background(), 42, testBuilderParentExtra(t, selected, 1, 0), testBuilderUSDBMain())
	if err != nil || built.AnchorSelection.SelectedHeight != 100 {
		t.Fatalf("new parent ignored: %+v %v", built, err)
	}
}

func TestPayloadBuilderGapUsesChildCheckpointAgeLimit(t *testing.T) {
	builder, client, selected := newHistoricalBuilderFixture(t)
	config := builder.chainConfig
	config.USDB.Activations[0].BTCAnchorMaxAgeBlocks = 5
	after := config.USDB.Activations[0]
	after.Block, after.BTCAnchorMaxAgeBlocks = 42, 1
	config.USDB.Activations = append(config.USDB.Activations, after)
	parent := testBuilderParentExtra(t, selected, 1, 1)
	before, err := builder.BuildCurrentPayload(context.Background(), 41, parent, testBuilderUSDBMain())
	if err != nil || before.AnchorSelection.SelectedHeight != 100 {
		t.Fatalf("before activation: %+v %v", before, err)
	}
	next := newTestSelector(t, 101)
	client.stateRef = testHistoricalStateRef(*newTestSystemStateInfo(t, next))
	client.profile = newTestProfileView(t, next, "0", "0")
	built, err := builder.BuildCurrentPayload(context.Background(), 42, parent, testBuilderUSDBMain())
	if err != nil || built.AnchorSelection.SelectedHeight != 101 || built.AnchorSelection.Reason != "age_limit" {
		t.Fatalf("child checkpoint did not override preferred gap: %+v %v", built, err)
	}
}
