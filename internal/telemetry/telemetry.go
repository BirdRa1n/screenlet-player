// Package telemetry reports player health back to Screenlet Studio so the
// Dispositivos panel can show online/offline state — and, before pairing,
// surface the device at all — without needing SSH.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// Heartbeat is the periodic status payload sent to Screenlet Studio.
// Hostname, PairingCode and PlayerVersion are included on every beat, not
// just the first one, so Studio can show them in the Dispositivos panel
// even for a device that announces itself but is never paired.
type Heartbeat struct {
	DeviceID      string    `json:"deviceId"`
	Hostname      string    `json:"hostname"`
	PairingCode   string    `json:"pairingCode"`
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

// HTTPReporter implements Reporter by POSTing heartbeats to Screenlet
// Studio's IPTV server.
type HTTPReporter struct {
	baseURL string
	http    *http.Client
	stop    chan struct{}
}

// NewHTTPReporter creates a Reporter targeting the given Studio base URL
// (e.g. "http://192.168.1.10:7095").
func NewHTTPReporter(baseURL string) *HTTPReporter {
	return &HTTPReporter{baseURL: baseURL, http: &http.Client{Timeout: 5 * time.Second}}
}

// Start sends one heartbeat immediately, then on every tick of interval.
func (r *HTTPReporter) Start(interval time.Duration, next func() Heartbeat) error {
	r.stop = make(chan struct{})

	send := func() {
		hb := next()
		body, err := json.Marshal(hb)
		if err != nil {
			log.Printf("telemetry: %v", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), interval/2)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/api/player/heartbeat", bytes.NewReader(body))
		if err != nil {
			log.Printf("telemetry: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := r.http.Do(req)
		if err != nil {
			log.Printf("telemetry: %v", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			log.Printf("telemetry: unexpected status %d", resp.StatusCode)
		}
	}

	go func() {
		send()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-r.stop:
				return
			case <-ticker.C:
				send()
			}
		}
	}()
	return nil
}

// Stop halts heartbeat sending. Safe to call once.
func (r *HTTPReporter) Stop() {
	if r.stop != nil {
		close(r.stop)
	}
}
