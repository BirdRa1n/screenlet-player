package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestHTTPReporterSendsHeartbeat(t *testing.T) {
	var mu sync.Mutex
	var received []Heartbeat

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var hb Heartbeat
		if err := json.NewDecoder(r.Body).Decode(&hb); err != nil {
			t.Errorf("decode body: %v", err)
		}
		mu.Lock()
		received = append(received, hb)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	reporter := NewHTTPReporter(srv.URL)
	want := Heartbeat{DeviceID: "device-1", Hostname: "tv-1", PairingCode: "AB12C", PlayerVersion: "v0.1.0", Playing: true, Source: "http://x/channel/1"}

	if err := reporter.Start(20*time.Millisecond, func() Heartbeat { return want }); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer reporter.Stop()

	time.Sleep(60 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatalf("expected at least one heartbeat to be received, got none")
	}
	got := received[0]
	if got.DeviceID != want.DeviceID || got.Hostname != want.Hostname || got.PairingCode != want.PairingCode || got.PlayerVersion != want.PlayerVersion || got.Playing != want.Playing || got.Source != want.Source {
		t.Fatalf("received heartbeat = %+v, want %+v", got, want)
	}
}
