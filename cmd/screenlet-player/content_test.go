package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/BirdRa1n/screenlet-player/internal/media"
	"github.com/BirdRa1n/screenlet-player/internal/playback"
	"github.com/BirdRa1n/screenlet-player/internal/sync"
)

// fakePlayer records what would be on screen.
type fakePlayer struct {
	playback.NoopPlayer
	playlist []string
	live     string
	message  string
}

func (f *fakePlayer) Play(src string) error { f.live, f.playlist = src, nil; return nil }
func (f *fakePlayer) PlayPlaylist(p []string, _ bool) error {
	f.playlist, f.live = append([]string(nil), p...), ""
	return nil
}
func (f *fakePlayer) ShowMessage(t string) error { f.message = t; return nil }

func hashOf(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

type studio struct {
	files map[string][]byte
	srv   *httptest.Server
}

func newStudio() *studio {
	s := &studio{files: map[string][]byte{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := s.files[filepath.Base(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	return s
}

func (s *studio) assignment(version string, names ...string) sync.ChannelAssignment {
	a := sync.ChannelAssignment{ChannelID: "ch", Version: version, Loop: true, PlaylistURL: s.srv.URL + "/channel/ch"}
	for _, n := range names {
		b := s.files[n]
		if b == nil {
			b = []byte("missing-" + n)
		}
		a.Items = append(a.Items, media.Item{Filename: n, URL: s.srv.URL + "/exports/" + n, Size: int64(len(b)), Hash: hashOf(b)})
	}
	return a
}

func setup(t *testing.T) (*content, *fakePlayer, *studio) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cache, err := media.NewCache()
	if err != nil {
		t.Fatal(err)
	}
	p := &fakePlayer{}
	st := newStudio()
	t.Cleanup(st.srv.Close)
	return newContent(p, cache, "ABCDE"), p, st
}

func TestIdleScreenInsteadOfConsole(t *testing.T) {
	c, p, _ := setup(t)
	c.boot()
	if p.message == "" {
		t.Fatal("with nothing cached the screen must show a status message")
	}
}

func TestFailedUpdateKeepsPlayingCachedContent(t *testing.T) {
	c, p, st := setup(t)
	st.files["a.mp4"] = []byte("aaaa")
	if !c.apply(context.Background(), st.assignment("v1", "a.mp4")) {
		t.Fatal("v1 should apply fully")
	}
	if len(p.playlist) != 1 || p.message != "" {
		t.Fatalf("expected a.mp4 on screen with no message, got %v / %q", p.playlist, p.message)
	}
	onScreen := p.playlist[0]

	// v2 references a file the PC can't deliver (PC went away mid-update).
	if c.apply(context.Background(), st.assignment("v2", "b.mp4")) {
		t.Fatal("incomplete update must be retried, not reported applied")
	}
	if p.live != "" {
		t.Fatal("must never fall back to the PC's live stream while content is cached")
	}
	if len(p.playlist) != 1 || p.playlist[0] != onScreen {
		t.Fatalf("current content must stay on screen, got %v", p.playlist)
	}

	// The file shows up later: the retry swaps to v2.
	st.files["b.mp4"] = []byte("missing-b.mp4")
	if !c.apply(context.Background(), st.assignment("v2", "b.mp4")) {
		t.Fatal("v2 should apply once its file is available")
	}
	if len(p.playlist) != 1 || p.playlist[0] == onScreen {
		t.Fatalf("expected v2 on screen, got %v", p.playlist)
	}
}

func TestRebootOfflinePlaysCache(t *testing.T) {
	c, _, st := setup(t)
	st.files["a.mp4"] = []byte("aaaa")
	st.files["b.mp4"] = []byte("bbbb")
	c.apply(context.Background(), st.assignment("v1", "a.mp4", "b.mp4"))
	st.srv.Close() // PC off

	cache, _ := media.NewCache()
	p2 := &fakePlayer{}
	c2 := newContent(p2, cache, "")
	c2.boot()
	if len(p2.playlist) != 2 || p2.live != "" {
		t.Fatalf("reboot without PC should play both cached files, got %v live=%q", p2.playlist, p2.live)
	}
}

func TestLiveOnlyWhenNothingCached(t *testing.T) {
	c, p, st := setup(t)
	c.apply(context.Background(), st.assignment("v1", "x.mp4")) // nothing downloadable
	if p.live == "" {
		t.Fatal("with nothing cached, the live stream is the only option")
	}
	st.files["x.mp4"] = []byte("missing-x.mp4")
	if !c.apply(context.Background(), st.assignment("v1", "x.mp4")) {
		t.Fatal("should apply once downloadable")
	}
	if p.live != "" || len(p.playlist) != 1 {
		t.Fatal("must switch from live to the local cache as soon as it is available")
	}
}

func TestPartialFirstSyncPlaysWhatArrived(t *testing.T) {
	c, p, st := setup(t)
	st.files["a.mp4"] = []byte("aaaa")
	if c.apply(context.Background(), st.assignment("v1", "a.mp4", "b.mp4")) {
		t.Fatal("partial sync must be retried")
	}
	if len(p.playlist) != 1 {
		t.Fatalf("first sync should show the file that did arrive, got %v", p.playlist)
	}
	st.files["b.mp4"] = []byte("missing-b.mp4")
	if !c.apply(context.Background(), st.assignment("v1", "a.mp4", "b.mp4")) {
		t.Fatal("complete now")
	}
	if len(p.playlist) != 2 {
		t.Fatalf("full playlist expected after retry, got %v", p.playlist)
	}
}
