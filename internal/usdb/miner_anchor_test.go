package usdb

import (
	"errors"
	"math"
	"testing"
)

func TestSelectMinerAnchorHeight(t *testing.T) {
	parent := func(h, age uint32) *ProfileSelectorPayload {
		return &ProfileSelectorPayload{BTCHeight: h, BTCAnchorAgeBlocks: age}
	}
	for _, test := range []struct {
		name                string
		latest, origin, gap uint32
		parent              *ProfileSelectorPayload
		policy              uint16
		maxAge, want        uint32
		reason              string
		err                 error
	}{
		{"latest", 100, 1, 0, nil, 1, 5, 100, "gap", nil},
		{"gap one", 100, 1, 1, nil, 1, 5, 99, "gap", nil},
		{"gap two", 100, 1, 2, nil, 1, 5, 98, "gap", nil},
		{"origin clamp", 100, 99, 2, nil, 1, 5, 99, "history_origin", nil},
		{"subtract underflow", 1, 1, 2, nil, 1, 5, 1, "history_origin", nil},
		{"maximum gap", 100, 0, math.MaxUint32, nil, 1, 5, 0, "history_origin", nil},
		{"below origin", 99, 100, 2, nil, 1, 5, 0, "", ErrStateNotRetained},
		{"parent floor", 100, 1, 2, parent(99, 0), 1, 5, 99, "parent_anchor", nil},
		{"last reuse", 100, 1, 2, parent(99, 4), 1, 5, 99, "parent_anchor", nil},
		{"yield age", 100, 1, 2, parent(99, 5), 1, 5, 100, "age_limit", nil},
		{"nearest newer", 104, 1, 4, parent(100, 5), 1, 5, 101, "age_limit", nil},
		{"policy tightens cap", 104, 1, 4, parent(100, 8), 1, 5, 101, "age_limit", nil},
		{"parent behind preference", 104, 1, 2, parent(100, 5), 1, 5, 102, "gap", nil},
		{"parent ahead of local", 100, 1, 2, parent(101, 0), 1, 5, 0, "", ErrBTCAnchorHeightRegression},
		{"exhausted tip", 100, 1, 2, parent(100, 5), 1, 5, 0, "", ErrBTCAnchorAgeExceeded},
		{"counter overflow yield", 100, 1, 2, parent(99, math.MaxUint32), 1, math.MaxUint32, 100, "age_limit", nil},
		{"height ceiling", math.MaxUint32, 1, 2, parent(math.MaxUint32, math.MaxUint32), 1, math.MaxUint32, 0, "", ErrBTCAnchorAgeExceeded},
		{"unknown policy", 100, 1, 2, nil, 2, 5, 0, "", ErrBTCAnchorPolicyVersion},
		{"zero cap", 100, 1, 2, parent(99, 5), 1, 0, 0, "", ErrBTCAnchorAgeExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := SelectMinerAnchorHeight(test.latest, test.origin, test.gap, test.parent, test.policy, test.maxAge)
			if !errors.Is(err, test.err) {
				t.Fatalf("selection=%+v err=%v want=%v", got, err, test.err)
			}
			if err == nil && (got.SelectedHeight != test.want || got.Reason != test.reason || got.ActualGap != test.latest-test.want || got.ConfiguredGap != test.gap) {
				t.Fatalf("selection=%+v want height=%d reason=%s", got, test.want, test.reason)
			}
		})
	}
}

func TestMinerAnchorGapRecoversAndYieldsUntilNewState(t *testing.T) {
	parent := &ProfileSelectorPayload{BTCHeight: 100}
	for _, step := range []struct{ latest, gap, want, actual uint32 }{
		{100, 2, 100, 0}, {101, 2, 100, 1}, {102, 2, 100, 2}, {103, 2, 101, 2},
		{103, 0, 103, 0}, {104, 2, 103, 1}, {105, 2, 103, 2},
	} {
		got, err := SelectMinerAnchorHeight(step.latest, 1, step.gap, parent, 1, 20)
		if err != nil || got.SelectedHeight != step.want || got.ActualGap != step.actual {
			t.Fatalf("step=%+v got=%+v err=%v", step, got, err)
		}
		child := *parent
		child.BTCHeight = got.SelectedHeight
		child.BTCAnchorAgeBlocks, err = ExpectedBTCAnchorAgeBlocks(parent, child, 1, 20)
		if err != nil {
			t.Fatal(err)
		}
		parent = &child
	}
	parent = &ProfileSelectorPayload{BTCHeight: 98}
	// With M=2 and a fixed tip=100, consume 2 remaining reuses plus 3+3 newer states.
	for _, want := range []uint32{98, 98, 99, 99, 99, 100, 100, 100} {
		got, err := SelectMinerAnchorHeight(100, 1, 2, parent, 1, 2)
		if err != nil || got.SelectedHeight != want {
			t.Fatalf("got=%+v err=%v want=%d", got, err, want)
		}
		child := *parent
		child.BTCHeight = got.SelectedHeight
		child.BTCAnchorAgeBlocks, err = ExpectedBTCAnchorAgeBlocks(parent, child, 1, 2)
		if err != nil {
			t.Fatal(err)
		}
		parent = &child
	}
	if _, err := SelectMinerAnchorHeight(100, 1, 2, parent, 1, 2); !errors.Is(err, ErrBTCAnchorAgeExceeded) {
		t.Fatal(err)
	}
	if got, err := SelectMinerAnchorHeight(101, 1, 2, parent, 1, 2); err != nil || got.SelectedHeight != 101 {
		t.Fatalf("did not recover: %+v %v", got, err)
	}
}
