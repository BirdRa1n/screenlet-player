package playback

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MPVOptions configures how the mpv subprocess is launched.
type MPVOptions struct {
	// BinPath is the mpv executable to run. Defaults to "mpv" (resolved via PATH).
	BinPath string
	// ExtraArgs are appended to the invocation as-is, e.g. a Raspberry
	// Pi's video-output backend ("--vo=drm") — see docs/ROADMAP.md.
	ExtraArgs []string
}

// MPVPlayer controls a single long-running mpv process over its JSON IPC
// socket. mpv is launched idle once and reused across Play calls — the
// player only ever sends it loadfile/stop commands, so switching channels
// never requires a restart (unlike the Kodi+SSH bridge it replaces). If
// the mpv process dies — crash, OOM, a bad stream, anything — it is
// automatically relaunched and the last-requested source resumed; an
// unattended signage device has no one to notice a permanently dead
// player otherwise.
type MPVPlayer struct {
	opts MPVOptions

	mu        sync.Mutex
	cmd       *exec.Cmd
	socketDir string // removed wholesale on Close or before a respawn
	stderr    *cappedBuffer
	conn      net.Conn
	pending   map[int64]chan mpvResponse
	status    Status
	closed    bool // true once Close() runs, so a disconnect doesn't trigger a respawn

	// Last requested content, replayed verbatim after an automatic respawn so
	// recovery doesn't depend on the next channel *change* (which may never
	// come on an unattended device). Exactly one of these is set at a time.
	lastSource   string
	lastPlaylist []string
	lastLoop     bool

	nextID int64
}

type mpvCommand struct {
	Command   []any `json:"command"`
	RequestID int64 `json:"request_id"`
}

type mpvResponse struct {
	Error     string          `json:"error"`
	Data      json.RawMessage `json:"data"`
	RequestID int64           `json:"request_id"`
}

const mpvCallTimeout = 5 * time.Second

// mpvProcess is everything launchMPV starts, handed back so the caller
// (NewMPVPlayer the first time, respawn every time after) can wire it
// into an MPVPlayer or tear it down on failure.
type mpvProcess struct {
	cmd       *exec.Cmd
	socketDir string
	stderr    *cappedBuffer
	conn      net.Conn
}

// NewMPVPlayer launches mpv idle and connects to its JSON IPC socket. The
// returned Player owns the mpv process: Close stops both. An error here
// almost always means mpv isn't installed or has no usable video output —
// callers on a dev machine should fall back to NewNoopPlayer rather than
// treat it as fatal.
func NewMPVPlayer(opts MPVOptions) (*MPVPlayer, error) {
	proc, err := launchMPV(opts)
	if err != nil {
		return nil, err
	}

	p := &MPVPlayer{
		opts:      opts,
		cmd:       proc.cmd,
		socketDir: proc.socketDir,
		stderr:    proc.stderr,
		conn:      proc.conn,
		pending:   make(map[int64]chan mpvResponse),
	}
	go p.readLoop(proc.conn)
	return p, nil
}

// launchMPV starts a fresh mpv process and connects to its IPC socket.
// Used both by NewMPVPlayer and by respawn after the previous process died.
func launchMPV(opts MPVOptions) (*mpvProcess, error) {
	bin := opts.BinPath
	if bin == "" {
		bin = "mpv"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return nil, fmt.Errorf("mpv: %q not found in PATH: %w", bin, err)
	}

	// A fresh, exclusively-owned 0700 directory (not a predictable path
	// directly under the shared, world-writable os.TempDir()) closes the
	// TOCTOU window a local attacker would otherwise have between removing
	// any stale socket and mpv creating the real one.
	sockDir, err := os.MkdirTemp("", "screenlet-player-mpv-*")
	if err != nil {
		return nil, fmt.Errorf("mpv: failed to create socket directory: %w", err)
	}
	sockPath := filepath.Join(sockDir, "mpv.sock")

	args := append([]string{
		"--idle=yes",
		"--fullscreen",
		"--no-terminal",
		"--really-quiet",
		"--input-ipc-server=" + sockPath,
	}, opts.ExtraArgs...)

	cmd := exec.Command(bin, args...)
	stderr := &cappedBuffer{max: 4096}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		os.RemoveAll(sockDir)
		return nil, fmt.Errorf("mpv: failed to start: %w", err)
	}

	conn, err := dialMPVSocket(sockPath, mpvCallTimeout)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		os.RemoveAll(sockDir)
		if log := stderr.String(); log != "" {
			return nil, fmt.Errorf("mpv: failed to connect to IPC socket: %w (stderr: %s)", err, log)
		}
		return nil, fmt.Errorf("mpv: failed to connect to IPC socket: %w", err)
	}

	return &mpvProcess{cmd: cmd, socketDir: sockDir, stderr: stderr, conn: conn}, nil
}

// dialMPVSocket retries connecting since mpv creates the socket file
// asynchronously shortly after the process starts.
func dialMPVSocket(path string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", path)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	return nil, lastErr
}

// readLoop owns the connection's read side for the player's lifetime: mpv
// IPC interleaves unsolicited event lines with command responses on the
// same stream, matched only by the response's request_id. When the
// connection drops — mpv crashed, was killed, anything — it triggers a
// respawn rather than just leaving the player permanently broken.
func (p *MPVPlayer) readLoop(conn net.Conn) {
	decoder := json.NewDecoder(conn)
	for {
		var resp mpvResponse
		if err := decoder.Decode(&resp); err != nil {
			p.handleDisconnect()
			return
		}
		if resp.RequestID == 0 {
			continue // unsolicited event line, not a command response
		}

		p.mu.Lock()
		ch, ok := p.pending[resp.RequestID]
		if ok {
			delete(p.pending, resp.RequestID)
		}
		p.mu.Unlock()
		if ok {
			ch <- resp
			close(ch)
		}
	}
}

// handleDisconnect cleans up after a lost connection and, unless this was
// triggered by an intentional Close(), starts trying to bring mpv back.
func (p *MPVPlayer) handleDisconnect() {
	p.mu.Lock()
	for id, ch := range p.pending {
		close(ch)
		delete(p.pending, id)
	}
	p.conn = nil
	closed := p.closed
	resume := resumeState{source: p.lastSource, playlist: append([]string(nil), p.lastPlaylist...), loop: p.lastLoop}
	p.mu.Unlock()

	if closed {
		return
	}

	log.Printf("mpv: connection lost, respawning")
	go p.respawn(resume)
}

// resumeState captures what to replay after a respawn.
type resumeState struct {
	source   string
	playlist []string
	loop     bool
}

// respawn keeps relaunching mpv with exponential backoff (capped at 30s)
// until it succeeds or Close() is called. A signage device left
// unattended has no one to notice — and restart — a permanently dead
// player otherwise. On success, the last-requested source (if any) is
// resumed automatically; without this, recovery would depend on the
// next channel *change*, which may never come.
func (p *MPVPlayer) respawn(resume resumeState) {
	for attempt := 0; ; attempt++ {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()

		proc, err := launchMPV(p.opts)
		if err != nil {
			delay := respawnBackoff(attempt)
			log.Printf("mpv: respawn attempt %d failed (%v), retrying in %s", attempt+1, err, delay)
			time.Sleep(delay)
			continue
		}

		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			_ = proc.cmd.Process.Kill()
			_ = proc.cmd.Wait()
			os.RemoveAll(proc.socketDir)
			return
		}
		os.RemoveAll(p.socketDir) // the dead process's now-orphaned socket dir
		p.cmd = proc.cmd
		p.socketDir = proc.socketDir
		p.stderr = proc.stderr
		p.conn = proc.conn
		p.pending = make(map[int64]chan mpvResponse)
		p.mu.Unlock()

		go p.readLoop(proc.conn)
		log.Printf("mpv: respawned successfully after %d attempt(s)", attempt+1)

		switch {
		case len(resume.playlist) > 0:
			if err := p.PlayPlaylist(resume.playlist, resume.loop); err != nil {
				log.Printf("mpv: failed to resume playlist after respawn: %v", err)
			}
		case resume.source != "":
			if err := p.Play(resume.source); err != nil {
				log.Printf("mpv: failed to resume playback after respawn: %v", err)
			}
		}
		return
	}
}

func respawnBackoff(attempt int) time.Duration {
	d := time.Second << attempt // 1s, 2s, 4s, 8s, 16s, 32s...
	if d > 30*time.Second || d <= 0 {
		d = 30 * time.Second
	}
	return d
}

func (p *MPVPlayer) call(args ...any) (json.RawMessage, error) {
	id := atomic.AddInt64(&p.nextID, 1)
	ch := make(chan mpvResponse, 1)

	p.mu.Lock()
	conn := p.conn
	if conn == nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("mpv: not connected")
	}
	p.pending[id] = ch
	p.mu.Unlock()

	payload, err := json.Marshal(mpvCommand{Command: args, RequestID: id})
	if err != nil {
		return nil, err
	}
	payload = append(payload, '\n')

	if _, err := conn.Write(payload); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, fmt.Errorf("mpv: write failed: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("mpv: connection closed before response")
		}
		if resp.Error != "" && resp.Error != "success" {
			return nil, fmt.Errorf("mpv: %s", resp.Error)
		}
		return resp.Data, nil
	case <-time.After(mpvCallTimeout):
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, fmt.Errorf("mpv: command timed out")
	}
}

func (p *MPVPlayer) Play(source string) error {
	if _, err := p.call("loadfile", source, "replace"); err != nil {
		return err
	}
	p.mu.Lock()
	p.status = Status{Playing: true, Source: source}
	p.lastSource = source
	p.lastPlaylist = nil
	p.mu.Unlock()
	return nil
}

// PlayPlaylist writes the ordered paths to an m3u inside mpv's private socket
// directory and tells mpv to load it, optionally looping the whole list. The
// playlist is remembered so a respawn can reload it.
func (p *MPVPlayer) PlayPlaylist(paths []string, loop bool) error {
	if len(paths) == 0 {
		return fmt.Errorf("mpv: empty playlist")
	}

	p.mu.Lock()
	socketDir := p.socketDir
	p.mu.Unlock()

	listPath, err := writeM3U(socketDir, paths)
	if err != nil {
		return err
	}

	if _, err := p.call("loadlist", listPath, "replace"); err != nil {
		return err
	}
	loopVal := "no"
	if loop {
		loopVal = "inf"
	}
	if _, err := p.call("set_property", "loop-playlist", loopVal); err != nil {
		// Non-fatal: the content still plays, it just won't loop on its own.
		log.Printf("mpv: failed to set loop-playlist=%s: %v", loopVal, err)
	}

	p.mu.Lock()
	p.status = Status{Playing: true, Source: paths[0]}
	p.lastSource = ""
	p.lastPlaylist = append([]string(nil), paths...)
	p.lastLoop = loop
	p.mu.Unlock()
	return nil
}

// writeM3U writes an absolute-path playlist mpv can load. Lives in mpv's
// 0700 socket directory, recreated on every (re)spawn, so it never collides
// with another process or outlives the mpv instance that uses it.
func writeM3U(dir string, paths []string) (string, error) {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for _, p := range paths {
		b.WriteString(p)
		b.WriteByte('\n')
	}
	listPath := filepath.Join(dir, "playlist.m3u")
	if err := os.WriteFile(listPath, []byte(b.String()), 0o600); err != nil {
		return "", fmt.Errorf("mpv: failed to write playlist: %w", err)
	}
	return listPath, nil
}

func (p *MPVPlayer) Stop() error {
	if _, err := p.call("stop"); err != nil {
		return err
	}
	p.mu.Lock()
	p.status = Status{}
	p.lastSource = ""
	p.lastPlaylist = nil
	p.mu.Unlock()
	return nil
}

func (p *MPVPlayer) Status() (Status, error) {
	p.mu.Lock()
	status := p.status
	p.mu.Unlock()

	if !status.Playing {
		return status, nil
	}

	data, err := p.call("get_property", "time-pos")
	if err != nil {
		// mpv errors this property when nothing is actually loaded yet —
		// report the last-known status rather than failing the caller.
		return status, nil
	}
	var pos float64
	if err := json.Unmarshal(data, &pos); err == nil {
		status.Position = pos
	}
	return status, nil
}

func (p *MPVPlayer) Close() error {
	p.mu.Lock()
	p.closed = true // must be set before reading conn/cmd/socketDir: stops any in-flight respawn from racing this
	conn := p.conn
	cmd := p.cmd
	socketDir := p.socketDir
	p.conn = nil
	p.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	os.RemoveAll(socketDir)
	return nil
}

// cappedBuffer collects up to max bytes and silently drops the rest. mpv
// runs for the lifetime of the device, so stderr can't be buffered
// without bound — this exists purely to surface a useful error message
// if the process fails to start (e.g. no video output available).
type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (w *cappedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if remaining := w.max - w.buf.Len(); remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		w.buf.Write(p[:remaining])
	}
	return len(p), nil
}

func (w *cappedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}
