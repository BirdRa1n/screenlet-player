package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BirdRa1n/screenlet-player/internal/playback"
)

func TestHandleStatus(t *testing.T) {
	srv := New(playback.NewNoopPlayer())
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), `"playing":false`) {
		t.Fatalf("body = %q, want it to contain playing:false", rec.Body.String())
	}
}

func TestHandlePlayAndStop(t *testing.T) {
	srv := New(playback.NewNoopPlayer())

	playReq := httptest.NewRequest(http.MethodPost, "/play", strings.NewReader(`{"source":"http://example.com/a.m3u"}`))
	playRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(playRec, playReq)
	if playRec.Code != http.StatusNoContent {
		t.Fatalf("/play status = %d, want %d", playRec.Code, http.StatusNoContent)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/stop", nil)
	stopRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(stopRec, stopReq)
	if stopRec.Code != http.StatusNoContent {
		t.Fatalf("/stop status = %d, want %d", stopRec.Code, http.StatusNoContent)
	}
}
