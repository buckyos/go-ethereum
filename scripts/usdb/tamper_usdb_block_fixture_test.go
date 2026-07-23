package main

import (
	"bytes"
	"testing"

	"github.com/ethereum/go-ethereum/internal/usdb"
)

func testSelectorBytes(t *testing.T) []byte {
	t.Helper()
	selector, err := usdb.NewProfileSelectorPayload(
		usdb.DifficultyPolicyVersionV1,
		137,
		"1111111111111111111111111111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222222222222222222222222222",
		"3333333333333333333333333333333333333333333333333333333333333333i0",
	)
	if err != nil {
		t.Fatalf("create selector: %v", err)
	}
	encoded, err := selector.MarshalBinary()
	if err != nil {
		t.Fatalf("encode selector: %v", err)
	}
	return encoded
}

func TestTamperSelectorChangesExactlyOneField(t *testing.T) {
	original := testSelectorBytes(t)
	var originalSelector usdb.ProfileSelectorPayload
	if err := originalSelector.UnmarshalBinary(original); err != nil {
		t.Fatalf("decode original selector: %v", err)
	}
	fields := []string{
		fieldPayloadVersion,
		fieldDifficultyPolicy,
		fieldBTCHeight,
		fieldSnapshotID,
		fieldSystemStateID,
		fieldPassID,
	}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			tampered, err := tamperSelector(original, field)
			if err != nil {
				t.Fatalf("tamper selector: %v", err)
			}
			if len(tampered) != len(original) {
				t.Fatalf("selector size changed: have %d want %d", len(tampered), len(original))
			}
			if bytes.Equal(tampered, original) {
				t.Fatal("selector did not change")
			}
			if field == fieldPayloadVersion {
				if tampered[0] != original[0]^0xff || !bytes.Equal(tampered[1:], original[1:]) {
					t.Fatal("payload-version tamper changed bytes outside the version field")
				}
				var decoded usdb.ProfileSelectorPayload
				if err := decoded.UnmarshalBinary(tampered); err == nil {
					t.Fatal("tampered payload version unexpectedly decoded")
				}
				return
			}

			var decoded usdb.ProfileSelectorPayload
			if err := decoded.UnmarshalBinary(tampered); err != nil {
				t.Fatalf("tampered selector no longer decodes: %v", err)
			}
			expected := originalSelector
			switch field {
			case fieldDifficultyPolicy:
				expected.DifficultyPolicyVersion ^= 0xffff
			case fieldBTCHeight:
				expected.BTCHeight ^= 1
			case fieldSnapshotID:
				expected.SnapshotID[0] ^= 0xff
			case fieldSystemStateID:
				expected.SystemStateID[0] ^= 0xff
			case fieldPassID:
				expected.PassID.TxID[0] ^= 0xff
			}
			if decoded != expected {
				t.Fatalf("tamper changed fields outside %s: have %+v want %+v", field, decoded, expected)
			}
		})
	}
}

func TestTamperSelectorRejectsUnknownFieldAndWrongSize(t *testing.T) {
	original := testSelectorBytes(t)
	if _, err := tamperSelector(original, "unknown"); err == nil {
		t.Fatal("unknown field unexpectedly accepted")
	}
	if _, err := tamperSelector(original[:len(original)-1], fieldBTCHeight); err == nil {
		t.Fatal("short selector unexpectedly accepted")
	}
}
