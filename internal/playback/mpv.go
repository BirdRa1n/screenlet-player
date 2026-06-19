package playback

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
// never requires a restart (unlike the Kodi+SSH bridge it replaces).
type MPVPlayer struct {
	cmd       *exec.Cmd
	socketDir string // removed wholesale on Close
	stderr    *cappedBuffer

	mu      sync.Mutex
	conn    net.Conn
	pending map[int64]chan mpvResponse
	status  Status

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

// NewMPVPlayer launches mpv idle and connects to its JSON IPC socket. The
// returned Player owns the mpv process: Close stops both. An error here
// almost always means mpv isn't installed or has no usable video output —
// callers on a dev machine should fall back to NewNoopPlayer rather than
// treat it as fatal.
func NewMPVPlayer(opts MPVOptions) (*MPVPlayer, error) {
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

	p := &MPVPlayer{
		cmd:       cmd,
		socketDir: sockDir,
		stderr:    stderr,
		conn:      conn,
		pending:   make(map[int64]chan mpvResponse),
	}
	go p.readLoop(conn)
	return p, nil
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
// same stream, matched only by the response's request_id.
func (p *MPVPlayer) readLoop(conn net.Conn) {
	decoder := json.NewDecoder(conn)
	for {
		var resp mpvResponse
		if err := decoder.Decode(&resp); err != nil {
			p.mu.Lock()
			for id, ch := range p.pending {
				close(ch)
				delete(p.pending, id)
			}
			p.conn = nil
			p.mu.Unlock()
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
	p.mu.Unlock()
	return nil
}

func (p *MPVPlayer) Stop() error {
	if _, err := p.call("stop"); err != nil {
		return err
	}
	p.mu.Lock()
	p.status = Status{}
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
	conn := p.conn
	p.conn = nil
	p.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	}
	os.RemoveAll(p.socketDir)
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
