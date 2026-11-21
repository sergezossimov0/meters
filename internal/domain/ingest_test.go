package domain

import (
	"slices"
	"testing"
)

func TestDeviceRejection(t *testing.T) {
	for status, want := range map[DeviceStatus]RejectReason{
		DeviceActive:   "",
		DevicePending:  ReasonDeviceNotActive,
		DeviceBlocked:  ReasonDeviceBlocked,
		DeviceRejected: ReasonDeviceBlocked,
	} {
		if got := DeviceRejection(status); got != want {
			t.Errorf("DeviceRejection(%s) = %q, want %q", status, got, want)
		}
	}
}

func TestSortRejected(t *testing.T) {
	b := Batch{Items: []BatchItem{{Serial: "B"}, {Serial: "A"}}}
	got := SortRejected(b, []Rejected{
		{"A", 1, ReasonDeviceNotActive},
		{"B", 2, ReasonTooOld},
		{"A", StatusIndex, ReasonInvalidStatus},
		{"B", 0, ReasonDeviceBlocked},
	})
	want := []Rejected{
		{"B", 0, ReasonDeviceBlocked},
		{"B", 2, ReasonTooOld},
		{"A", StatusIndex, ReasonInvalidStatus},
		{"A", 1, ReasonDeviceNotActive},
	}
	if !slices.Equal(got, want) {
		t.Errorf("SortRejected = %v\nwant %v", got, want)
	}
	if empty := SortRejected(b, nil); empty == nil || len(empty) != 0 {
		t.Errorf("SortRejected(nil) = %#v, want an empty non-nil slice", empty)
	}
}

func TestRejectAll(t *testing.T) {
	item := BatchItem{Serial: "A", Readings: []ItemReading{{Index: 3}, {Index: 0}}}
	want := []Rejected{{"A", 3, ReasonDeviceBlocked}, {"A", 0, ReasonDeviceBlocked}}
	if got := RejectAll(item, ReasonDeviceBlocked); !slices.Equal(got, want) {
		t.Errorf("RejectAll = %v, want %v", got, want)
	}
}
