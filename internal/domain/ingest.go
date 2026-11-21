package domain

import (
	"cmp"
	"slices"
)

// DeviceRejection is why readings of a device in status s are not stored
// (spec п. 5): pending — device_not_active, blocked and rejected —
// device_blocked; "" for an active device.
func DeviceRejection(s DeviceStatus) RejectReason {
	switch s {
	case DeviceActive:
		return ""
	case DevicePending:
		return ReasonDeviceNotActive
	default:
		return ReasonDeviceBlocked
	}
}

// RejectAll lists every reading of item as rejected with reason.
func RejectAll(item BatchItem, reason RejectReason) []Rejected {
	out := make([]Rejected, 0, len(item.Readings))
	for _, r := range item.Readings {
		out = append(out, Rejected{DeviceSerial: item.Serial, Index: r.Index, Reason: reason})
	}
	return out
}

// SortRejected orders rejections as the gateway sent them: by the position of
// the device in b, then by index (device_status, -1, first). The result is
// never nil, so the answer always carries "rejected": [].
func SortRejected(b Batch, rejected []Rejected) []Rejected {
	pos := make(map[string]int, len(b.Items))
	for i, it := range b.Items {
		pos[it.Serial] = i
	}
	out := slices.Clone(rejected)
	if out == nil {
		out = []Rejected{}
	}
	slices.SortStableFunc(out, func(x, y Rejected) int {
		return cmp.Or(cmp.Compare(pos[x.DeviceSerial], pos[y.DeviceSerial]), cmp.Compare(x.Index, y.Index))
	})
	return out
}

// Readings strips the indexes.
func Readings(items []ItemReading) []Reading {
	out := make([]Reading, len(items))
	for i, r := range items {
		out[i] = r.Reading
	}
	return out
}
