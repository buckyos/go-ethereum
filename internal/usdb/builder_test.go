package usdb

import (
	"context"
	"testing"
)

type stubClient struct {
	system *SystemStateInfo
	pass   *PassSnapshot
}

func (s *stubClient) GetSystemStateInfo(context.Context) (*SystemStateInfo, error) {
	return s.system, nil
}

func (s *stubClient) GetPassSnapshot(context.Context, PassID, QueryContext) (*PassSnapshot, error) {
	return s.pass, nil
}

func (s *stubClient) GetPassEnergy(context.Context, PassID, QueryContext) (*PassEnergySnapshot, error) {
	return &PassEnergySnapshot{}, nil
}

func (s *stubClient) Close() {}

func TestPayloadBuilderBuildCurrentPayload(t *testing.T) {
	builder, err := NewPayloadBuilder(&stubClient{
		system: &SystemStateInfo{
			LocalSyncedBlockHeight: 123,
			UpstreamSnapshotID:     repeatHex("11", 32),
			SystemStateID:          repeatHex("22", 32),
		},
		pass: &PassSnapshot{
			InscriptionID:  repeatHex("33", 32) + "i1",
			ResolvedHeight: 123,
		},
	}, repeatHex("33", 32)+"i1", 0)
	if err != nil {
		t.Fatalf("failed to build payload builder: %v", err)
	}

	encoded, err := builder.BuildCurrentPayload(context.Background())
	if err != nil {
		t.Fatalf("failed to build payload: %v", err)
	}
	if len(encoded) != RewardPayloadV1Size {
		t.Fatalf("unexpected payload size: have %d want %d", len(encoded), RewardPayloadV1Size)
	}

	var payload RewardPayloadV1
	if err := payload.UnmarshalBinary(encoded); err != nil {
		t.Fatalf("failed to decode payload: %v", err)
	}
	if payload.BTCHeight != 123 {
		t.Fatalf("unexpected btc height: have %d want %d", payload.BTCHeight, 123)
	}
	if payload.PassID.String() != repeatHex("33", 32)+"i1" {
		t.Fatalf("unexpected pass id: have %s", payload.PassID.String())
	}
}
