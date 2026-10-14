package usdb

import "fmt"

// DefaultMinerAnchorGap is the UIP-0018 local mining preference, in BTC blocks.
// It is not a consensus parameter and explicit zero selects the latest state.
const DefaultMinerAnchorGap uint32 = 2

// MinerAnchorSelection describes one local height decision, not a wire field.
type MinerAnchorSelection struct {
	LatestHeight   uint32
	SelectedHeight uint32
	ConfiguredGap  uint32
	ActualGap      uint32
	Reason         string
}

// SelectMinerAnchorHeight applies UIP-0018 without querying or ranking passes.
// Full state identity and candidate validation must follow this height decision.
func SelectMinerAnchorHeight(latest, origin, gap uint32, parent *ProfileSelectorPayload, policy uint16, maxAge uint32) (MinerAnchorSelection, error) {
	selection := MinerAnchorSelection{LatestHeight: latest, ConfiguredGap: gap, Reason: "gap"}
	// Dispatch before doing policy-specific arithmetic, including the zero cap.
	if _, err := ExpectedBTCAnchorAgeBlocks(nil, ProfileSelectorPayload{}, policy, maxAge); err != nil {
		return selection, err
	}
	if latest < origin {
		return selection, fmt.Errorf("%w: latest_height=%d origin_height=%d gap=%d", ErrStateNotRetained, latest, origin, gap)
	}
	if parent != nil && parent.BTCHeight > latest {
		return selection, fmt.Errorf("%w: latest_height=%d parent_height=%d gap=%d; waiting for parent state", ErrBTCAnchorHeightRegression, latest, parent.BTCHeight, gap)
	}
	height := uint32(0)
	if latest >= gap {
		height = latest - gap
	}
	if height < origin || latest < gap {
		height, selection.Reason = origin, "history_origin"
	}
	if parent != nil && height < parent.BTCHeight {
		height, selection.Reason = parent.BTCHeight, "parent_anchor"
	}
	if parent != nil && height == parent.BTCHeight && parent.BTCAnchorAgeBlocks >= maxAge {
		if latest == height {
			return selection, fmt.Errorf("%w: latest_height=%d parent_height=%d parent_age=%d max_age=%d gap=%d; waiting for newer state", ErrBTCAnchorAgeExceeded, latest, parent.BTCHeight, parent.BTCAnchorAgeBlocks, maxAge, gap)
		}
		// latest > parent guarantees this increment cannot overflow.
		height, selection.Reason = parent.BTCHeight+1, "age_limit"
	}
	selection.SelectedHeight, selection.ActualGap = height, latest-height
	return selection, nil
}
