package domain

import "slices"

// Classify decides what happens to the incoming readings of one active
// device (spec п. 6.1):
//   - a reading whose measured_at is already stored, or repeats an earlier
//     incoming one, is a duplicate and is not returned;
//   - every other reading is accepted; it gets IsDecrease when its value is
//     below the reference — the latest reading without the flag that is
//     earlier than it, stored or incoming; with none, the initial value.
//
// before is the latest stored reading without the flag that is earlier than
// all incoming ones (nil if none). stored are the stored readings within
// [min, max] of the incoming times. Flags of stored readings are never
// recalculated. The result is in time order; incoming IsDecrease is ignored.
func Classify(initial Value, before *Reading, stored, incoming []Reading) (accepted []Reading, duplicates int) {
	byTime := func(x, y Reading) int { return x.MeasuredAt.Compare(y.MeasuredAt) }
	stored = slices.SortedStableFunc(slices.Values(stored), byTime)
	incoming = slices.SortedStableFunc(slices.Values(incoming), byTime)

	ref := initial
	if before != nil {
		ref = before.Value
	}
	accepted = make([]Reading, 0, len(incoming))
	j := 0
	for i, in := range incoming {
		// Everything earlier than in is already in time order: advance the
		// reference through the stored readings before it.
		for j < len(stored) && stored[j].MeasuredAt.Before(in.MeasuredAt) {
			if !stored[j].IsDecrease {
				ref = stored[j].Value
			}
			j++
		}
		if (j < len(stored) && stored[j].MeasuredAt.Equal(in.MeasuredAt)) ||
			(i > 0 && incoming[i-1].MeasuredAt.Equal(in.MeasuredAt)) {
			duplicates++
			continue
		}
		in.IsDecrease = in.Value < ref
		if !in.IsDecrease {
			ref = in.Value
		}
		accepted = append(accepted, in)
	}
	return accepted, duplicates
}
