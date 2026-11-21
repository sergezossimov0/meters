package domain

import (
	"slices"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)

// at returns t0 + h hours.
func at(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }

// rd builds a reading at hour h with an integer value; dec sets the flag.
func rd(h int, v int64, dec bool) Reading {
	return Reading{MeasuredAt: at(h), Value: Value(v * 1000), IsDecrease: dec}
}

func TestClassify(t *testing.T) {
	r100 := rd(0, 100, false)
	tests := []struct {
		name     string
		initial  Value
		before   *Reading
		stored   []Reading
		incoming []Reading
		want     []Reading
		dups     int
	}{
		{
			name:     "growing values, reference is initial",
			initial:  50_000,
			incoming: []Reading{rd(1, 60, false), rd(2, 70, false)},
			want:     []Reading{rd(1, 60, false), rd(2, 70, false)},
		},
		{
			name:     "below initial: first accepted reading is a decrease",
			initial:  100_000,
			incoming: []Reading{rd(1, 99, false)},
			want:     []Reading{rd(1, 99, true)},
		},
		{
			name:     "equal to reference is not a decrease",
			before:   &r100,
			incoming: []Reading{rd(1, 100, false)},
			want:     []Reading{rd(1, 100, false)},
		},
		{
			name:     "roll-back 100 -> 90 -> 91 in one batch: both flagged",
			before:   &r100,
			incoming: []Reading{rd(1, 90, false), rd(2, 91, false), rd(3, 101, false)},
			want:     []Reading{rd(1, 90, true), rd(2, 91, true), rd(3, 101, false)},
		},
		{
			name:     "roll-back across batches: 90 is stored flagged, 91 compares with 100",
			before:   &r100,
			stored:   []Reading{rd(1, 90, true)},
			incoming: []Reading{rd(1, 90, false), rd(2, 91, false)},
			want:     []Reading{rd(2, 91, true)},
			dups:     1,
		},
		{
			name:     "incoming sent unsorted is sorted by time",
			initial:  0,
			incoming: []Reading{rd(3, 30, false), rd(1, 10, false), rd(2, 20, false)},
			want:     []Reading{rd(1, 10, false), rd(2, 20, false), rd(3, 30, false)},
		},
		{
			name:     "late reading between stored ones uses the earlier stored as reference",
			before:   &r100,
			stored:   []Reading{rd(1, 110, false), rd(3, 130, false)},
			incoming: []Reading{rd(2, 120, false), rd(4, 125, false)},
			want:     []Reading{rd(2, 120, false), rd(4, 125, true)},
		},
		{
			name:     "late reading below the earlier stored is a decrease, stored flags stay",
			before:   &r100,
			stored:   []Reading{rd(1, 110, false), rd(3, 130, false)},
			incoming: []Reading{rd(2, 105, false)},
			want:     []Reading{rd(2, 105, true)},
		},
		{
			name:     "flagged stored reading is skipped as reference",
			before:   &r100,
			stored:   []Reading{rd(1, 50, true)},
			incoming: []Reading{rd(2, 60, false)},
			want:     []Reading{rd(2, 60, true)},
		},
		{
			name:     "duplicate of a stored reading is not accepted even with another value",
			before:   &r100,
			stored:   []Reading{rd(1, 110, false)},
			incoming: []Reading{rd(1, 999, false), rd(2, 120, false)},
			want:     []Reading{rd(2, 120, false)},
			dups:     1,
		},
		{
			name:     "duplicate inside the batch: the first one wins",
			initial:  0,
			incoming: []Reading{rd(1, 10, false), rd(1, 5, false), rd(1, 7, false)},
			want:     []Reading{rd(1, 10, false)},
			dups:     2,
		},
		{
			name:     "accepted incoming decrease does not move the reference",
			initial:  0,
			incoming: []Reading{rd(1, 100, false), rd(2, 90, false), rd(3, 95, false), rd(4, 100, false)},
			want:     []Reading{rd(1, 100, false), rd(2, 90, true), rd(3, 95, true), rd(4, 100, false)},
		},
		{
			name: "nothing incoming",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := slices.Clone(tt.stored)
			incoming := slices.Clone(tt.incoming)
			got, dups := Classify(tt.initial, tt.before, tt.stored, tt.incoming)
			if !slices.EqualFunc(got, tt.want, sameReading) || dups != tt.dups {
				t.Errorf("got %v, %d duplicates; want %v, %d", got, dups, tt.want, tt.dups)
			}
			if !slices.Equal(stored, tt.stored) || !slices.Equal(incoming, tt.incoming) {
				t.Error("Classify must not modify its arguments")
			}
		})
	}
}

func sameReading(a, b Reading) bool {
	return a.MeasuredAt.Equal(b.MeasuredAt) && a.Value == b.Value && a.IsDecrease == b.IsDecrease
}
