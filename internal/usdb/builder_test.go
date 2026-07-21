package usdb

import (
	"context"
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/params"
)

func testBuilderChainConfig(payloadVersion byte, difficultyPolicyVersion uint16) *params.ChainConfig {
	return &params.ChainConfig{USDB: &params.USDBConsensusConfig{
		PayloadVersion:          payloadVersion,
		DifficultyPolicyVersion: difficultyPolicyVersion,
	}}
}

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
	builder, err := NewPayloadBuilder(client, selector.PassID.String(), testBuilderChainConfig(ProfileSelectorPayloadVersionV1, 7), 0)
	if err != nil {
		t.Fatalf("failed to build payload builder: %v", err)
	}

	encoded, err := builder.BuildCurrentPayload(context.Background(), 42)
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
			builder, err := NewPayloadBuilder(client, selector.PassID.String(), testBuilderChainConfig(ProfileSelectorPayloadVersionV1, DifficultyPolicyVersionV1), 0)
			if err != nil {
				t.Fatalf("failed to build payload builder: %v", err)
			}
			if _, err := builder.BuildCurrentPayload(context.Background(), 42); !errors.Is(err, ErrSelectedPassNotCandidate) {
				t.Fatalf("expected candidate error, got %v", err)
			}
		})
	}
}

func TestPayloadBuilderRejectsUnavailableConsensusPolicy(t *testing.T) {
	selector := newTestSelector(t, 123)
	if _, err := NewPayloadBuilder(&stubProfileClient{}, selector.PassID.String(), nil, 0); err == nil {
		t.Fatal("expected nil chain config to be rejected")
	}
	tests := []struct {
		name   string
		config *params.ChainConfig
	}{
		{name: "inactive", config: &params.ChainConfig{}},
		{name: "zero payload version", config: testBuilderChainConfig(0, DifficultyPolicyVersionV1)},
		{name: "zero difficulty policy", config: testBuilderChainConfig(ProfileSelectorPayloadVersionV1, 0)},
		{name: "unsupported payload version", config: testBuilderChainConfig(2, DifficultyPolicyVersionV1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder, err := NewPayloadBuilder(&stubProfileClient{}, selector.PassID.String(), test.config, 0)
			if err != nil {
				t.Fatalf("failed to construct builder: %v", err)
			}
			if _, err := builder.BuildCurrentPayload(context.Background(), 42); err == nil {
				t.Fatal("expected unavailable consensus policy to stop payload generation")
			}
		})
	}
}

func TestPayloadBuilderRejectsUnavailableCurrentStateAndProfile(t *testing.T) {
	selector := newTestSelector(t, 123)
	config := testBuilderChainConfig(ProfileSelectorPayloadVersionV1, DifficultyPolicyVersionV1)
	tests := []struct {
		name   string
		client *stubProfileClient
	}{
		{name: "missing current state", client: &stubProfileClient{}},
		{name: "missing profile", client: &stubProfileClient{system: &SystemStateInfo{
			LocalSyncedBlockHeight: selector.BTCHeight,
			UpstreamSnapshotID:     selector.SnapshotIDHex(),
			SystemStateID:          selector.SystemStateIDHex(),
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder, err := NewPayloadBuilder(test.client, selector.PassID.String(), config, 0)
			if err != nil {
				t.Fatalf("failed to construct builder: %v", err)
			}
			if _, err := builder.BuildCurrentPayload(context.Background(), 42); err == nil {
				t.Fatal("expected unavailable current profile state to stop payload generation")
			}
		})
	}
}
