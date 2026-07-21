package usdb

import (
	"context"
	"errors"
	"testing"
)

func TestPayloadBuilderBuildsValidatedCurrentProfileSelector(t *testing.T) {
	selector := newTestSelector(t, 123)
	client := &stubProfileClient{
		system: &SystemStateInfo{
			LocalSyncedBlockHeight: 123,
			UpstreamSnapshotID:     selector.SnapshotIDHex(),
			SystemStateID:          selector.SystemStateIDHex(),
		},
		profile: newTestProfileView(t, selector, "1000000", "500000"),
	}
	builder, err := NewPayloadBuilder(client, selector.PassID.String(), 7, 0)
	if err != nil {
		t.Fatalf("failed to build payload builder: %v", err)
	}

	encoded, err := builder.BuildCurrentPayload(context.Background())
	if err != nil {
		t.Fatalf("failed to build profile selector: %v", err)
	}
	if len(encoded) != ProfileSelectorPayloadV1Size {
		t.Fatalf("unexpected payload size: have %d want %d", len(encoded), ProfileSelectorPayloadV1Size)
	}

	var payload ProfileSelectorPayload
	if err := payload.UnmarshalBinary(encoded); err != nil {
		t.Fatalf("failed to decode profile selector: %v", err)
	}
	if payload.BTCHeight != 123 || payload.DifficultyPolicyVersion != 7 {
		t.Fatalf("unexpected selector versions/heights: %+v", payload)
	}
	if payload.PassID != selector.PassID {
		t.Fatalf("unexpected pass id: have %s want %s", payload.PassID.String(), selector.PassID.String())
	}
	if client.lastPassID != selector.PassID {
		t.Fatalf("unexpected profile query pass id: have %s want %s", client.lastPassID.String(), selector.PassID.String())
	}
	if client.lastQuery.RequestedHeight != selector.BTCHeight ||
		client.lastQuery.ExpectedState.SnapshotID != selector.SnapshotIDHex() ||
		client.lastQuery.ExpectedState.SystemStateID != selector.SystemStateIDHex() {
		t.Fatalf("profile query was not pinned to selector state: %+v", client.lastQuery)
	}
}

func TestPayloadBuilderRejectsNonCandidateConfiguredPass(t *testing.T) {
	selector := newTestSelector(t, 123)
	tests := []struct {
		name  string
		state string
		kind  string
	}{
		{name: "dormant standard", state: "dormant", kind: passKindStandard},
		{name: "active collab", state: passStateActive, kind: "collab"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := newTestProfileView(t, selector, "1000000", "0")
			profile.Pass.State = test.state
			profile.Pass.PassKind = test.kind
			client := &stubProfileClient{
				system: &SystemStateInfo{
					LocalSyncedBlockHeight: selector.BTCHeight,
					UpstreamSnapshotID:     selector.SnapshotIDHex(),
					SystemStateID:          selector.SystemStateIDHex(),
				},
				profile: profile,
			}
			builder, err := NewPayloadBuilder(client, selector.PassID.String(), DifficultyPolicyVersionV1, 0)
			if err != nil {
				t.Fatalf("failed to build payload builder: %v", err)
			}
			if _, err := builder.BuildCurrentPayload(context.Background()); !errors.Is(err, ErrSelectedPassNotCandidate) {
				t.Fatalf("expected candidate error, got %v", err)
			}
		})
	}
}

func TestNewPayloadBuilderRejectsZeroDifficultyPolicyVersion(t *testing.T) {
	selector := newTestSelector(t, 123)
	if _, err := NewPayloadBuilder(&stubProfileClient{}, selector.PassID.String(), 0, 0); err == nil {
		t.Fatal("expected zero difficulty policy version to be rejected")
	}
}
