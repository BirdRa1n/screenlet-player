// Package api exposes a small local HTTP API so Screenlet Studio (or an
// admin) can query and control this player without SSH — covering the
// same ground the Studio's Kodi JSON-RPC bridge does today (status,
// play, stop), but served natively by the player itself.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/BirdRa1n/screenlet-player/internal/playback"
)

// Server exposes a small local HTTP API bound to a playback backend.
type Server struct {
	player playback.Player
}

// New creates an API server bound to the given playback backend.
func New(player playback.Player) *Server {
	return &Server{player: player}
}

// Handler returns the http.Handler to mount.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/play", s.handlePlay)
	mux.HandleFunc("/stop", s.handleStop)
	return mux
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
