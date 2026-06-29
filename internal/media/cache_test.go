package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// serveFiles returns an httptest server serving the given files at /exports/<name>.
func serveFiles(files map[string][]byte) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/exports/", func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		body, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	})
	return httptest.NewServer(mux)
}

func TestSyncDownloadsVerifiesAndPlays(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	a := []byte("video-a-bytes")
	b := []byte("video-b-bytes")
	srv := serveFiles(map[string][]byte{"a.mp4": a, "b.mp4": b})
	defer srv.Close()

	cache, err := NewCache()
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	m := &Manifest{
		ChannelID: "ch1",
		Version:   "v1",
		Loop:      true,
		Items: []Item{
			{Filename: "a.mp4", URL: srv.URL + "/exports/a.mp4", Size: int64(len(a)), Hash: sha256Hex(a)},
			{Filename: "b.mp4", URL: srv.URL + "/exports/b.mp4", Size: int64(len(b)), Hash: sha256Hex(b)},
		},
	}

	paths, err := cache.Sync(context.Background(), m)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 playable paths, got %d", len(paths))
	}
	// Order must follow the manifest.
	if filepath.Base(paths[0]) != "a.mp4" || filepath.Base(paths[1]) != "b.mp4" {
		t.Fatalf("playlist order wrong: %v", paths)
	}

	// LocalPlaylist must return the same files offline, without the server.
	if got := cache.LocalPlaylist(m); len(got) != 2 {
		t.Fatalf("LocalPlaylist offline returned %d items, want 2", len(got))
	}
}

func TestSyncRejectsHashMismatch(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	data := []byte("real-bytes")
	srv := serveFiles(map[string][]byte{"x.mp4": data})
	defer srv.Close()

	cache, _ := NewCache()
	m := &Manifest{
		ChannelID: "ch1",
		Items: []Item{
			// Declared hash is for different content — download must be rejected.
			{Filename: "x.mp4", URL: srv.URL + "/exports/x.mp4", Size: int64(len(data)), Hash: sha256Hex([]byte("other"))},
		},
	}

	paths, err := cache.Sync(context.Background(), m)
	if err == nil {
		t.Fatal("expected an error for hash mismatch, got nil")
	}
	if len(paths) != 0 {
		t.Fatalf("corrupt item must not be playable, got %v", paths)
	}
	if _, statErr := os.Stat(filepath.Join(cache.Dir(), "x.mp4")); !os.IsNotExist(statErr) {
		t.Fatal("a hash-mismatched download must not be committed to the cache")
	}
}

func TestSyncRejectsOversizedBody(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	actual := []byte("this is much larger than declared")
	srv := serveFiles(map[string][]byte{"big.mp4": actual})
	defer srv.Close()

	cache, _ := NewCache()
	m := &Manifest{
		Items: []Item{
			// Declared size far smaller than what the server actually sends.
			{Filename: "big.mp4", URL: srv.URL + "/exports/big.mp4", Size: 4, Hash: sha256Hex(actual)},
		},
	}

	if _, err := cache.Sync(context.Background(), m); err == nil {
		t.Fatal("expected a size-mismatch error, got nil")
	}
	if _, statErr := os.Stat(filepath.Join(cache.Dir(), "big.mp4")); !os.IsNotExist(statErr) {
		t.Fatal("an oversized download must not be committed")
	}
}

func TestSafeNameRejectsTraversal(t *testing.T) {
	for _, bad := range []string{"../escape", "a/b", "..", ".", "", "/etc/passwd", "sub/../../x"} {
		if _, err := safeName(bad); err == nil {
			t.Errorf("safeName(%q) should have been rejected", bad)
		}
	}
	for _, good := range []string{"a.mp4", "projeto.mp4", "video-1.webm"} {
		if _, err := safeName(good); err != nil {
			t.Errorf("safeName(%q) should be allowed: %v", good, err)
		}
	}
}

func TestGCRemovesUnreferencedFiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	keep := []byte("keep")
	srv := serveFiles(map[string][]byte{"keep.mp4": keep})
	defer srv.Close()

	cache, _ := NewCache()

	// A stale file from a previous channel that the new manifest won't list.
	stale := filepath.Join(cache.Dir(), "stale.mp4")
	if err := os.WriteFile(stale, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := &Manifest{
		Items: []Item{{Filename: "keep.mp4", URL: srv.URL + "/exports/keep.mp4", Size: int64(len(keep)), Hash: sha256Hex(keep)}},
	}
	if _, err := cache.Sync(context.Background(), m); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("gc should have removed the unreferenced file")
	}
	if _, err := os.Stat(filepath.Join(cache.Dir(), "keep.mp4")); err != nil {
		t.Fatalf("referenced file should remain: %v", err)
	}
}

func TestSyncSkipsUnchangedSecondTime(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	data := []byte("stable")
	var hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/exports/", func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Write(data)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cache, _ := NewCache()
	m := &Manifest{
		Items: []Item{{Filename: "s.mp4", URL: srv.URL + "/exports/s.mp4", Size: int64(len(data)), Hash: sha256Hex(data)}},
	}

	if _, err := cache.Sync(context.Background(), m); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if err := SaveManifest(m); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	// Second sync with the same hash recorded should not re-download.
	if _, err := cache.Sync(context.Background(), m); err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if hits != 1 {
		t.Fatalf("expected exactly 1 download, got %d", hits)
	}
}

func TestManifestRoundTripAndClear(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	m := &Manifest{ChannelID: "ch9", Version: "abc", Loop: true, Items: []Item{{Filename: "a.mp4"}}}
	if err := SaveManifest(m); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	got, ok, err := LoadManifest()
	if err != nil || !ok {
		t.Fatalf("LoadManifest: ok=%v err=%v", ok, err)
	}
	if got.ChannelID != "ch9" || got.Version != "abc" || !got.Loop || len(got.Items) != 1 {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	cache, _ := NewCache()
	os.WriteFile(filepath.Join(cache.Dir(), "a.mp4"), []byte("x"), 0o600)
	if err := Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, ok, _ := LoadManifest(); ok {
		t.Fatal("manifest should be gone after Clear")
	}
	if _, err := os.Stat(cache.Dir()); !os.IsNotExist(err) {
		t.Fatal("media dir should be gone after Clear")
	}
}
