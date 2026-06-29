// Package sync polls Screenlet Studio for this device's current channel
// assignment, replacing the SSH-triggered Kodi restart used by the legacy
// bridge with an ordinary pull-based config refresh.
package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/BirdRa1n/screenlet-player/internal/media"
)

// ChannelAssignment is what Screenlet Studio tells a device to play. Items and
// Version come from Studio's offline manifest; PlaylistURL is the legacy
// live-stream URL kept as a fallback for servers (or channels) that advertise
// no downloadable items.
type ChannelAssignment struct {
	ChannelID   string       `json:"channelId"`
	PlaylistURL string       `json:"playlistUrl"`
	UpdatedAt   time.Time    `json:"updatedAt"`
	Loop        bool         `json:"loop"`
	Version     string       `json:"version"`
	Items       []media.Item `json:"items"`
}

// HasItems reports whether Studio advertised a downloadable, cacheable
// manifest (vs. only the legacy live-stream URL).
func (a ChannelAssignment) HasItems() bool { return len(a.Items) > 0 }

// Manifest converts the assignment into a media.Manifest for the cache.
func (a ChannelAssignment) Manifest() media.Manifest {
	return media.Manifest{ChannelID: a.ChannelID, Version: a.Version, Loop: a.Loop, Items: a.Items}
}

// changeKey is a comparable digest of the assignment used to detect changes.
// It keys on the content version when present (so an in-place re-render with
// the same filename is still noticed) and otherwise on the legacy fields.
func (a ChannelAssignment) changeKey() string {
	if a.Version != "" {
		return a.ChannelID + "@" + a.Version
	}
	return a.ChannelID + "|" + a.PlaylistURL + "|" + a.UpdatedAt.String()
}

// Syncer periodically polls Screenlet Studio for the device's current
// channel assignment and notifies subscribers when it changes.
type Syncer interface {
	// Start begins polling at the given interval, invoking onChange whenever
	// the assignment differs from the last known one.
	Start(interval time.Duration, onChange func(ChannelAssignment)) error
	Stop()
}

// syncResponse mirrors Screenlet Studio's GET /api/player/sync payload.
// Paired is false until an admin claims this device's pairing code in the
// Dispositivos panel — see docs/PAIRING.md.
type syncResponse struct {
	Paired      bool         `json:"paired"`
	ChannelID   string       `json:"channelId"`
	PlaylistURL string       `json:"playlistUrl"`
	UpdatedAt   time.Time    `json:"updatedAt"`
	Loop        bool         `json:"loop"`
	Version     string       `json:"version"`
	Items       []media.Item `json:"items"`
}

// Client talks to a Screenlet Studio instance over HTTP on behalf of one
// device. Studio's IPTV server (the same one serving playlist.m3u) hosts
// the /api/player/* routes this client calls.
type Client struct {
	baseURL  string
	deviceID string
	http     *http.Client
}

// NewClient creates a Client for the given Studio base URL (e.g.
// "http://192.168.1.10:7095") and this device's persisted ID.
func NewClient(baseURL, deviceID string) *Client {
	return &Client{baseURL: baseURL, deviceID: deviceID, http: &http.Client{Timeout: 5 * time.Second}}
}

// Fetch asks Studio for this device's current channel assignment. paired
// is false until an admin pairs the device — callers should treat that as
// "nothing to do yet", not an error.
func (c *Client) Fetch(ctx context.Context) (assignment ChannelAssignment, paired bool, err error) {
	endpoint := fmt.Sprintf("%s/api/player/sync?deviceId=%s", c.baseURL, url.QueryEscape(c.deviceID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ChannelAssignment{}, false, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return ChannelAssignment{}, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ChannelAssignment{}, false, fmt.Errorf("sync: unexpected status %d", resp.StatusCode)
	}

	var body syncResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ChannelAssignment{}, false, err
	}
	if !body.Paired {
		return ChannelAssignment{}, false, nil
	}
	return ChannelAssignment{
		ChannelID:   body.ChannelID,
		PlaylistURL: body.PlaylistURL,
		UpdatedAt:   body.UpdatedAt,
		Loop:        body.Loop,
		Version:     body.Version,
		Items:       body.Items,
	}, true, nil
}

// Poller implements Syncer by polling a Client on a fixed interval.
type Poller struct {
	client *Client
	stop   chan struct{}
}

// NewPoller creates a Poller backed by the given Client.
func NewPoller(client *Client) *Poller {
	return &Poller{client: client}
}

// Start begins polling immediately, then on every tick of interval. It
// returns control to the caller right away — polling happens in the
// background until Stop is called.
func (p *Poller) Start(interval time.Duration, onChange func(ChannelAssignment)) error {
	p.stop = make(chan struct{})

	var lastKey string
	var haveLast bool
	check := func() {
		ctx, cancel := context.WithTimeout(context.Background(), interval/2)
		defer cancel()
		assignment, paired, err := p.client.Fetch(ctx)
		if err != nil {
			log.Printf("sync: %v", err)
			return
		}
		if paired {
			if key := assignment.changeKey(); !haveLast || key != lastKey {
				lastKey = key
				haveLast = true
				onChange(assignment)
			}
		}
	}

	go func() {
		check()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-ticker.C:
				check()
			}
		}
	}()
	return nil
}

// Stop halts polling. Safe to call once; Start must be called again to resume.
func (p *Poller) Stop() {
	if p.stop != nil {
		close(p.stop)
	}
}
