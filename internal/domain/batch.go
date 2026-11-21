package domain

import (
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// BatchRequest is the body of POST /gateway/v1/readings as the gateway sends
// it (spec п. 6.1). It is checked by ValidateBatch before anything is stored.
type BatchRequest struct {
	BatchID string        `json:"batch_id"`
	Items   []ItemRequest `json:"items"`
}

// ItemRequest is one device in a batch.
type ItemRequest struct {
	DeviceSerial string           `json:"device_serial"`
	Model        string           `json:"model"`
	DeviceStatus *DeviceState     `json:"device_status,omitempty"`
	Readings     []ReadingRequest `json:"readings"`
}

// ReadingRequest is a reading as sent: the value is a string, parsed by
// ValidateBatch so a bad value rejects one reading, not the whole batch.
type ReadingRequest struct {
	MeasuredAt time.Time `json:"measured_at"`
	Value      string    `json:"value"`
}

// RejectReason says why a reading (or a device_status) was not stored.
type RejectReason string

// Reject reasons (spec п. 5, 6.1).
const (
	ReasonFutureTS        RejectReason = "future_ts"
	ReasonTooOld          RejectReason = "too_old"
	ReasonInvalidValue    RejectReason = "invalid_value"
	ReasonDeviceNotActive RejectReason = "device_not_active"
	ReasonDeviceBlocked   RejectReason = "device_blocked"
	ReasonInvalidStatus   RejectReason = "invalid_status"
)

// StatusIndex is the index of a rejected device_status (there is no reading).
const StatusIndex = -1

// Rejected is one entry of the "rejected" list in the answer to the gateway.
// Index is the position in the readings array of the item as sent.
type Rejected struct {
	DeviceSerial string       `json:"device_serial"`
	Index        int          `json:"index"`
	Reason       RejectReason `json:"reason"`
}

// BatchResult is the answer to a batch; it is also stored for replays.
type BatchResult struct {
	Accepted   int        `json:"accepted"`
	Duplicates int        `json:"duplicates"`
	Rejected   []Rejected `json:"rejected"`
}

// Batch is a checked batch: one item per serial, readings sorted by time,
// only readings that passed the checks.
type Batch struct {
	ID    string
	Items []BatchItem
}

// BatchItem is one device of a checked batch.
type BatchItem struct {
	Serial   string
	Model    string
	State    *DeviceState  // valid fields only; nil if none
	Readings []ItemReading // sorted by MeasuredAt, stable for equal times
}

// ItemReading is a checked reading with its index as sent by the gateway.
type ItemReading struct {
	Index int
	Reading
}

// Batch limits.
const (
	MaxBatchReadings = 5000 // spec п. 6.1: more is 400
	MaxBatchIDLen    = 64
	MaxSerialLen     = 64
	MaxModelRunes    = 64
	MaxErrorCodes    = 16
	MaxErrorCodeLen  = 64

	FutureTolerance = 5 * time.Minute
	MaxReadingAge   = 90 * 24 * time.Hour

	BatteryMin, BatteryMax = 0, 100
	RSSIMin, RSSIMax       = -150, 0
)

// ValidateBatch checks a batch received at now. A broken structure or a limit
// is an error (400 for the whole batch). Bad readings and bad device_status
// fields are only left out and listed in the returned rejections; the rest of
// the batch is kept.
//
// Items with the same serial are merged: readings are joined, device_status
// fields of a later item override earlier ones, the first model wins. Each
// reading keeps the index it had in its own item.
func ValidateBatch(req BatchRequest, now time.Time) (Batch, []Rejected, error) {
	if !printableASCII(req.BatchID, MaxBatchIDLen) {
		return Batch{}, nil, Invalidf("batch_id must be 1 to %d printable ASCII characters without spaces", MaxBatchIDLen)
	}
	if len(req.Items) == 0 {
		return Batch{}, nil, Invalidf("items must not be empty")
	}
	total := 0
	for _, it := range req.Items {
		total += len(it.Readings)
	}
	if total > MaxBatchReadings {
		return Batch{}, nil, Invalidf("a batch may hold at most %d readings, got %d", MaxBatchReadings, total)
	}

	b := Batch{ID: req.BatchID}
	bySerial := make(map[string]int, len(req.Items)) // serial -> position in b.Items
	var rejected []Rejected
	for i, it := range req.Items {
		if !printableASCII(it.DeviceSerial, MaxSerialLen) {
			return Batch{}, nil, Invalidf("items[%d]: device_serial must be 1 to %d printable ASCII characters without spaces", i, MaxSerialLen)
		}
		model, ok := validModel(it.Model)
		if !ok {
			return Batch{}, nil, Invalidf("items[%d]: model must be 1 to %d characters", i, MaxModelRunes)
		}
		if len(it.Readings) == 0 && it.DeviceStatus == nil {
			return Batch{}, nil, Invalidf("items[%d]: neither readings nor device_status", i)
		}

		pos, seen := bySerial[it.DeviceSerial]
		if !seen {
			pos = len(b.Items)
			bySerial[it.DeviceSerial] = pos
			b.Items = append(b.Items, BatchItem{Serial: it.DeviceSerial, Model: model})
		}
		item := &b.Items[pos]

		if it.DeviceStatus != nil {
			state, bad := checkState(*it.DeviceStatus)
			item.State = mergeState(item.State, state)
			if bad {
				rejected = append(rejected, Rejected{it.DeviceSerial, StatusIndex, ReasonInvalidStatus})
			}
		}
		for j, r := range it.Readings {
			reason, v := checkReading(r, now)
			if reason != "" {
				rejected = append(rejected, Rejected{it.DeviceSerial, j, reason})
				continue
			}
			// PostgreSQL keeps microseconds: truncating here makes duplicates
			// the same for every storage and for the gateway.
			at := r.MeasuredAt.Truncate(time.Microsecond).UTC()
			item.Readings = append(item.Readings, ItemReading{Index: j, Reading: Reading{MeasuredAt: at, Value: v}})
		}
	}
	for i := range b.Items {
		slices.SortStableFunc(b.Items[i].Readings, func(x, y ItemReading) int {
			return x.MeasuredAt.Compare(y.MeasuredAt)
		})
	}
	return b, rejected, nil
}

// checkReading returns the reject reason, or "" and the parsed value.
// The boundaries are inclusive: exactly now+5m and exactly now-90d pass.
func checkReading(r ReadingRequest, now time.Time) (RejectReason, Value) {
	switch {
	case r.MeasuredAt.After(now.Add(FutureTolerance)):
		return ReasonFutureTS, 0
	case r.MeasuredAt.Before(now.Add(-MaxReadingAge)):
		return ReasonTooOld, 0
	}
	v, err := ParseValue(r.Value)
	if err != nil {
		return ReasonInvalidValue, 0
	}
	return "", v
}

// checkState keeps the valid fields of s; bad reports whether any was dropped.
func checkState(s DeviceState) (valid DeviceState, bad bool) {
	if p := s.BatteryPct; p != nil {
		if *p >= BatteryMin && *p <= BatteryMax {
			valid.BatteryPct = p
		} else {
			bad = true
		}
	}
	if p := s.RSSI; p != nil {
		if *p >= RSSIMin && *p <= RSSIMax {
			valid.RSSI = p
		} else {
			bad = true
		}
	}
	if p := s.ErrorCodes; p != nil {
		if validErrorCodes(*p) {
			codes := slices.Clone(*p)
			if codes == nil {
				codes = []string{} // keep "empty" distinct from "not sent"
			}
			valid.ErrorCodes = &codes
		} else {
			bad = true
		}
	}
	return valid, bad
}

func validErrorCodes(codes []string) bool {
	if len(codes) > MaxErrorCodes {
		return false
	}
	for _, c := range codes {
		if !printableASCII(c, MaxErrorCodeLen) {
			return false
		}
	}
	return true
}

// mergeState lays the set fields of next over prev; nil if nothing is set.
func mergeState(prev *DeviceState, next DeviceState) *DeviceState {
	var m DeviceState
	if prev != nil {
		m = *prev
	}
	if next.BatteryPct != nil {
		m.BatteryPct = next.BatteryPct
	}
	if next.RSSI != nil {
		m.RSSI = next.RSSI
	}
	if next.ErrorCodes != nil {
		m.ErrorCodes = next.ErrorCodes
	}
	if m.BatteryPct == nil && m.RSSI == nil && m.ErrorCodes == nil {
		return nil
	}
	return &m
}

// printableASCII: 1 to maxLen bytes in '!'..'~' (no spaces, no control
// characters, no Unicode look-alikes).
func printableASCII(s string, maxLen int) bool {
	if s == "" || len(s) > maxLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '!' || s[i] > '~' {
			return false
		}
	}
	return true
}

// validModel trims the model and checks it is 1–64 printable characters.
func validModel(s string) (string, bool) {
	m := strings.TrimSpace(s)
	if m == "" || !utf8.ValidString(m) || utf8.RuneCountInString(m) > MaxModelRunes {
		return "", false
	}
	for _, r := range m {
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	return m, true
}
