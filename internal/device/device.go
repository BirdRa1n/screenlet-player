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

// pairingAlphabet excludes 0/O and 1/I so codes are unambiguous when read
// off a log or, eventually, displayed on screen.
const pairingAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// PairingCode returns this device's persisted human-readable pairing code,
// generating and saving a new one on first call. An admin types this code
// into Screenlet Studio's Dispositivos panel to claim the device — see
// docs/PAIRING.md.
func PairingCode() (string, error) {
	cfg, err := storage.Load()
	if err != nil {
		return "", err
	}

	if cfg.PairingCode == "" {
		code, err := newPairingCode()
		if err != nil {
			return "", err
		}
		cfg.PairingCode = code
		if err := storage.Save(cfg); err != nil {
			return "", err
		}
	}

	return cfg.PairingCode, nil
}

func newPairingCode() (string, error) {
	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	code := make([]byte, len(buf))
	for i, b := range buf {
		code[i] = pairingAlphabet[int(b)%len(pairingAlphabet)]
	}
	return string(code), nil
}
