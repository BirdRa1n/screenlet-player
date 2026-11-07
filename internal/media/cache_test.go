package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	wantA, _ := cache.pathFor(m.Items[0])
	wantB, _ := cache.pathFor(m.Items[1])
	if paths[0] != wantA || paths[1] != wantB {
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
	if entries, _ := os.ReadDir(cache.Dir()); len(entries) != 0 {
		t.Fatalf("a hash-mismatched download must not be committed to the cache, found %v", entries)
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
	if entries, _ := os.ReadDir(cache.Dir()); len(entries) != 0 {
		t.Fatalf("an oversized download must not be committed, found %v", entries)
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
	keepPath, _ := cache.pathFor(m.Items[0])
	if _, err := os.Stat(keepPath); err != nil {
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

func TestSyncResumesPartialDownload(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	data := bytes.Repeat([]byte("0123456789"), 1000)
	var gotRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		http.ServeContent(w, r, "v.mp4", time.Time{}, bytes.NewReader(data))
	}))
	defer srv.Close()

	cache, _ := NewCache()
	item := Item{Filename: "v.mp4", URL: srv.URL + "/exports/v.mp4", Size: int64(len(data)), Hash: sha256Hex(data)}
	dest, _ := cache.pathFor(item)
	// A previous attempt got 4000 bytes in before the connection dropped.
	if err := os.WriteFile(dest+".part", data[:4000], 0o600); err != nil {
		t.Fatal(err)
	}

	paths, err := cache.Sync(context.Background(), &Manifest{Items: []Item{item}})
	if err != nil || len(paths) != 1 {
		t.Fatalf("Sync: paths=%v err=%v", paths, err)
	}
	if gotRange != "bytes=4000-" {
		t.Fatalf("expected a resumed Range request, got %q", gotRange)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, data) {
		t.Fatal("resumed file content differs from the source")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part file should be gone after commit")
	}
}

func TestSyncKeepsCurrentContentWhenUpdateFails(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	old := []byte("old-video")
	srv := serveFiles(map[string][]byte{"old.mp4": old}) // "new.mp4" will 404
	defer srv.Close()

	cache, _ := NewCache()
	current := &Manifest{Version: "v1", Items: []Item{{Filename: "old.mp4", URL: srv.URL + "/exports/old.mp4", Size: int64(len(old)), Hash: sha256Hex(old)}}}
	if _, err := cache.Sync(context.Background(), current); err != nil {
		t.Fatalf("initial Sync: %v", err)
	}

	newBytes := []byte("new-video")
	next := &Manifest{Version: "v2", Items: []Item{{Filename: "new.mp4", URL: srv.URL + "/exports/new.mp4", Size: int64(len(newBytes)), Hash: sha256Hex(newBytes)}}}
	if _, err := cache.Sync(context.Background(), next, current); err == nil {
		t.Fatal("expected the failed download to be reported")
	}
	if cache.Complete(next) {
		t.Fatal("next manifest must not be reported complete")
	}
	if got := cache.LocalPlaylist(current); len(got) != 1 {
		t.Fatal("content on screen was deleted by a failed update")
	}
}

func TestInPlaceRerenderGetsItsOwnFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	v1 := []byte("render-1")
	v2 := []byte("render-2!")
	files := map[string][]byte{"ad.mp4": v1}
	srv := serveFiles(files)
	defer srv.Close()

	cache, _ := NewCache()
	m1 := &Manifest{Items: []Item{{Filename: "ad.mp4", URL: srv.URL + "/exports/ad.mp4", Size: int64(len(v1)), Hash: sha256Hex(v1)}}}
	p1, err := cache.Sync(context.Background(), m1)
	if err != nil {
		t.Fatalf("Sync v1: %v", err)
	}

	files["ad.mp4"] = v2 // Studio re-rendered the same filename
	m2 := &Manifest{Items: []Item{{Filename: "ad.mp4", URL: srv.URL + "/exports/ad.mp4", Size: int64(len(v2)), Hash: sha256Hex(v2)}}}
	p2, err := cache.Sync(context.Background(), m2, m1)
	if err != nil {
		t.Fatalf("Sync v2: %v", err)
	}
	if p1[0] == p2[0] {
		t.Fatal("a re-render must not overwrite the file being played")
	}
	if b, _ := os.ReadFile(p1[0]); !bytes.Equal(b, v1) {
		t.Fatal("old render must stay intact while it may still be on screen")
	}
	cache.GC(m2)
	if _, err := os.Stat(p1[0]); !os.IsNotExist(err) {
		t.Fatal("old render should be collected once it's off screen")
	}
}

func TestAdoptMigratesLegacyFilenameCache(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cache, _ := NewCache()
	data := []byte("legacy-bytes")
	if err := os.WriteFile(filepath.Join(cache.Dir(), "promo.mp4"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	stale := []byte("stale-bytes!")
	if err := os.WriteFile(filepath.Join(cache.Dir(), "wrong.mp4"), stale, 0o600); err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Items: []Item{
		{Filename: "promo.mp4", Size: int64(len(data)), Hash: sha256Hex(data)},
		// Old layout bug: manifest hash no longer matches the bytes on disk.
		{Filename: "wrong.mp4", Size: int64(len(stale)), Hash: sha256Hex([]byte("other-bytes!"))},
	}}
	cache.Adopt(m)
	got := cache.LocalPlaylist(m)
	if len(got) != 1 || !strings.HasPrefix(filepath.Base(got[0]), strings.TrimPrefix(m.Items[0].Hash, "sha256:")) {
		t.Fatalf("expected only the verified legacy file to be adopted, got %v", got)
	}
}

func TestLocalNameRejectsBadInput(t *testing.T) {
	good := sha256Hex([]byte("x"))
	if _, err := localName(Item{Filename: "../x.mp4", Hash: good}); err == nil {
		t.Error("traversal filename must be rejected")
	}
	if _, err := localName(Item{Filename: "x.mp4", Hash: "sha256:../../etc"}); err == nil {
		t.Error("non-hex hash must be rejected")
	}
	if n, err := localName(Item{Filename: "x.MP4", Hash: good}); err != nil || !strings.HasSuffix(n, ".mp4") {
		t.Errorf("localName = %q, %v", n, err)
	}
}
