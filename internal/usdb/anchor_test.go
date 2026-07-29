package usdb

import (
	"errors"
	"math"
	"testing"
)

func TestValidateBTCAnchorTransitionV1(t *testing.T) {
	base := newTestSelector(t, 100)
	parent := base
	parent.BTCAnchorAgeBlocks = 3

	tests := []struct {
		name    string
		parent  *ProfileSelectorPayload
		child   ProfileSelectorPayload
		maxAge  uint32
		wantErr error
	}{
		{
			name:   "first active block",
			child:  base,
			maxAge: 5,
		},
		{
			name:    "first active block nonzero age",
			child:   selectorWithAge(base, 1),
			maxAge:  5,
			wantErr: ErrBTCAnchorAgeMismatch,
		},
		{
			name:   "same anchor increments",
			parent: &parent,
			child:  selectorWithAge(base, 4),
			maxAge: 5,
		},
		{
			name:    "same anchor skipped count",
			parent:  &parent,
			child:   selectorWithAge(base, 5),
			maxAge:  5,
			wantErr: ErrBTCAnchorAgeMismatch,
		},
		{
			name:   "new height resets",
			parent: &parent,
			child:  selectorWithHeightAndAge(base, 101, 0),
			maxAge: 5,
		},
		{
			name:    "new height nonzero age",
			parent:  &parent,
			child:   selectorWithHeightAndAge(base, 101, 1),
			maxAge:  5,
			wantErr: ErrBTCAnchorAgeMismatch,
		},
		{
			name:    "height regression",
			parent:  &parent,
			child:   selectorWithHeightAndAge(base, 99, 0),
			maxAge:  5,
			wantErr: ErrBTCAnchorHeightRegression,
		},
		{
			name:   "maximum exact",
			parent: &parent,
			child:  selectorWithAge(base, 4),
			maxAge: 4,
		},
		{
			name:    "maximum exceeded",
			parent:  &parent,
			child:   selectorWithAge(base, 4),
			maxAge:  3,
			wantErr: ErrBTCAnchorAgeExceeded,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateBTCAnchorTransition(
				test.parent,
				test.child,
				BTCAnchorPolicyVersionV1,
				test.maxAge,
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("have error %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestValidateBTCAnchorTransitionRejectsSameHeightReplacement(t *testing.T) {
	parent := newTestSelector(t, 100)
	child := parent
	child.BTCAnchorAgeBlocks = 1
	child.SnapshotID[0] ^= 0xff
	if err := ValidateBTCAnchorTransition(
		&parent,
		child,
		BTCAnchorPolicyVersionV1,
		5,
	); !errors.Is(err, ErrBTCAnchorIdentityMismatch) {
		t.Fatalf("snapshot replacement returned %v", err)
	}

	child = parent
	child.BTCAnchorAgeBlocks = 1
	child.SystemStateID[0] ^= 0xff
	if err := ValidateBTCAnchorTransition(
		&parent,
		child,
		BTCAnchorPolicyVersionV1,
		5,
	); !errors.Is(err, ErrBTCAnchorIdentityMismatch) {
		t.Fatalf("system-state replacement returned %v", err)
	}
}

func TestExpectedBTCAnchorAgeBlocksRejectsUnsupportedAndOverflow(t *testing.T) {
	child := newTestSelector(t, 100)
	if _, err := ExpectedBTCAnchorAgeBlocks(nil, child, 2, 5); !errors.Is(err, ErrBTCAnchorPolicyVersion) {
		t.Fatalf("unsupported policy returned %v", err)
	}
	parent := child
	parent.BTCAnchorAgeBlocks = math.MaxUint32
	if _, err := ExpectedBTCAnchorAgeBlocks(
		&parent,
		child,
		BTCAnchorPolicyVersionV1,
		math.MaxUint32,
	); !errors.Is(err, ErrBTCAnchorAgeExceeded) {
		t.Fatalf("counter overflow returned %v", err)
	}
}

func selectorWithAge(selector ProfileSelectorPayload, age uint32) ProfileSelectorPayload {
	selector.BTCAnchorAgeBlocks = age
	return selector
}

func selectorWithHeightAndAge(selector ProfileSelectorPayload, height, age uint32) ProfileSelectorPayload {
	selector.BTCHeight = height
	selector.BTCAnchorAgeBlocks = age
	return selector
}
