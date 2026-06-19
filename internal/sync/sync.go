// Package sync polls Screenlet Studio for this device's current channel
// assignment, replacing the SSH-triggered Kodi restart used by the legacy
// bridge with an ordinary pull-based config refresh.
package sync

import "time"

// ChannelAssignment is what Screenlet Studio tells a device to play.
type ChannelAssignment struct {
	ChannelID   string    `json:"channelId"`
	PlaylistURL string    `json:"playlistUrl"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Syncer periodically polls Screenlet Studio for the device's current
// channel assignment and notifies subscribers when it changes.
type Syncer interface {
	// Start begins polling at the given interval, invoking onChange whenever
	// the assignment differs from the last known one.
	Start(interval time.Duration, onChange func(ChannelAssignment)) error
	Stop()
}
