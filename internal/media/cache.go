package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BirdRa1n/screenlet-player/internal/storage"
)

// Cache stores channel assets under the player's config directory so they
// survive reboots. Reads are cheap (existence + size); the comparatively
// expensive content-hash check runs only when fetching a new asset, never on
// the boot/local path.
type Cache struct {
	dir  string
	http *http.Client
}

// mediaDir returns the cache directory path (without creating it).
func mediaDir() (string, error) {
	base, err := storage.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "media"), nil
}

// NewCache prepares (and creates) the on-disk media directory.
func NewCache() (*Cache, error) {
	dir, err := mediaDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// Generous timeout: signage videos can be large and links slow, but a
	// stuck connection should still eventually fail rather than hang forever.
	return &Cache{dir: dir, http: &http.Client{Timeout: 10 * time.Minute}}, nil
}

// Dir returns the media cache directory.
func (c *Cache) Dir() string { return c.dir }

// safeName rejects anything that isn't a plain filename, closing path-traversal
// vectors from a malicious or buggy server (e.g. "../../.ssh/authorized_keys").
func safeName(name string) (string, error) {
	if name == "" || name == "." || name == ".." ||
		name != filepath.Base(name) ||
		strings.ContainsRune(name, '/') || strings.ContainsRune(name, os.PathSeparator) {
		return "", fmt.Errorf("media: unsafe filename %q", name)
	}
	return name, nil
}

func (c *Cache) pathFor(name string) (string, error) {
	safe, err := safeName(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(c.dir, safe), nil
}

// fileMatches reports whether the item is present on disk at its expected size.
// Cheap by design: cached files are only ever created by a hash-verified,
// atomic download, so a present file of the right size is known-good — there is
// no need to re-hash it on every boot.
func (c *Cache) fileMatches(item Item) bool {
	path, err := c.pathFor(item.Filename)
	if err != nil {
		return false
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return false
	}
	return item.Size <= 0 || st.Size() == item.Size
}

// LocalPlaylist returns, in manifest order, the cache paths of every item that
// is present at the expected size. Purely local — no network — so it is safe
// to call at boot to start playback before any server contact.
func (c *Cache) LocalPlaylist(m *Manifest) []string {
	if m == nil {
		return nil
	}
	var paths []string
	for _, it := range m.Items {
		if !c.fileMatches(it) {
			continue
		}
		if p, err := c.pathFor(it.Filename); err == nil {
			paths = append(paths, p)
		}
	}
	return paths
}

// Sync downloads any missing or changed items, removes assets the manifest no
// longer references, and returns the ordered local paths ready for playback. A
// per-item download failure is logged and collected but does not abort the
// whole sync: whatever is already cached still plays. Items are considered
// already-cached when the previously persisted manifest recorded the same hash
// and the file is present at the right size — so no on-device hashing is needed
// to decide what to fetch.
func (c *Cache) Sync(ctx context.Context, m *Manifest) ([]string, error) {
	if m == nil {
		return nil, errors.New("media: nil manifest")
	}

	prevHash := map[string]string{}
	if old, ok, err := LoadManifest(); err == nil && ok {
		for _, it := range old.Items {
			prevHash[it.Filename] = it.Hash
		}
	}

	var firstErr error
	for _, it := range m.Items {
		if prevHash[it.Filename] == it.Hash && c.fileMatches(it) {
			continue // unchanged since last sync and still on disk
		}
		if err := c.download(ctx, it); err != nil {
			log.Printf("media: failed to cache %s: %v", it.Filename, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	c.gc(m)
	return c.LocalPlaylist(m), firstErr
}

// download fetches one item to a temporary file, verifies its size and content
// hash, then atomically renames it into place. A failed or corrupt download
// never replaces an existing good file.
func (c *Cache) download(ctx context.Context, item Item) error {
	dest, err := c.pathFor(item.Filename)
	if err != nil {
		return err
	}
	safe := filepath.Base(dest)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("media: %s returned HTTP %d", item.URL, resp.StatusCode)
	}

	tmp, err := os.CreateTemp(c.dir, safe+".*.part")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		tmp.Close()
		if !committed {
			os.Remove(tmpName)
		}
	}()

	// Cap the read at the advertised size (+1 to detect an overrun) so a server
	// cannot fill the disk by streaming far more than it declared.
	var src io.Reader = resp.Body
	if item.Size > 0 {
		src = io.LimitReader(resp.Body, item.Size+1)
	}
	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hasher), src)
	if err != nil {
		return err
	}
	if item.Size > 0 && n != item.Size {
		return fmt.Errorf("media: %s size mismatch: got %d want %d", item.Filename, n, item.Size)
	}
	if err := verifyDigest(hasher.Sum(nil), item.Hash); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmpName, 0o600)
	if err := os.Rename(tmpName, dest); err != nil {
		return err
	}
	committed = true
	syncDir(c.dir) // make the rename itself durable
	return nil
}

// gc removes cached files (and any orphaned temp files) the current manifest no
// longer references, keeping a device's SD card from filling up over its life.
func (c *Cache) gc(m *Manifest) {
	keep := make(map[string]bool, len(m.Items))
	for _, it := range m.Items {
		if safe, err := safeName(it.Filename); err == nil {
			keep[safe] = true
		}
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if keep[e.Name()] {
			continue
		}
		os.Remove(filepath.Join(c.dir, e.Name()))
	}
}

func verifyDigest(sum []byte, expected string) error {
	exp := strings.TrimPrefix(expected, "sha256:")
	if exp == "" {
		return errors.New("media: manifest item is missing a content hash")
	}
	got := hex.EncodeToString(sum)
	if !strings.EqualFold(got, exp) {
		return fmt.Errorf("media: hash mismatch (got %s want %s)", got, exp)
	}
	return nil
}

// atomicWrite writes data to a temp file in the same directory, fsyncs it, then
// renames it over the destination — so readers only ever see a complete file.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		tmp.Close()
		if !committed {
			os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	committed = true
	syncDir(dir)
	return nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
