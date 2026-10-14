package usdb

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestPayloadBuilderDiagnosticsHistoryFailureAndRecovery(t *testing.T) {
	b, client, selected := newHistoricalBuilderFixture(t)
	parent := testBuilderParentExtra(t, selected, 1, 0)
	built, err := b.BuildCurrentPayload(context.Background(), 42, parent, testBuilderUSDBMain())
	if err != nil {
		t.Fatal(err)
	}
	status := b.LastBuildStatus()
	if status.Stage != "validated" || status.Selection.SelectedHeight != 100 || *status.LatestHeight != 102 || status.PassID == "" || status.SnapshotID != selected.SnapshotIDHex() || status.AnchorAge != 1 {
		t.Fatalf("incorrect historical diagnostics: %+v", status)
	}
	status.Selection.SelectedHeight = 0
	*status.LatestHeight = 0
	if b.LastBuildStatus().Selection.SelectedHeight != 100 || *built.Diagnostics.LatestHeight != 102 {
		t.Fatal("mutable snapshot alias")
	}
	client.candidateErr = ErrMinerCandidateNotFound
	if _, err := b.BuildCurrentPayload(context.Background(), 42, parent, testBuilderUSDBMain()); !errors.Is(err, ErrMinerCandidateNotFound) {
		t.Fatal(err)
	}
	status = b.LastBuildStatus()
	if status.Failure != "candidate_unavailable" || status.Stage != "candidate" || status.PassID != "" || status.Selection.SelectedHeight != 100 {
		t.Fatal(status)
	}
	client.candidateErr = errors.New("http://user:secret@internal/path?token=private")
	b.BuildCurrentPayload(context.Background(), 42, parent, testBuilderUSDBMain())
	encoded, _ := json.Marshal(b.LastBuildStatus())
	if strings.Contains(string(encoded), "secret") || !strings.Contains(string(encoded), "build_failed") {
		t.Fatal(string(encoded))
	}
	client.candidateErr = nil
	if _, err := b.BuildCurrentPayload(context.Background(), 42, parent, testBuilderUSDBMain()); err != nil {
		t.Fatal(err)
	}
	if b.LastBuildStatus().Failure != "" {
		t.Fatal("failure did not clear")
	}
}

func TestPayloadBuilderDiagnosticsConcurrentReadAndWaitHeight(t *testing.T) {
	b, _, selected := newHistoricalBuilderFixture(t)
	parent := testBuilderParentExtra(t, selected, 1, 0)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			b.LastBuildStatus()
		}
	}()
	for i := 0; i < 10; i++ {
		if _, err := b.BuildCurrentPayload(context.Background(), 42, parent, testBuilderUSDBMain()); err != nil {
			t.Error(err)
		}
	}
	wg.Wait()
	selected.BTCHeight = 103
	parent = testBuilderParentExtra(t, selected, 1, 0)
	if _, err := b.BuildCurrentPayload(context.Background(), 42, parent, testBuilderUSDBMain()); !errors.Is(err, ErrBTCAnchorHeightRegression) {
		t.Fatal(err)
	}
	status := b.LastBuildStatus()
	if status.Failure != "parent_state_unavailable" || *status.RequiredHeight != 103 || *status.LatestHeight != 102 || status.Selection != nil {
		t.Fatal(status)
	}
}
