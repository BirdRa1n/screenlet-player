// Package display describes the output the player renders into.
package display

// Mode describes the output configuration the player renders into.
type Mode struct {
	Width      int  `json:"width"`
	Height     int  `json:"height"`
	RefreshHz  int  `json:"refreshHz"`
	Fullscreen bool `json:"fullscreen"`
}

// Detector reports the active display's current mode. The first
// implementation will likely read /sys/class/drm directly or shell out to
// kmsprint/xrandr — see docs/ROADMAP.md for sequencing.
type Detector interface {
	Detect() (Mode, error)
}
