package sync

import (
	"context"
	"net/http"
	"net/http/httptest"
	stdsync "sync"
	"testing"
	"time"
)

func TestClientFetchUnpaired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"paired":false}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "device-1")
	_, paired, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if paired {
		t.Fatalf("expected paired=false, got true")
	}
}

func TestClientFetchPaired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("deviceId"); got != "device-1" {
			t.Errorf("deviceId query param = %q, want %q", got, "device-1")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"paired":true,"channelId":"ch1","playlistUrl":"http://studio/channel/ch1","updatedAt":"2026-01-01T00:00:00Z"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "device-1")
	assignment, paired, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if !paired {
		t.Fatalf("expected paired=true, got false")
	}
	if assignment.ChannelID != "ch1" || assignment.PlaylistURL != "http://studio/channel/ch1" {
		t.Fatalf("Fetch() = %+v, unexpected values", assignment)
	}
}

func TestPollerNotifiesOnChange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"paired":true,"channelId":"ch1","playlistUrl":"http://studio/channel/ch1","updatedAt":"2026-01-01T00:00:00Z"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "device-1")
	poller := NewPoller(client)

	var mu stdsync.Mutex
	var calls int
	if err := poller.Start(20*time.Millisecond, func(ChannelAssignment) {
		mu.Lock()
		calls++
		mu.Unlock()
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer poller.Stop()

	time.Sleep(80 * time.Millisecond)

	mu.Lock()
	got := calls
	mu.Unlock()
	// The assignment never changes, so onChange should fire exactly once
	// (on first successful fetch) even though several polls happen.
	if got != 1 {
		t.Fatalf("onChange called %d times, want exactly 1", got)
	}
}
