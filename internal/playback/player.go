// Package playback defines the Player interface every media backend must
// implement, plus a NoopPlayer used as a safe default until a real engine
// is wired in.
package playback

// Status describes what the player is currently doing.
type Status struct {
	Playing  bool    `json:"playing"`
	Source   string  `json:"source,omitempty"`
	Position float64 `json:"position"`
}

// Player is the interface every playback backend must implement. The
// first concrete implementation targets mpv over its JSON IPC socket (see
// docs/ARCHITECTURE.md) — chosen for hardware-accelerated decode on
// Raspberry Pi / low-power Linux boards without bundling a full media
// center like Kodi.
type Player interface {
	// Play starts (or switches to) playing the given source URL.
	Play(source string) error
	// PlayPlaylist starts (or switches to) playing an ordered list of local
	// files, looping the whole list when loop is true. This is the offline
	// signage path: the files are served from the device's own media cache,
	// so playback continues across reboots without a reachable server.
	PlayPlaylist(paths []string, loop bool) error
	// Stop halts playback and releases the display.
	Stop() error
	// Status reports the current playback state.
	Status() (Status, error)
	// Close releases any resources held by the backend (sockets, processes).
	Close() error
}

// NoopPlayer is a placeholder backend used until a real playback engine is
// wired in. It tracks requested state without rendering anything — useful
// for exercising the API and pairing flow on a dev machine with no
// display attached.
type NoopPlayer struct {
	status Status
}

// NewNoopPlayer creates a Player that tracks requested state without
// rendering anything.
func NewNoopPlayer() *NoopPlayer {
	return &NoopPlayer{}
}

func (p *NoopPlayer) Play(source string) error {
	p.status = Status{Playing: true, Source: source}
	return nil
}

func (p *NoopPlayer) PlayPlaylist(paths []string, _ bool) error {
	src := ""
	if len(paths) > 0 {
		src = paths[0]
	}
	p.status = Status{Playing: src != "", Source: src}
	return nil
}

func (p *NoopPlayer) Stop() error {
	p.status = Status{}
	return nil
}

func (p *NoopPlayer) Status() (Status, error) {
	return p.status, nil
}

func (p *NoopPlayer) Close() error {
	return nil
}
