package usdb

import (
	"context"
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

func testBuilderChainConfig(payloadVersion byte, difficultyPolicyVersion uint16) *params.ChainConfig {
	return &params.ChainConfig{USDB: &params.USDBConsensusConfig{
		BTCNetworkID:         "btc-regtest",
		BTCIndexOriginHeight: 1,
		Activations: []params.USDBConsensusActivation{{
			BTCActivationRegistryID: BTCRegtestActivationRegistryIDV1,
			BTCAnchorMaxAgeBlocks:   params.USDBDevelopmentBTCAnchorMaxAgeBlocks,
			Versions: params.USDBConsensusVersions{
				PayloadVersion:          payloadVersion,
				BTCAnchorPolicyVersion:  BTCAnchorPolicyVersionV1,
				DifficultyPolicyVersion: difficultyPolicyVersion,
			},
		}},
	}}
}

func testBuilderParentExtra(t *testing.T, selector ProfileSelectorPayload, difficultyPolicyVersion uint16, age uint32) []byte {
	t.Helper()
	selector.DifficultyPolicyVersion = difficultyPolicyVersion
	selector.BTCAnchorAgeBlocks = age
	return marshalTestSelector(t, selector)
}

func TestPayloadBuilderBuildsValidatedCurrentProfileSelector(t *testing.T) {
	selector := newTestSelector(t, 123)
	client := &stubProfileClient{
		system:  newTestSystemStateInfo(t, selector),
		profile: newTestProfileView(t, selector, "1000000", "500000"),
	}
	builder, err := NewPayloadBuilder(client, selector.PassID.String(), testBuilderChainConfig(ProfileSelectorPayloadVersionV1, 7), 0)
	if err != nil {
		t.Fatalf("failed to build payload builder: %v", err)
	}

	parentExtra := testBuilderParentExtra(t, selector, 7, 3)
	built, err := builder.BuildCurrentPayload(context.Background(), 42, parentExtra)
	if err != nil {
		t.Fatalf("failed to build profile selector: %v", err)
	}
	if len(built.Payload) != ProfileSelectorPayloadV1Size {
		t.Fatalf("unexpected payload size: have %d want %d", len(built.Payload), ProfileSelectorPayloadV1Size)
	}
	if built.RewardRecipient != common.HexToAddress("0x1111111111111111111111111111111111111111") {
		t.Fatalf("unexpected reward recipient: %s", built.RewardRecipient)
	}

	var payload ProfileSelectorPayload
	if err := payload.UnmarshalBinary(built.Payload); err != nil {
		t.Fatalf("failed to decode profile selector: %v", err)
	}
	if payload.BTCHeight != 123 || payload.BTCAnchorAgeBlocks != 4 || payload.DifficultyPolicyVersion != 7 {
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
		client.lastQuery.ExpectedState.ActivationRegistryID != BTCRegtestActivationRegistryIDV1 ||
		client.lastQuery.ExpectedState.ActiveVersionSetID != client.profile.ExternalState.ActiveVersionSetID ||
		client.lastQuery.ExpectedState.SystemStateID != selector.SystemStateIDHex() {
		t.Fatalf("profile query was not pinned to selector state: %+v", client.lastQuery)
	}
}

func TestPayloadBuilderDerivesAndEnforcesBTCAnchorAge(t *testing.T) {
	current := newTestSelector(t, 123)
	build := func(
		t *testing.T,
		config *params.ChainConfig,
		systemSelector ProfileSelectorPayload,
		parentExtra []byte,
	) (*BuiltProfileSelector, error) {
		t.Helper()
		client := &stubProfileClient{
			system:  newTestSystemStateInfo(t, systemSelector),
			profile: newTestProfileView(t, systemSelector, "0", "0"),
		}
		builder, err := NewPayloadBuilder(client, systemSelector.PassID.String(), config, 0)
		if err != nil {
			t.Fatalf("construct builder: %v", err)
		}
		return builder.BuildCurrentPayload(context.Background(), 42, parentExtra)
	}

	config := testBuilderChainConfig(ProfileSelectorPayloadVersionV1, DifficultyPolicyVersionV1)
	advancedParent := current
	advancedParent.BTCHeight--
	built, err := build(
		t,
		config,
		current,
		testBuilderParentExtra(t, advancedParent, DifficultyPolicyVersionV1, 12),
	)
	if err != nil {
		t.Fatalf("build with newer BTC height: %v", err)
	}
	var advanced ProfileSelectorPayload
	if err := advanced.UnmarshalBinary(built.Payload); err != nil {
		t.Fatalf("decode advanced selector: %v", err)
	}
	if advanced.BTCAnchorAgeBlocks != 0 {
		t.Fatalf("new BTC height did not reset age: %d", advanced.BTCAnchorAgeBlocks)
	}

	firstConfig := testBuilderChainConfig(ProfileSelectorPayloadVersionV1, DifficultyPolicyVersionV1)
	firstConfig.USDB.Activations[0].Block = 42
	built, err = build(t, firstConfig, current, nil)
	if err != nil {
		t.Fatalf("build first active selector: %v", err)
	}
	var first ProfileSelectorPayload
	if err := first.UnmarshalBinary(built.Payload); err != nil {
		t.Fatalf("decode first selector: %v", err)
	}
	if first.BTCAnchorAgeBlocks != 0 {
		t.Fatalf("first active selector age is %d, want 0", first.BTCAnchorAgeBlocks)
	}

	if _, err := build(t, config, current, nil); !errors.Is(err, ErrMissingProfileSelector) {
		t.Fatalf("missing active parent selector returned %v", err)
	}

	replacement := current
	replacement.SnapshotID[0] ^= 0xff
	if _, err := build(
		t,
		config,
		current,
		testBuilderParentExtra(t, replacement, DifficultyPolicyVersionV1, 0),
	); !errors.Is(err, ErrBTCAnchorIdentityMismatch) {
		t.Fatalf("same-height replacement returned %v", err)
	}

	regressed := current
	regressed.BTCHeight--
	if _, err := build(
		t,
		config,
		regressed,
		testBuilderParentExtra(t, current, DifficultyPolicyVersionV1, 0),
	); !errors.Is(err, ErrBTCAnchorHeightRegression) {
		t.Fatalf("BTC height regression returned %v", err)
	}

	config.USDB.Activations[0].BTCAnchorMaxAgeBlocks = 1
	if _, err := build(
		t,
		config,
		current,
		testBuilderParentExtra(t, current, DifficultyPolicyVersionV1, 1),
	); !errors.Is(err, ErrBTCAnchorAgeExceeded) {
		t.Fatalf("anchor max-age overflow returned %v", err)
	}
}

func TestPayloadBuilderUsesExpectedVersionAtActivationBoundary(t *testing.T) {
	selector := newTestSelector(t, 123)
	client := &stubProfileClient{
		system:  newTestSystemStateInfo(t, selector),
		profile: newTestProfileView(t, selector, "0", "0"),
	}
	config := &params.ChainConfig{USDB: &params.USDBConsensusConfig{
		BTCNetworkID:         "btc-regtest",
		BTCIndexOriginHeight: 1,
		Activations: []params.USDBConsensusActivation{
			{
				Block:                   0,
				BTCActivationRegistryID: BTCRegtestActivationRegistryIDV1,
				BTCAnchorMaxAgeBlocks:   params.USDBDevelopmentBTCAnchorMaxAgeBlocks,
				Versions: params.USDBConsensusVersions{
					PayloadVersion:          1,
					BTCAnchorPolicyVersion:  BTCAnchorPolicyVersionV1,
					DifficultyPolicyVersion: 1,
				},
			},
			{
				Block:                   100,
				BTCActivationRegistryID: BTCRegtestActivationRegistryIDRevision2,
				BTCAnchorMaxAgeBlocks:   params.USDBDevelopmentBTCAnchorMaxAgeBlocks,
				Versions: params.USDBConsensusVersions{
					PayloadVersion:          1,
					BTCAnchorPolicyVersion:  BTCAnchorPolicyVersionV1,
					DifficultyPolicyVersion: 2,
				},
			},
		},
	}}
	builder, err := NewPayloadBuilder(client, selector.PassID.String(), config, 0)
	if err != nil {
		t.Fatalf("failed to build payload builder: %v", err)
	}
	for _, test := range []struct {
		block         uint64
		want          uint16
		parentVersion uint16
		wantRegistry  string
	}{
		{block: 99, want: 1, parentVersion: 1, wantRegistry: BTCRegtestActivationRegistryIDV1},
		{block: 100, want: 2, parentVersion: 1, wantRegistry: BTCRegtestActivationRegistryIDRevision2},
	} {
		client.profile.ExternalState.ActivationRegistryID = test.wantRegistry
		parentExtra := testBuilderParentExtra(t, selector, test.parentVersion, 0)
		built, err := builder.BuildCurrentPayload(context.Background(), test.block, parentExtra)
		if err != nil {
			t.Fatalf("block %d payload failed: %v", test.block, err)
		}
		var payload ProfileSelectorPayload
		if err := payload.UnmarshalBinary(built.Payload); err != nil {
			t.Fatalf("block %d payload decode failed: %v", test.block, err)
		}
		if payload.DifficultyPolicyVersion != test.want {
			t.Fatalf("block %d used difficulty version %d, want %d", test.block, payload.DifficultyPolicyVersion, test.want)
		}
		if client.lastQuery.ExpectedState.ActivationRegistryID != test.wantRegistry {
			t.Fatalf("block %d queried registry %s, want %s", test.block, client.lastQuery.ExpectedState.ActivationRegistryID, test.wantRegistry)
		}
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
				system:  newTestSystemStateInfo(t, selector),
				profile: profile,
			}
			builder, err := NewPayloadBuilder(client, selector.PassID.String(), testBuilderChainConfig(ProfileSelectorPayloadVersionV1, DifficultyPolicyVersionV1), 0)
			if err != nil {
				t.Fatalf("failed to build payload builder: %v", err)
			}
			parentExtra := testBuilderParentExtra(t, selector, DifficultyPolicyVersionV1, 0)
			if _, err := builder.BuildCurrentPayload(context.Background(), 42, parentExtra); !errors.Is(err, ErrSelectedPassNotCandidate) {
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
	unknownRegistry := testBuilderChainConfig(ProfileSelectorPayloadVersionV1, DifficultyPolicyVersionV1)
	unknownRegistry.USDB.Activations[0].BTCActivationRegistryID = repeatHex("99", 32)
	unknownBuilder, err := NewPayloadBuilder(&stubProfileClient{}, selector.PassID.String(), unknownRegistry, 0)
	if err != nil {
		t.Fatalf("future registry must not fail builder construction: %v", err)
	}
	parentExtra := testBuilderParentExtra(t, selector, DifficultyPolicyVersionV1, 0)
	if _, err := unknownBuilder.BuildCurrentPayload(context.Background(), 42, parentExtra); !errors.Is(err, ErrBTCActivationRegistryNotSupported) {
		t.Fatalf("expected unknown chain-config registry to fail closed, got %v", err)
	}
	zeroAnchorPolicy := testBuilderChainConfig(ProfileSelectorPayloadVersionV1, DifficultyPolicyVersionV1)
	zeroAnchorPolicy.USDB.Activations[0].Versions.BTCAnchorPolicyVersion = 0
	unsupportedAnchorPolicy := testBuilderChainConfig(ProfileSelectorPayloadVersionV1, DifficultyPolicyVersionV1)
	unsupportedAnchorPolicy.USDB.Activations[0].Versions.BTCAnchorPolicyVersion = 2
	zeroAnchorMax := testBuilderChainConfig(ProfileSelectorPayloadVersionV1, DifficultyPolicyVersionV1)
	zeroAnchorMax.USDB.Activations[0].BTCAnchorMaxAgeBlocks = 0
	tests := []struct {
		name   string
		config *params.ChainConfig
	}{
		{name: "inactive", config: &params.ChainConfig{}},
		{name: "zero payload version", config: testBuilderChainConfig(0, DifficultyPolicyVersionV1)},
		{name: "zero difficulty policy", config: testBuilderChainConfig(ProfileSelectorPayloadVersionV1, 0)},
		{name: "zero anchor policy", config: zeroAnchorPolicy},
		{name: "unsupported anchor policy", config: unsupportedAnchorPolicy},
		{name: "zero anchor max", config: zeroAnchorMax},
		{name: "unsupported payload version", config: testBuilderChainConfig(2, DifficultyPolicyVersionV1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder, err := NewPayloadBuilder(&stubProfileClient{}, selector.PassID.String(), test.config, 0)
			if err != nil {
				return
			}
			if _, err := builder.BuildCurrentPayload(context.Background(), 42, parentExtra); err == nil {
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
		{name: "missing profile", client: &stubProfileClient{system: newTestSystemStateInfo(t, selector)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder, err := NewPayloadBuilder(test.client, selector.PassID.String(), config, 0)
			if err != nil {
				t.Fatalf("failed to construct builder: %v", err)
			}
			parentExtra := testBuilderParentExtra(t, selector, DifficultyPolicyVersionV1, 0)
			if _, err := builder.BuildCurrentPayload(context.Background(), 42, parentExtra); err == nil {
				t.Fatal("expected unavailable current profile state to stop payload generation")
			}
		})
	}
}

func TestPayloadBuilderRejectsInvalidOrChangingActivationIdentity(t *testing.T) {
	selector := newTestSelector(t, 123)
	config := testBuilderChainConfig(ProfileSelectorPayloadVersionV1, DifficultyPolicyVersionV1)

	invalidState := newTestSystemStateInfo(t, selector)
	invalidState.ActiveVersionSetID = repeatHex("88", 32)
	client := &stubProfileClient{
		system:  invalidState,
		profile: newTestProfileView(t, selector, "0", "0"),
	}
	builder, err := NewPayloadBuilder(client, selector.PassID.String(), config, 0)
	if err != nil {
		t.Fatalf("failed to construct builder: %v", err)
	}
	parentExtra := testBuilderParentExtra(t, selector, DifficultyPolicyVersionV1, 0)
	if _, err := builder.BuildCurrentPayload(context.Background(), 42, parentExtra); err == nil {
		t.Fatal("expected invalid current activation identity to stop payload generation")
	}

	client.system = newTestSystemStateInfo(t, selector)
	client.profile = newTestProfileView(t, selector, "0", "0")
	client.profile.ExternalState.ActivationRegistryID = repeatHex("99", 32)
	if _, err := builder.BuildCurrentPayload(context.Background(), 42, parentExtra); err == nil {
		t.Fatal("expected activation identity drift to stop payload generation")
	}
}
