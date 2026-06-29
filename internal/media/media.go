// Package media manages the player's local cache of channel assets and the
// manifest describing what to play. It is what lets a device keep showing the
// last known programming after a reboot even when no server is reachable: the
// video files live on disk and the manifest records their order, so once
// content has been cached at least once, playback never again depends on the
// network.
//
// Everything written here is durable and verified: downloads are checked
// against the manifest's content hash before they are allowed to become a
// cached file, and both the manifest and each asset are written atomically so
// a crash or power cut can never leave a truncated file that the player would
// later try to play.
package media

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/BirdRa1n/screenlet-player/internal/storage"
)

// Item is a single asset in a channel's playlist.
type Item struct {
	Filename   string `json:"filename"`
	URL        string `json:"url"`
	Size       int64  `json:"size"`
	Hash       string `json:"hash"` // "sha256:<hex>"
	Transition string `json:"transition,omitempty"`
}

// Manifest is the offline playback definition for a channel: the ordered set
// of assets plus a version digest the device uses to detect changes cheaply.
type Manifest struct {
	ChannelID string `json:"channelId"`
	Version   string `json:"version"`
	Loop      bool   `json:"loop"`
	Items     []Item `json:"items"`
}

const manifestFile = "manifest.json"

func manifestPath() (string, error) {
	dir, err := storage.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, manifestFile), nil
}

// LoadManifest reads the persisted manifest. ok is false (with a nil error)
// when none has been saved yet — a first-boot device simply has nothing cached.
func LoadManifest() (m *Manifest, ok bool, err error) {
	path, err := manifestPath()
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var man Manifest
	if err := json.Unmarshal(data, &man); err != nil {
		return nil, false, err
	}
	return &man, true, nil
}

// SaveManifest atomically persists the manifest so a crash mid-write can never
// leave a truncated, unparseable file behind.
func SaveManifest(m *Manifest) error {
	path, err := manifestPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data, 0o600)
}

// Clear removes the persisted manifest and the entire media cache. Used by the
// player's -reset flow so a device handed to a different Studio instance does
// not keep playing the previous owner's content.
func Clear() error {
	path, err := manifestPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	dir, err := mediaDir()
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
