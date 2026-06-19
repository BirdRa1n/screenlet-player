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

// APIToken returns the persisted control-API bearer token, or "" if this
// device hasn't been claimed yet.
func APIToken() (string, error) {
	cfg, err := storage.Load()
	if err != nil {
		return "", err
	}
	return cfg.APIToken, nil
}

// StudioURL returns the Screenlet Studio base URL learned via a previous
// /claim call, or "" if none is persisted (e.g. only -studio-url was ever
// used, or this device hasn't been claimed yet).
func StudioURL() (string, error) {
	cfg, err := storage.Load()
	if err != nil {
		return "", err
	}
	return cfg.StudioURL, nil
}

// GenerateAPIToken mints and persists a new control-API bearer token,
// overwriting any existing one. studioURL is persisted too when non-empty,
// so a device claimed without -studio-url at boot still knows where to
// send heartbeats/sync from then on. Called exactly once per claim by
// internal/api's injected mint function — see docs/PAIRING.md.
func GenerateAPIToken(studioURL string) (string, error) {
	cfg, err := storage.Load()
	if err != nil {
		return "", err
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)

	cfg.APIToken = token
	if studioURL != "" {
		cfg.StudioURL = studioURL
	}
	if err := storage.Save(cfg); err != nil {
		return "", err
	}
	return token, nil
}

// Reset wipes all persisted local state — device identity, pairing code,
// API token, studio URL, channel — so this device becomes unclaimed again
// and can be paired with a different Screenlet Studio instance. Intended
// to be reachable only via local/SSH access to the machine (the cmd's
// -reset flag), never over the network: it is the only way to undo a
// claim, by design.
func Reset() error {
	return storage.Save(&storage.Config{})
}
