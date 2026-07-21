package usdb

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestProfileSelectorPayloadV1GoldenVector(t *testing.T) {
	payload, err := NewProfileSelectorPayload(
		0x1234,
		0x01020304,
		repeatHex("11", 32),
		repeatHex("22", 32),
		repeatHex("33", 32)+"i84281096",
	)
	if err != nil {
		t.Fatalf("failed to build profile selector: %v", err)
	}
	encoded, err := payload.MarshalBinary()
	if err != nil {
		t.Fatalf("failed to encode profile selector: %v", err)
	}
	const expected = "01123401020304" +
		"1111111111111111111111111111111111111111111111111111111111111111" +
		"2222222222222222222222222222222222222222222222222222222222222222" +
		"3333333333333333333333333333333333333333333333333333333333333333" +
		"05060708"
	if got := hex.EncodeToString(encoded); got != expected {
		t.Fatalf("unexpected golden encoding:\n have %s\n want %s", got, expected)
	}
	if len(encoded) != ProfileSelectorPayloadV1Size {
		t.Fatalf("unexpected payload size: have %d want %d", len(encoded), ProfileSelectorPayloadV1Size)
	}
}

func TestProfileSelectorPayloadV1RoundTrip(t *testing.T) {
	payload, err := NewProfileSelectorPayload(
		DifficultyPolicyVersionV1,
		123,
		"11"+repeatHex("22", 31),
		repeatHex("aa", 32),
		repeatHex("bb", 32)+"i7",
	)
	if err != nil {
		t.Fatalf("failed to build profile selector: %v", err)
	}
	encoded, err := payload.MarshalBinary()
	if err != nil {
		t.Fatalf("failed to encode profile selector: %v", err)
	}

	var decoded ProfileSelectorPayload
	if err := decoded.UnmarshalBinary(encoded); err != nil {
		t.Fatalf("failed to decode profile selector: %v", err)
	}
	if decoded != *payload {
		t.Fatalf("unexpected roundtrip payload: have %+v want %+v", decoded, *payload)
	}
}

func TestProfileSelectorPayloadV1RejectsInvalidSizeAndVersion(t *testing.T) {
	for _, size := range []int{0, ProfileSelectorPayloadV1Size - 1, ProfileSelectorPayloadV1Size + 1} {
		data := make([]byte, size)
		var payload ProfileSelectorPayload
		if err := payload.UnmarshalBinary(data); !errors.Is(err, ErrProfileSelectorSize) {
			t.Fatalf("size %d: expected size error, got %v", size, err)
		}
	}

	data := make([]byte, ProfileSelectorPayloadV1Size)
	data[0] = 9
	var payload ProfileSelectorPayload
	if err := payload.UnmarshalBinary(data); !errors.Is(err, ErrProfileSelectorVersion) {
		t.Fatalf("expected version error, got %v", err)
	}
	payload.PayloadVersion = 9
	if _, err := payload.MarshalBinary(); !errors.Is(err, ErrProfileSelectorVersion) {
		t.Fatalf("expected marshal version error, got %v", err)
	}
}

func TestParsePassIDRequiresCanonicalEncoding(t *testing.T) {
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
	for _, index := range []string{"0", "4294967295"} {
		value := repeatHex("ab", 32) + "i" + index
		if parsed, err := ParsePassID(value); err != nil || parsed.String() != value {
			t.Fatalf("canonical boundary pass id %q failed: parsed=%v err=%v", value, parsed, err)
		}
	}

	invalid := []string{
		"0x" + repeatHex("ab", 32) + "i42",
		repeatHex("AB", 32) + "i42",
		repeatHex("ab", 32) + "i042",
		repeatHex("ab", 32) + "i+42",
		repeatHex("ab", 32) + "i4294967296",
		repeatHex("ab", 32) + "i42i0",
		repeatHex("ab", 31) + "i42",
	}
	for _, value := range invalid {
		if _, err := ParsePassID(value); err == nil {
			t.Fatalf("expected non-canonical pass id %q to be rejected", value)
		}
	}
}

func TestNewProfileSelectorPayloadRejectsNonCanonicalStateIDs(t *testing.T) {
	passID := repeatHex("33", 32) + "i0"
	tests := []struct {
		name       string
		snapshotID string
		systemID   string
	}{
		{name: "snapshot prefix", snapshotID: "0x" + repeatHex("11", 32), systemID: repeatHex("22", 32)},
		{name: "snapshot uppercase", snapshotID: repeatHex("AA", 32), systemID: repeatHex("22", 32)},
		{name: "system prefix", snapshotID: repeatHex("11", 32), systemID: "0x" + repeatHex("22", 32)},
		{name: "system uppercase", snapshotID: repeatHex("11", 32), systemID: repeatHex("BB", 32)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewProfileSelectorPayload(DifficultyPolicyVersionV1, 1, test.snapshotID, test.systemID, passID); err == nil {
				t.Fatal("expected non-canonical state id to be rejected")
			}
		})
	}
}

func repeatHex(unit string, count int) string {
	output := ""
	for i := 0; i < count; i++ {
		output += unit
	}
	return output
}
