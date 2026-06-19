// Package storage persists the player's local state — device identity,
// pairing info and the last known channel assignment — to a small JSON
// file. Anything that can be re-fetched from Screenlet Studio belongs in
// the sync package instead, not here.
package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config is the player's persisted local state.
type Config struct {
	DeviceID    string `json:"deviceId"`
	DeviceName  string `json:"deviceName,omitempty"`
	StudioURL   string `json:"studioUrl,omitempty"`
	PairingCode string `json:"pairingCode,omitempty"`
	ChannelID   string `json:"channelId,omitempty"`
}

// Dir returns the directory used to persist player state, creating it if
// needed. Honors $XDG_CONFIG_HOME, falling back to ~/.config.
func Dir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	dir := filepath.Join(base, "screenlet-player")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func configPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the persisted config, returning a zero-value Config if none
// exists yet (first run).
func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save persists the config to disk as indented JSON.
func Save(cfg *Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
