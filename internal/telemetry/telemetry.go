// Package telemetry reports player health back to Screenlet Studio so the
// Dispositivos panel can show online/offline state without needing SSH.
package telemetry

import "time"

// Heartbeat is the periodic status payload sent to Screenlet Studio.
type Heartbeat struct {
	DeviceID      string    `json:"deviceId"`
	PlayerVersion string    `json:"playerVersion"`
	Playing       bool      `json:"playing"`
	Source        string    `json:"source,omitempty"`
	SentAt        time.Time `json:"sentAt"`
}

// Reporter sends periodic heartbeats to Screenlet Studio.
type Reporter interface {
	// Start begins sending heartbeats at the given interval, calling next
	// each tick to build the payload.
	Start(interval time.Duration, next func() Heartbeat) error
	Stop()
}
