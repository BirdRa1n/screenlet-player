// Package api exposes a small local HTTP API so Screenlet Studio (or an
// admin) can query and control this player without SSH — covering the
// same ground the Studio's Kodi JSON-RPC bridge does today (status,
// play, stop), but served natively by the player itself.
//
// Every route except /identify and /claim requires the bearer token
// issued at claim time: until a device is claimed, nothing on it can be
// controlled remotely beyond reading /identify and calling /claim once.
// See docs/PAIRING.md for the full flow.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/BirdRa1n/screenlet-player/internal/playback"
)

// Info identifies this device to callers of /identify, before and after
// claiming.
type Info struct {
	DeviceID      string
	Hostname      string
	PlayerVersion string
}

// ClaimResult is what a successful /claim mints and returns.
type ClaimResult struct {
	Token       string
	DeviceID    string
	PairingCode string
}

// Options configures a Server.
type Options struct {
	Info Info
	// Token is the bearer token loaded from local storage at boot. Empty
	// means this device hasn't been claimed yet.
	Token string
	// Mint is called by the /claim handler the first (and only) time this
	// device is claimed. It must persist the new token — and studioURL,
	// when non-empty — and return it; see internal/device.GenerateAPIToken.
	// Required.
	Mint func(studioURL string) (ClaimResult, error)
	// OnClaimed runs after a successful claim. Optional — cmd uses it to
	// start the Studio sync/telemetry goroutines on demand if they weren't
	// already running (e.g. the device was claimed without -studio-url).
	OnClaimed func(token, studioURL string)
}

// Server exposes a small local HTTP API bound to a playback backend.
type Server struct {
	player    playback.Player
	info      Info
	mint      func(studioURL string) (ClaimResult, error)
	onClaimed func(token, studioURL string)

	mu    sync.Mutex
	token string
}

// New creates an API server bound to the given playback backend.
func New(player playback.Player, opts Options) *Server {
	return &Server{
		player:    player,
		info:      opts.Info,
		mint:      opts.Mint,
		onClaimed: opts.OnClaimed,
		token:     opts.Token,
	}
}

// Handler returns the http.Handler to mount.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/identify", s.handleIdentify)
	mux.HandleFunc("/claim", s.handleClaim)
	mux.HandleFunc("/status", s.requireAuth(s.handleStatus))
	mux.HandleFunc("/play", s.requireAuth(s.handlePlay))
	mux.HandleFunc("/stop", s.requireAuth(s.handleStop))
	return mux
}

func (s *Server) claimed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token != ""
}

// requireAuth wraps a handler so it only runs when the request carries the
// exact bearer token issued at claim time. The comparison is constant-time
// so a timing side channel can't be used to guess the token byte by byte.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		expected := s.token
		s.mu.Unlock()

		if expected == "" {
			http.Error(w, "device not claimed yet — pair it from Screenlet Studio first", http.StatusForbidden)
			return
		}

		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			http.Error(w, "invalid or missing token", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// handleIdentify is the only thing a not-yet-claimed device reveals: just
// enough for Screenlet Studio's network scan to recognize it as a
// compatible, unclaimed player. No playback state, no secrets.
func (s *Server) handleIdentify(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		DeviceID      string `json:"deviceId"`
		Hostname      string `json:"hostname"`
		PlayerVersion string `json:"playerVersion"`
		Claimed       bool   `json:"claimed"`
	}{
		DeviceID:      s.info.DeviceID,
		Hostname:      s.info.Hostname,
		PlayerVersion: s.info.PlayerVersion,
		Claimed:       s.claimed(),
	})
}

// handleClaim binds this device to exactly one caller, permanently, until
// a local -reset. The studioUrl in the request body is optional: a device
// already started with -studio-url doesn't need it repeated here.
func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token != "" {
		http.Error(w, "already claimed — reset requires local access (screenlet-player -reset)", http.StatusConflict)
		return
	}

	var body struct {
		StudioURL string `json:"studioUrl"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // studioUrl is optional; a missing/empty body is fine

	result, err := s.mint(body.StudioURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.token = result.Token

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Token       string `json:"token"`
		DeviceID    string `json:"deviceId"`
		PairingCode string `json:"pairingCode"`
	}{Token: result.Token, DeviceID: result.DeviceID, PairingCode: result.PairingCode})

	if s.onClaimed != nil {
		go s.onClaimed(result.Token, body.StudioURL)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	status, err := s.player.Status()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// isAllowedSource restricts /play to http(s) URLs. Without this, a caller
// (anyone who knows the token) could point mpv at a local file:// path or
// another scheme it happens to support, on a box this API was only ever
// meant to drive toward playlist URLs Screenlet Studio itself serves.
func isAllowedSource(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !isAllowedSource(body.Source) {
		http.Error(w, "source must be an http:// or https:// URL", http.StatusBadRequest)
		return
	}
	if err := s.player.Play(body.Source); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.player.Stop(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
