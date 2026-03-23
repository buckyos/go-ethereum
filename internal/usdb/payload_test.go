package usdb

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestRewardPayloadV1RoundTrip(t *testing.T) {
	payload, err := NewRewardPayloadV1(
		123,
		"11"+repeatHex("22", 31),
		repeatHex("aa", 32),
		repeatHex("bb", 32)+"i7",
	)
	if err != nil {
		t.Fatalf("failed to build payload: %v", err)
	}
	encoded, err := payload.MarshalBinary()
	if err != nil {
		t.Fatalf("failed to encode payload: %v", err)
	}
	if len(encoded) != RewardPayloadV1Size {
		t.Fatalf("unexpected payload size: have %d want %d", len(encoded), RewardPayloadV1Size)
	}
	var decoded RewardPayloadV1
	if err := decoded.UnmarshalBinary(encoded); err != nil {
		t.Fatalf("failed to decode payload: %v", err)
	}
	if decoded.BTCHeight != payload.BTCHeight {
		t.Fatalf("unexpected btc height: have %d want %d", decoded.BTCHeight, payload.BTCHeight)
	}
	if decoded.SnapshotID != payload.SnapshotID {
		t.Fatalf("unexpected snapshot id: have %s want %s", decoded.SnapshotID.Hex(), payload.SnapshotID.Hex())
	}
	if decoded.SystemStateID != payload.SystemStateID {
		t.Fatalf("unexpected system state id: have %s want %s", decoded.SystemStateID.Hex(), payload.SystemStateID.Hex())
	}
	if decoded.PassID != payload.PassID {
		t.Fatalf("unexpected pass id: have %+v want %+v", decoded.PassID, payload.PassID)
	}
}

func TestRewardPayloadV1RejectsInvalidVersion(t *testing.T) {
	data := make([]byte, RewardPayloadV1Size)
	data[0] = 9

	var payload RewardPayloadV1
	if err := payload.UnmarshalBinary(data); err == nil {
		t.Fatalf("expected invalid version error")
	}
}

func TestParsePassIDRoundTrip(t *testing.T) {
	raw := repeatHex("ab", 32) + "i42"
	passID, err := ParsePassID(raw)
	if err != nil {
		t.Fatalf("failed to parse pass id: %v", err)
	}
	if passID.Index != 42 {
		t.Fatalf("unexpected index: have %d want %d", passID.Index, 42)
	}
	if passID.TxID != common.HexToHash("0x"+repeatHex("ab", 32)) {
		t.Fatalf("unexpected txid: have %s", passID.TxID.Hex())
	}
	if got := passID.String(); got != raw {
		t.Fatalf("unexpected roundtrip string: have %s want %s", got, raw)
	}
}

func repeatHex(unit string, count int) string {
	output := ""
	for i := 0; i < count; i++ {
		output += unit
	}
	return output
}
