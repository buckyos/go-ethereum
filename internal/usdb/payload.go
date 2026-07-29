package usdb

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

const (
	// ProfileSelectorPayloadVersionV1 identifies the UIP-0007 v1 binary layout.
	ProfileSelectorPayloadVersionV1 byte = 1
	// DifficultyPolicyVersionV1 identifies the first UIP-0005 difficulty policy.
	DifficultyPolicyVersionV1 uint16 = 1
	// BTCAnchorPolicyVersionV1 requires monotonic BTC heights and bounded reuse
	// of one exact BTC-side state identity.
	BTCAnchorPolicyVersionV1 uint16 = 1
	// DifficultyPolicyVersionActivationConformance is reserved for build-tagged
	// activation tests. Production builds deliberately reject this version.
	DifficultyPolicyVersionActivationConformance uint16 = 0xffff

	// PassIDEncodedSize stores a compact inscription outpoint: 32-byte txid + 4-byte index.
	PassIDEncodedSize = common.HashLength + 4
	// ProfileSelectorPayloadV1Size is the exact UIP-0007 v1 header.Extra size.
	ProfileSelectorPayloadV1Size = 1 + 2 + 4 + 4 + common.HashLength + common.HashLength + PassIDEncodedSize

	payloadVersionOffset          = 0
	difficultyPolicyVersionOffset = 1
	btcHeightOffset               = 3
	btcAnchorAgeBlocksOffset      = 7
	snapshotIDOffset              = 11
	systemStateIDOffset           = 43
	passIDOffset                  = 75
)

var (
	// ErrMissingProfileSelector indicates that an active USDB block has no selector payload.
	ErrMissingProfileSelector = errors.New("missing usdb profile selector")
	// ErrProfileSelectorSize indicates that header.Extra is not the exact active payload size.
	ErrProfileSelectorSize = errors.New("usdb profile selector size mismatch")
	// ErrProfileSelectorVersion indicates an unsupported binary payload layout.
	ErrProfileSelectorVersion = errors.New("usdb profile selector version mismatch")
	// ErrDifficultyPolicyVersionMismatch indicates that the header commitment does
	// not match the version selected by the USDB chain configuration.
	ErrDifficultyPolicyVersionMismatch = errors.New("usdb difficulty policy version mismatch")
)

// PassID is the canonical compact pass identifier used in USDB consensus payloads.
//
// It intentionally avoids the variable-length inscription string on chain and stores
// only the underlying outpoint components.
type PassID struct {
	TxID  common.Hash
	Index uint32
}

// ProfileSelectorPayload is the UIP-0007 v1 selector stored in header.Extra.
type ProfileSelectorPayload struct {
	PayloadVersion          byte
	DifficultyPolicyVersion uint16
	BTCHeight               uint32
	BTCAnchorAgeBlocks      uint32
	SnapshotID              common.Hash
	SystemStateID           common.Hash
	PassID                  PassID
}

// NewProfileSelectorPayload converts canonical USDB ids into the compact binary payload format.
func NewProfileSelectorPayload(difficultyPolicyVersion uint16, btcHeight, btcAnchorAgeBlocks uint32, snapshotID, systemStateID, passID string) (*ProfileSelectorPayload, error) {
	snapshotHash, err := parseCanonicalHex32("snapshot_id", snapshotID)
	if err != nil {
		return nil, err
	}
	systemHash, err := parseCanonicalHex32("system_state_id", systemStateID)
	if err != nil {
		return nil, err
	}
	parsedPassID, err := ParsePassID(passID)
	if err != nil {
		return nil, err
	}
	return &ProfileSelectorPayload{
		PayloadVersion:          ProfileSelectorPayloadVersionV1,
		DifficultyPolicyVersion: difficultyPolicyVersion,
		BTCHeight:               btcHeight,
		BTCAnchorAgeBlocks:      btcAnchorAgeBlocks,
		SnapshotID:              snapshotHash,
		SystemStateID:           systemHash,
		PassID:                  parsedPassID,
	}, nil
}

// MarshalBinary encodes the selector into the exact UIP-0007 v1 header.Extra layout.
func (p ProfileSelectorPayload) MarshalBinary() ([]byte, error) {
	if p.PayloadVersion != ProfileSelectorPayloadVersionV1 {
		return nil, fmt.Errorf("%w: have %d want %d", ErrProfileSelectorVersion, p.PayloadVersion, ProfileSelectorPayloadVersionV1)
	}
	output := make([]byte, ProfileSelectorPayloadV1Size)
	output[payloadVersionOffset] = p.PayloadVersion
	binary.BigEndian.PutUint16(output[difficultyPolicyVersionOffset:btcHeightOffset], p.DifficultyPolicyVersion)
	binary.BigEndian.PutUint32(output[btcHeightOffset:btcAnchorAgeBlocksOffset], p.BTCHeight)
	binary.BigEndian.PutUint32(output[btcAnchorAgeBlocksOffset:snapshotIDOffset], p.BTCAnchorAgeBlocks)
	copy(output[snapshotIDOffset:systemStateIDOffset], p.SnapshotID[:])
	copy(output[systemStateIDOffset:passIDOffset], p.SystemStateID[:])
	passBytes, err := p.PassID.MarshalBinary()
	if err != nil {
		return nil, err
	}
	copy(output[passIDOffset:], passBytes)
	return output, nil
}

// UnmarshalBinary decodes the exact UIP-0007 v1 payload from header.Extra.
func (p *ProfileSelectorPayload) UnmarshalBinary(data []byte) error {
	if len(data) != ProfileSelectorPayloadV1Size {
		return fmt.Errorf("%w: have %d want %d", ErrProfileSelectorSize, len(data), ProfileSelectorPayloadV1Size)
	}
	if data[payloadVersionOffset] != ProfileSelectorPayloadVersionV1 {
		return fmt.Errorf("%w: have %d want %d", ErrProfileSelectorVersion, data[payloadVersionOffset], ProfileSelectorPayloadVersionV1)
	}
	p.PayloadVersion = data[payloadVersionOffset]
	p.DifficultyPolicyVersion = binary.BigEndian.Uint16(data[difficultyPolicyVersionOffset:btcHeightOffset])
	p.BTCHeight = binary.BigEndian.Uint32(data[btcHeightOffset:btcAnchorAgeBlocksOffset])
	p.BTCAnchorAgeBlocks = binary.BigEndian.Uint32(data[btcAnchorAgeBlocksOffset:snapshotIDOffset])
	p.SnapshotID = common.BytesToHash(data[snapshotIDOffset:systemStateIDOffset])
	p.SystemStateID = common.BytesToHash(data[systemStateIDOffset:passIDOffset])
	return p.PassID.UnmarshalBinary(data[passIDOffset:])
}

// ValidateProfileSelectorPayload performs the consensus-only binary checks for
// header.Extra. It does not query usdb-indexer.
func ValidateProfileSelectorPayload(data []byte, expectedPayloadVersion byte, expectedDifficultyPolicyVersion uint16) error {
	if len(data) == 0 {
		return ErrMissingProfileSelector
	}
	if expectedPayloadVersion != ProfileSelectorPayloadVersionV1 {
		return fmt.Errorf("%w: chain config expects unsupported version %d", ErrProfileSelectorVersion, expectedPayloadVersion)
	}
	var payload ProfileSelectorPayload
	if err := payload.UnmarshalBinary(data); err != nil {
		return err
	}
	if payload.PayloadVersion != expectedPayloadVersion {
		return fmt.Errorf("%w: have %d want %d", ErrProfileSelectorVersion, payload.PayloadVersion, expectedPayloadVersion)
	}
	if payload.DifficultyPolicyVersion != expectedDifficultyPolicyVersion {
		return fmt.Errorf("%w: have %d want %d", ErrDifficultyPolicyVersionMismatch, payload.DifficultyPolicyVersion, expectedDifficultyPolicyVersion)
	}
	return nil
}

// SnapshotIDHex returns the canonical lowercase USDB snapshot id without a prefix.
func (p ProfileSelectorPayload) SnapshotIDHex() string {
	return hex.EncodeToString(p.SnapshotID[:])
}

// SystemStateIDHex returns the canonical lowercase USDB system-state id without a prefix.
func (p ProfileSelectorPayload) SystemStateIDHex() string {
	return hex.EncodeToString(p.SystemStateID[:])
}

// ParsePassID converts a canonical `txidiN` inscription id into the compact consensus form.
func ParsePassID(value string) (PassID, error) {
	txidText, indexText, found := strings.Cut(value, "i")
	if !found || strings.Contains(indexText, "i") {
		return PassID{}, fmt.Errorf("invalid pass id %q: expected canonical txidiN format", value)
	}
	txid, err := parseCanonicalHex32("pass_id.txid", txidText)
	if err != nil {
		return PassID{}, err
	}
	if indexText == "" || (len(indexText) > 1 && indexText[0] == '0') {
		return PassID{}, fmt.Errorf("invalid pass id %q: index is not canonical uint32 decimal", value)
	}
	for _, char := range indexText {
		if char < '0' || char > '9' {
			return PassID{}, fmt.Errorf("invalid pass id %q: index is not canonical uint32 decimal", value)
		}
	}
	index, err := strconv.ParseUint(indexText, 10, 32)
	if err != nil {
		return PassID{}, fmt.Errorf("invalid pass id %q: bad index: %w", value, err)
	}
	return PassID{TxID: txid, Index: uint32(index)}, nil
}

// MarshalBinary encodes a pass id as txid bytes followed by a big-endian uint32 index.
func (id PassID) MarshalBinary() ([]byte, error) {
	output := make([]byte, PassIDEncodedSize)
	copy(output[:common.HashLength], id.TxID[:])
	binary.BigEndian.PutUint32(output[common.HashLength:], id.Index)
	return output, nil
}

// UnmarshalBinary decodes the fixed-size binary pass id used by UIP-0007.
func (id *PassID) UnmarshalBinary(data []byte) error {
	if len(data) != PassIDEncodedSize {
		return fmt.Errorf("invalid pass id size: have %d want %d", len(data), PassIDEncodedSize)
	}
	id.TxID = common.BytesToHash(data[:common.HashLength])
	id.Index = binary.BigEndian.Uint32(data[common.HashLength:])
	return nil
}

// String returns the UIP-0001 canonical text representation of the pass id.
func (id PassID) String() string {
	return hex.EncodeToString(id.TxID[:]) + "i" + strconv.FormatUint(uint64(id.Index), 10)
}

func parseCanonicalHex32(label, value string) (common.Hash, error) {
	if len(value) != common.HashLength*2 {
		return common.Hash{}, fmt.Errorf("invalid %s length: have %d want %d", label, len(value), common.HashLength*2)
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return common.Hash{}, fmt.Errorf("invalid %s: expected lowercase hex without prefix", label)
		}
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return common.Hash{}, fmt.Errorf("invalid %s hex: %w", label, err)
	}
	return common.BytesToHash(decoded), nil
}
