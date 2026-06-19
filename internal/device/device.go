// Package device manages this player's identity: a stable random ID
// generated on first run and persisted across restarts, used by Screenlet
// Studio to recognize the device through pairing and sync.
package device

import (
	"crypto/rand"
	"encoding/hex"
	"os"

	"github.com/BirdRa1n/screenlet-player/internal/storage"
)

// Identity uniquely identifies this physical player to Screenlet Studio.
type Identity struct {
	ID       string
	Hostname string
}

// LoadOrCreate returns the device's persisted identity, generating and
// saving a new random ID on first run so it survives restarts.
func LoadOrCreate() (*Identity, error) {
	cfg, err := storage.Load()
	if err != nil {
		return nil, err
	}

	if cfg.DeviceID == "" {
		id, err := newID()
		if err != nil {
			return nil, err
		}
		cfg.DeviceID = id
		if err := storage.Save(cfg); err != nil {
			return nil, err
		}
	}

	hostname, _ := os.Hostname()
	return &Identity{ID: cfg.DeviceID, Hostname: hostname}, nil
}

func newID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
