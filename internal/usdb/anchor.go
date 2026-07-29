package usdb

import (
	"errors"
	"fmt"
	"math"
)

var (
	// ErrBTCAnchorPolicyVersion indicates an unsupported parent-child anchor policy.
	ErrBTCAnchorPolicyVersion = errors.New("unsupported usdb BTC anchor policy version")
	// ErrBTCAnchorHeightRegression indicates that a child references an older BTC height.
	ErrBTCAnchorHeightRegression = errors.New("usdb BTC anchor height regressed")
	// ErrBTCAnchorIdentityMismatch indicates a same-height replacement of the committed state.
	ErrBTCAnchorIdentityMismatch = errors.New("usdb BTC anchor identity changed at the same height")
	// ErrBTCAnchorAgeMismatch indicates a non-canonical child age counter.
	ErrBTCAnchorAgeMismatch = errors.New("usdb BTC anchor age mismatch")
	// ErrBTCAnchorAgeExceeded indicates that one anchor exceeded its configured reuse bound.
	ErrBTCAnchorAgeExceeded = errors.New("usdb BTC anchor age exceeded")
)

// ExpectedBTCAnchorAgeBlocks derives the only valid child age from its parent.
// A nil parent represents the first non-genesis block under USDB consensus.
func ExpectedBTCAnchorAgeBlocks(
	parent *ProfileSelectorPayload,
	child ProfileSelectorPayload,
	policyVersion uint16,
	maxAgeBlocks uint32,
) (uint32, error) {
	if policyVersion != BTCAnchorPolicyVersionV1 {
		return 0, fmt.Errorf("%w: %d", ErrBTCAnchorPolicyVersion, policyVersion)
	}
	if maxAgeBlocks == 0 {
		return 0, fmt.Errorf("%w: configured maximum is zero", ErrBTCAnchorAgeExceeded)
	}
	if parent == nil || child.BTCHeight > parent.BTCHeight {
		return 0, nil
	}
	if child.BTCHeight < parent.BTCHeight {
		return 0, fmt.Errorf(
			"%w: parent %d child %d",
			ErrBTCAnchorHeightRegression,
			parent.BTCHeight,
			child.BTCHeight,
		)
	}
	if child.SnapshotID != parent.SnapshotID || child.SystemStateID != parent.SystemStateID {
		return 0, fmt.Errorf(
			"%w: height %d",
			ErrBTCAnchorIdentityMismatch,
			child.BTCHeight,
		)
	}
	if parent.BTCAnchorAgeBlocks == math.MaxUint32 {
		return 0, fmt.Errorf("%w: counter overflow", ErrBTCAnchorAgeExceeded)
	}
	age := parent.BTCAnchorAgeBlocks + 1
	if age > maxAgeBlocks {
		return 0, fmt.Errorf(
			"%w: have %d maximum %d",
			ErrBTCAnchorAgeExceeded,
			age,
			maxAgeBlocks,
		)
	}
	return age, nil
}

// ValidateBTCAnchorTransition checks one selector against the committed parent
// selector without consulting a local BTC tip or any external service.
func ValidateBTCAnchorTransition(
	parent *ProfileSelectorPayload,
	child ProfileSelectorPayload,
	policyVersion uint16,
	maxAgeBlocks uint32,
) error {
	expected, err := ExpectedBTCAnchorAgeBlocks(parent, child, policyVersion, maxAgeBlocks)
	if err != nil {
		return err
	}
	if child.BTCAnchorAgeBlocks != expected {
		return fmt.Errorf(
			"%w: have %d want %d",
			ErrBTCAnchorAgeMismatch,
			child.BTCAnchorAgeBlocks,
			expected,
		)
	}
	return nil
}
