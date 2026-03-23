package usdb

import (
	"context"
	"testing"
)

type stubVerifierClient struct {
	pass   *PassSnapshot
	energy *PassEnergySnapshot
}

func (s *stubVerifierClient) GetSystemStateInfo(context.Context) (*SystemStateInfo, error) {
	return nil, nil
}

func (s *stubVerifierClient) GetPassSnapshot(context.Context, PassID, QueryContext) (*PassSnapshot, error) {
	return s.pass, nil
}

func (s *stubVerifierClient) GetPassEnergy(context.Context, PassID, QueryContext) (*PassEnergySnapshot, error) {
	return s.energy, nil
}

func (s *stubVerifierClient) Close() {}

func TestVerifierResolveReward(t *testing.T) {
	payload, err := NewRewardPayloadV1(
		123,
		repeatHex("11", 32),
		repeatHex("22", 32),
		repeatHex("33", 32)+"i7",
	)
	if err != nil {
		t.Fatalf("failed to build payload: %v", err)
	}
	encoded, err := payload.MarshalBinary()
	if err != nil {
		t.Fatalf("failed to encode payload: %v", err)
	}

	verifier, err := NewVerifier(&stubVerifierClient{
		pass: &PassSnapshot{
			InscriptionID:  repeatHex("33", 32) + "i7",
			State:          "active",
			ResolvedHeight: 123,
		},
		energy: &PassEnergySnapshot{
			InscriptionID:  repeatHex("33", 32) + "i7",
			Energy:         DefaultLevelBaseEnergy,
			ResolvedHeight: 123,
		},
	}, 0)
	if err != nil {
		t.Fatalf("failed to build verifier: %v", err)
	}

	resolved, err := verifier.ResolveReward(context.Background(), encoded, 99)
	if err != nil {
		t.Fatalf("failed to resolve reward: %v", err)
	}
	if resolved.Payload.BTCHeight != 123 {
		t.Fatalf("unexpected btc height: have %d want 123", resolved.Payload.BTCHeight)
	}
	if resolved.Level != 1 {
		t.Fatalf("unexpected level: have %d want 1", resolved.Level)
	}
	if resolved.MultiplierBps != MinimumMultiplierBps {
		t.Fatalf("unexpected multiplier: have %d want %d", resolved.MultiplierBps, MinimumMultiplierBps)
	}
	if resolved.MinerReward.Cmp(RewardForLevel(99, 1)) != 0 {
		t.Fatalf("unexpected miner reward: have %s want %s", resolved.MinerReward, RewardForLevel(99, 1))
	}
}

func TestVerifierResolveRewardRejectsMissingPayload(t *testing.T) {
	verifier, err := NewVerifier(&stubVerifierClient{}, 0)
	if err != nil {
		t.Fatalf("failed to build verifier: %v", err)
	}
	if _, err := verifier.ResolveReward(context.Background(), nil, 0); err == nil {
		t.Fatalf("expected missing payload error")
	}
}
