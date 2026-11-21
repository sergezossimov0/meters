package domain

import "time"

// Gateway is a LPWAN gateway that sends readings to the server. Its API key
// is never stored, only sha256 of it (spec п. 6.1).
type Gateway struct {
	ID              int64
	Name            string
	Blocked         bool
	LastHeartbeatAt *time.Time
	CreatedAt       time.Time
}

// DeviceStatus is a step of the device life cycle (spec п. 5).
type DeviceStatus string

// Device statuses.
const (
	DevicePending  DeviceStatus = "pending"
	DeviceActive   DeviceStatus = "active"
	DeviceBlocked  DeviceStatus = "blocked"
	DeviceRejected DeviceStatus = "rejected"
)

// Device is a meter known to the server. It appears as pending with the
// first batch that mentions its serial; the admin fills subtype, owner,
// address and the initial value when approving it.
type Device struct {
	ID           int64
	Serial       string
	Model        string
	Status       DeviceStatus
	SubtypeID    *int
	OwnerID      *int64
	Address      string
	InitialValue *Value
	GatewayID    int64 // the gateway the device first appeared through
	CreatedAt    time.Time
	ActivatedAt  *time.Time
}

// Reading is one stored or incoming meter reading.
type Reading struct {
	MeasuredAt time.Time
	Value      Value
	IsDecrease bool // less than the reference reading (spec п. 6.1)
}

// DeviceState is the technical state a device reports (device_status).
// Pointers tell "not sent" from "empty": a nil ErrorCodes keeps the stored
// codes, a pointer to an empty slice means "no errors".
type DeviceState struct {
	BatteryPct *int      `json:"battery_pct,omitempty"`
	RSSI       *int      `json:"rssi,omitempty"`
	ErrorCodes *[]string `json:"error_codes,omitempty"`
}
