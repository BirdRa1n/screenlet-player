package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BirdRa1n/screenlet-player/internal/storage"
)

// Cache stores channel assets under the player's config directory so they
// survive reboots.
//
// Files are content-addressed: each one is stored as "<sha256>.<ext>", never
// under the server's filename. That makes a cached file immutable — an
// in-place re-render in Studio (same filename, new bytes) gets a new hash and
// therefore a new file, so the copy mpv is playing right now is never
// overwritten or deleted out from under it, and "is this item cached?" is
// answered by a stat, with no bookkeeping that could drift from what is
// actually on disk.
type Cache struct {
	dir   string
	http  *http.Client
	stall time.Duration
}

// stallTimeout aborts a download that has received no bytes for this long. A
// total-time limit would instead kill a healthy-but-slow download of a large
// video over Wi-Fi; a stalled one resumes from its .part file next time.
const stallTimeout = 60 * time.Second

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
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		IdleConnTimeout:       60 * time.Second,
	}
	return &Cache{dir: dir, http: &http.Client{Transport: transport}, stall: stallTimeout}, nil
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

var (
	hexHash = regexp.MustCompile(`^[0-9a-f]{64}$`)
	safeExt = regexp.MustCompile(`^\.[a-z0-9]{1,8}$`)
)

// localName is the content-addressed file name for an item: its sha256 plus
// the original extension (kept so mpv can sniff the container cheaply).
func localName(item Item) (string, error) {
	if _, err := safeName(item.Filename); err != nil {
		return "", err
	}
	sum := strings.ToLower(strings.TrimPrefix(item.Hash, "sha256:"))
	if !hexHash.MatchString(sum) {
		return "", fmt.Errorf("media: %s has no valid sha256 hash", item.Filename)
	}
	ext := strings.ToLower(filepath.Ext(item.Filename))
	if !safeExt.MatchString(ext) {
		ext = ""
	}
	return sum + ext, nil
}

func (c *Cache) pathFor(item Item) (string, error) {
	name, err := localName(item)
	if err != nil {
		return "", err
	}
	return filepath.Join(c.dir, name), nil
}

// fileMatches reports whether the item is present on disk at its expected size.
// Cheap by design: cached files are only ever created by a hash-verified,
// atomic download and named after that hash, so a present file of the right
// size is known-good — there is no need to re-hash it on every boot.
func (c *Cache) fileMatches(item Item) bool {
	path, err := c.pathFor(item)
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
		if p, err := c.pathFor(it); err == nil {
			paths = append(paths, p)
		}
	}
	return paths
}

// Complete reports whether every item of the manifest is cached locally.
func (c *Cache) Complete(m *Manifest) bool {
	return m != nil && len(m.Items) > 0 && len(c.LocalPlaylist(m)) == len(m.Items)
}

// Adopt migrates files cached by older player versions, which stored them
// under the server's filename, to their content-addressed name. Each one is
// re-hashed first (once, ever): the old layout could end up with a manifest
// hash that no longer matched the bytes on disk.
func (c *Cache) Adopt(m *Manifest) {
	if m == nil {
		return
	}
	for _, it := range m.Items {
		if c.fileMatches(it) {
			continue
		}
		name, err := safeName(it.Filename)
		if err != nil {
			continue
		}
		legacy := filepath.Join(c.dir, name)
		st, err := os.Stat(legacy)
		if err != nil || st.IsDir() || (it.Size > 0 && st.Size() != it.Size) {
			continue
		}
		dest, err := c.pathFor(it)
		if err != nil || dest == legacy {
			continue
		}
		if err := hashFile(legacy, it.Hash); err != nil {
			log.Printf("media: not adopting legacy %s: %v", name, err)
			continue
		}
		if err := os.Rename(legacy, dest); err == nil {
			log.Printf("media: migrated cached %s to content-addressed storage", name)
		}
	}
	syncDir(c.dir)
}

// Sync downloads any missing items of m and returns the ordered local paths of
// the ones now available. A per-item failure is logged and returned (the first
// one) but does not abort the rest. Files referenced by m or by any manifest in
// keep — typically the one currently on screen — are never removed, so a
// failed or partial update can never take away content that is playing.
func (c *Cache) Sync(ctx context.Context, m *Manifest, keep ...*Manifest) ([]string, error) {
	if m == nil {
		return nil, errors.New("media: nil manifest")
	}

	var firstErr error
	for _, it := range m.Items {
		if c.fileMatches(it) {
			continue
		}
		if err := c.download(ctx, it); err != nil {
			log.Printf("media: failed to cache %s: %v", it.Filename, err)
			if firstErr == nil {
				firstErr = err
			}
		}
		if ctx.Err() != nil {
			break
		}
	}

	c.GC(append([]*Manifest{m}, keep...)...)
	return c.LocalPlaylist(m), firstErr
}

// download fetches one item into "<name>.part", resuming a previous partial
// download with a Range request when possible, verifies its size and content
// hash, then atomically renames it into place. A failed download keeps the
// .part file for the next attempt; a corrupt one is discarded.
func (c *Cache) download(ctx context.Context, item Item) error {
	dest, err := c.pathFor(item)
	if err != nil {
		return err
	}
	part := dest + ".part"

	var offset int64
	if item.Size > 0 {
		if st, err := os.Stat(part); err == nil && !st.IsDir() {
			offset = st.Size()
			if offset > item.Size {
				os.Remove(part)
				offset = 0
			}
		}
	}
	if item.Size > 0 && offset == item.Size {
		// Fully downloaded last time but not committed (crash/power cut).
		return c.commit(part, dest, item, nil)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stalled atomic.Bool
	watchdog := time.AfterFunc(c.stall, func() { stalled.Store(true); cancel() })
	defer watchdog.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusPartialContent && offset > 0:
		if !strings.HasPrefix(resp.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-", offset)) {
			os.Remove(part)
			return fmt.Errorf("media: %s unexpected Content-Range %q", item.Filename, resp.Header.Get("Content-Range"))
		}
	case resp.StatusCode == http.StatusOK:
		offset = 0 // server ignored the Range: start over
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		os.Remove(part)
		return fmt.Errorf("media: %s range not satisfiable, restarting next time", item.Filename)
	default:
		return fmt.Errorf("media: %s returned HTTP %d", item.URL, resp.StatusCode)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	f, err := os.OpenFile(part, flags, 0o600)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	if offset > 0 {
		if err := hashPrefix(part, offset, hasher); err != nil {
			f.Close()
			os.Remove(part)
			return err
		}
	}

	// Cap the read at the advertised size (+1 to detect an overrun) so a server
	// cannot fill the disk by streaming far more than it declared.
	var src io.Reader = resp.Body
	if item.Size > 0 {
		src = io.LimitReader(resp.Body, item.Size-offset+1)
	}
	src = &activityReader{r: src, onRead: func() { watchdog.Reset(c.stall) }}
	n, copyErr := io.Copy(io.MultiWriter(f, hasher), src)
	syncErr := f.Sync()
	closeErr := f.Close()
	if copyErr != nil {
		if stalled.Load() {
			return fmt.Errorf("media: %s stalled after %d bytes, will resume", item.Filename, offset+n)
		}
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	total := offset + n
	if item.Size > 0 && total < item.Size {
		return fmt.Errorf("media: %s incomplete (%d of %d bytes), will resume", item.Filename, total, item.Size)
	}
	if item.Size > 0 && total > item.Size {
		os.Remove(part)
		return fmt.Errorf("media: %s size mismatch: got more than %d bytes", item.Filename, item.Size)
	}
	return c.commit(part, dest, item, hasher.Sum(nil))
}

// commit verifies a finished .part file (hashing it if sum is nil) and renames
// it into place durably.
func (c *Cache) commit(part, dest string, item Item, sum []byte) error {
	var err error
	if sum == nil {
		err = hashFile(part, item.Hash)
	} else {
		err = verifyDigest(sum, item.Hash)
	}
	if err != nil {
		os.Remove(part)
		return err
	}
	_ = os.Chmod(part, 0o600)
	if err := os.Rename(part, dest); err != nil {
		return err
	}
	syncDir(c.dir) // make the rename itself durable
	return nil
}

// GC removes cached files (and stale partial downloads) that none of the given
// manifests reference, keeping a device's SD card from filling up over its
// life. Partial downloads of referenced items are kept so they can resume.
func (c *Cache) GC(manifests ...*Manifest) {
	keep := map[string]bool{}
	for _, m := range manifests {
		if m == nil {
			continue
		}
		for _, it := range m.Items {
			if name, err := localName(it); err == nil {
				keep[name] = true
				keep[name+".part"] = true
			}
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
		os.RemoveAll(filepath.Join(c.dir, e.Name()))
	}
}

// activityReader calls onRead every time bytes arrive (feeds the stall watchdog).
type activityReader struct {
	r      io.Reader
	onRead func()
}

func (a *activityReader) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if n > 0 {
		a.onRead()
	}
	return n, err
}

func hashPrefix(path string, n int64, h io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	got, err := io.Copy(h, io.LimitReader(f, n))
	if err != nil {
		return err
	}
	if got != n {
		return fmt.Errorf("media: partial file shorter than expected")
	}
	return nil
}

func hashFile(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	return verifyDigest(h.Sum(nil), expected)
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
