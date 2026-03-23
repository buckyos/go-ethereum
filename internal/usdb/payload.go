package usdb

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

const (
	// PayloadVersionV1 is the first extra-data encoding format for USDB reward inputs.
	PayloadVersionV1 byte = 1

	// PassIDEncodedSize stores a compact inscription outpoint: 32-byte txid + 4-byte index.
	PassIDEncodedSize = common.HashLength + 4
	// RewardPayloadV1Size is the fixed-size binary encoding used in header.Extra.
	RewardPayloadV1Size = 1 + 4 + common.HashLength + common.HashLength + PassIDEncodedSize
)

// PassID is the canonical compact pass identifier used in ETHW consensus payloads.
//
// It intentionally avoids the variable-length inscription string on chain and stores
// only the underlying outpoint components.
type PassID struct {
	TxID  common.Hash
	Index uint32
}

// RewardPayloadV1 is the first version of the USDB reward payload stored in header.Extra.
type RewardPayloadV1 struct {
	BTCHeight     uint32
	SnapshotID    common.Hash
	SystemStateID common.Hash
	PassID        PassID
}

// NewRewardPayloadV1 converts USDB RPC string ids into the compact binary payload format.
func NewRewardPayloadV1(btcHeight uint32, snapshotID, systemStateID, passID string) (*RewardPayloadV1, error) {
	snapshotHash, err := parseHex32("snapshot_id", snapshotID)
	if err != nil {
		return nil, err
	}
	systemHash, err := parseHex32("system_state_id", systemStateID)
	if err != nil {
		return nil, err
	}
	parsedPassID, err := ParsePassID(passID)
	if err != nil {
		return nil, err
	}
	return &RewardPayloadV1{
		BTCHeight:     btcHeight,
		SnapshotID:    snapshotHash,
		SystemStateID: systemHash,
		PassID:        parsedPassID,
	}, nil
}

// MarshalBinary encodes the payload into a fixed-size format suitable for header.Extra.
func (p RewardPayloadV1) MarshalBinary() ([]byte, error) {
	output := make([]byte, RewardPayloadV1Size)
	output[0] = PayloadVersionV1
	binary.BigEndian.PutUint32(output[1:5], p.BTCHeight)
	copy(output[5:5+common.HashLength], p.SnapshotID[:])
	copy(output[37:37+common.HashLength], p.SystemStateID[:])
	passBytes, err := p.PassID.MarshalBinary()
	if err != nil {
		return nil, err
	}
	copy(output[69:], passBytes)
	return output, nil
}

// UnmarshalBinary decodes the fixed-size v1 payload from header.Extra.
func (p *RewardPayloadV1) UnmarshalBinary(data []byte) error {
	if len(data) != RewardPayloadV1Size {
		return fmt.Errorf("invalid usdb reward payload size: have %d want %d", len(data), RewardPayloadV1Size)
	}
	if data[0] != PayloadVersionV1 {
		return fmt.Errorf("unsupported usdb reward payload version: %d", data[0])
	}
	p.BTCHeight = binary.BigEndian.Uint32(data[1:5])
	p.SnapshotID = common.BytesToHash(data[5 : 5+common.HashLength])
	p.SystemStateID = common.BytesToHash(data[37 : 37+common.HashLength])
	return p.PassID.UnmarshalBinary(data[69:])
}

// SnapshotIDHex returns the canonical lowercase USDB snapshot id without 0x prefix.
func (p RewardPayloadV1) SnapshotIDHex() string {
	return hex.EncodeToString(p.SnapshotID[:])
}

// SystemStateIDHex returns the canonical lowercase USDB system state id without 0x prefix.
func (p RewardPayloadV1) SystemStateIDHex() string {
	return hex.EncodeToString(p.SystemStateID[:])
}

// ParsePassID converts the canonical `txidiN` inscription id into the compact consensus form.
func ParsePassID(value string) (PassID, error) {
	parts := strings.Split(value, "i")
	if len(parts) != 2 {
		return PassID{}, fmt.Errorf("invalid pass id %q: expected txidiN format", value)
	}
	txid, err := parseHex32("pass_id.txid", parts[0])
	if err != nil {
		return PassID{}, err
	}
	index, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return PassID{}, fmt.Errorf("invalid pass id %q: bad index: %w", value, err)
	}
	return PassID{TxID: txid, Index: uint32(index)}, nil
}

func (id PassID) MarshalBinary() ([]byte, error) {
	output := make([]byte, PassIDEncodedSize)
	copy(output[:common.HashLength], id.TxID[:])
	binary.BigEndian.PutUint32(output[common.HashLength:], id.Index)
	return output, nil
}

func (id *PassID) UnmarshalBinary(data []byte) error {
	if len(data) != PassIDEncodedSize {
		return fmt.Errorf("invalid pass id size: have %d want %d", len(data), PassIDEncodedSize)
	}
	id.TxID = common.BytesToHash(data[:common.HashLength])
	id.Index = binary.BigEndian.Uint32(data[common.HashLength:])
	return nil
}

func (id PassID) String() string {
	return hex.EncodeToString(id.TxID[:]) + "i" + strconv.FormatUint(uint64(id.Index), 10)
}

func parseHex32(label, value string) (common.Hash, error) {
	trimmed := strings.TrimPrefix(strings.ToLower(value), "0x")
	if len(trimmed) != common.HashLength*2 {
		return common.Hash{}, fmt.Errorf("invalid %s length: have %d want %d", label, len(trimmed), common.HashLength*2)
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil {
		return common.Hash{}, fmt.Errorf("invalid %s hex: %w", label, err)
	}
	return common.BytesToHash(decoded), nil
}
