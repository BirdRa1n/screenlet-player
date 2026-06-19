package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BirdRa1n/screenlet-player/internal/playback"
)

const testToken = "test-token-0123456789abcdef"

func newClaimedServer() *Server {
	return New(playback.NewNoopPlayer(), Options{
		Info:  Info{DeviceID: "dev-1", Hostname: "host-1", PlayerVersion: "v0.0.0-test"},
		Token: testToken,
		Mint: func(string) (ClaimResult, error) {
			t := testToken
			return ClaimResult{Token: t, DeviceID: "dev-1", PairingCode: "ABCDE"}, nil
		},
	})
}

func authed(req *http.Request) *http.Request {
	req.Header.Set("Authorization", "Bearer "+testToken)
	return req
}

func TestHandleStatus_RequiresAuth(t *testing.T) {
	srv := newClaimedServer()

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status without token = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleStatus_RejectsWrongToken(t *testing.T) {
	srv := newClaimedServer()

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status with wrong token = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleStatus_AcceptsValidToken(t *testing.T) {
	srv := newClaimedServer()

	req := authed(httptest.NewRequest(http.MethodGet, "/status", nil))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), `"playing":false`) {
		t.Fatalf("body = %q, want it to contain playing:false", rec.Body.String())
	}
}

func TestHandleStatus_UnclaimedDeviceRejectsEvenWithoutToken(t *testing.T) {
	srv := New(playback.NewNoopPlayer(), Options{
		Mint: func(string) (ClaimResult, error) { return ClaimResult{}, nil },
	})

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status on unclaimed device = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestHandlePlayAndStop(t *testing.T) {
	srv := newClaimedServer()

	playReq := authed(httptest.NewRequest(http.MethodPost, "/play", strings.NewReader(`{"source":"http://example.com/a.m3u"}`)))
	playRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(playRec, playReq)
	if playRec.Code != http.StatusNoContent {
		t.Fatalf("/play status = %d, want %d", playRec.Code, http.StatusNoContent)
	}

	stopReq := authed(httptest.NewRequest(http.MethodPost, "/stop", nil))
	stopRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(stopRec, stopReq)
	if stopRec.Code != http.StatusNoContent {
		t.Fatalf("/stop status = %d, want %d", stopRec.Code, http.StatusNoContent)
	}
}

func TestHandlePlay_RejectsNonHTTPSource(t *testing.T) {
	srv := newClaimedServer()

	cases := []string{
		`{"source":"file:///etc/passwd"}`,
		`{"source":"/etc/passwd"}`,
		`{"source":"javascript:alert(1)"}`,
		`{"source":""}`,
	}
	for _, body := range cases {
		req := authed(httptest.NewRequest(http.MethodPost, "/play", strings.NewReader(body)))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("/play with body %q = %d, want %d", body, rec.Code, http.StatusBadRequest)
		}
	}
}

func TestHandleIdentify_NeverRequiresAuth(t *testing.T) {
	srv := newClaimedServer()

	req := httptest.NewRequest(http.MethodGet, "/identify", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/identify = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), `"claimed":true`) {
		t.Fatalf("body = %q, want claimed:true", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), testToken) {
		t.Fatalf("body = %q, must never leak the token", rec.Body.String())
	}
}

func TestHandleClaim_MintsTokenOnce(t *testing.T) {
	const mintedToken = "freshly-minted-token"
	var gotStudioURL string

	srv := New(playback.NewNoopPlayer(), Options{
		Info: Info{DeviceID: "dev-2", Hostname: "host-2", PlayerVersion: "v0.0.0-test"},
		Mint: func(studioURL string) (ClaimResult, error) {
			gotStudioURL = studioURL
			return ClaimResult{Token: mintedToken, DeviceID: "dev-2", PairingCode: "FGHJK"}, nil
		},
	})

	identifyRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(identifyRec, httptest.NewRequest(http.MethodGet, "/identify", nil))
	if !strings.Contains(identifyRec.Body.String(), `"claimed":false`) {
		t.Fatalf("expected unclaimed before /claim, got %q", identifyRec.Body.String())
	}

	claimReq := httptest.NewRequest(http.MethodPost, "/claim", strings.NewReader(`{"studioUrl":"http://192.168.1.10:7095"}`))
	claimRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(claimRec, claimReq)

	if claimRec.Code != http.StatusOK {
		t.Fatalf("/claim status = %d, want %d, body=%q", claimRec.Code, http.StatusOK, claimRec.Body.String())
	}
	if !strings.Contains(claimRec.Body.String(), mintedToken) {
		t.Fatalf("/claim body = %q, want it to contain the minted token", claimRec.Body.String())
	}
	if gotStudioURL != "http://192.168.1.10:7095" {
		t.Fatalf("Mint received studioURL = %q, want the one from the request body", gotStudioURL)
	}

	// Now claimed: a request without a token must be rejected, and a second
	// claim attempt must be rejected outright (reset requires local access).
	statusRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(statusRec, httptest.NewRequest(http.MethodGet, "/status", nil))
	if statusRec.Code != http.StatusUnauthorized {
		t.Fatalf("/status without token after claim = %d, want %d", statusRec.Code, http.StatusUnauthorized)
	}

	authedStatusReq := httptest.NewRequest(http.MethodGet, "/status", nil)
	authedStatusReq.Header.Set("Authorization", "Bearer "+mintedToken)
	authedStatusRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(authedStatusRec, authedStatusReq)
	if authedStatusRec.Code != http.StatusOK {
		t.Fatalf("/status with the freshly minted token = %d, want %d", authedStatusRec.Code, http.StatusOK)
	}
}

func TestHandleClaim_RejectsSecondAttempt(t *testing.T) {
	srv := newClaimedServer() // already claimed via Options.Token

	req := httptest.NewRequest(http.MethodPost, "/claim", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("/claim on already-claimed device = %d, want %d", rec.Code, http.StatusConflict)
	}
}
