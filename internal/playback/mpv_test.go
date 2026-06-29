package playback

import (
	"os/exec"
	"testing"
	"time"
)

// requireMPV skips the test when mpv isn't installed — CI runners and most
// dev machines won't have it, and these tests exercise the real process +
// IPC socket rather than a mock.
func requireMPV(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv not found in PATH, skipping real-process test")
	}
}

func TestMPVPlayer_Lifecycle(t *testing.T) {
	requireMPV(t)

	p, err := NewMPVPlayer(MPVOptions{ExtraArgs: []string{"--vo=null", "--ao=null"}})
	if err != nil {
		t.Fatalf("NewMPVPlayer() error = %v", err)
	}
	defer p.Close()

	status, err := p.Status()
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Playing {
		t.Fatalf("expected Playing=false before Play(), got true")
	}

	// loadfile is fire-and-forget at the IPC level — mpv queues the load
	// and reports success immediately, so this exercises the real
	// request/response wiring without depending on network access.
	const source = "av://lavfi:testsrc"
	if err := p.Play(source); err != nil {
		t.Fatalf("Play() error = %v", err)
	}
	status, err = p.Status()
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Playing || status.Source != source {
		t.Fatalf("Status() = %+v, want Playing=true Source=%q", status, source)
	}

	if err := p.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	status, err = p.Status()
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Playing {
		t.Fatalf("expected Playing=false after Stop(), got true")
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestMPVPlayer_MultiplePlayCalls(t *testing.T) {
	requireMPV(t)

	p, err := NewMPVPlayer(MPVOptions{ExtraArgs: []string{"--vo=null", "--ao=null"}})
	if err != nil {
		t.Fatalf("NewMPVPlayer() error = %v", err)
	}
	defer p.Close()

	// The whole point of the mpv-backed player: switching the assigned
	// channel reuses the same long-running process via loadfile, no
	// restart, unlike the Kodi+SSH bridge this replaces.
	for i := 0; i < 3; i++ {
		if err := p.Play("av://lavfi:testsrc"); err != nil {
			t.Fatalf("Play() call %d error = %v", i, err)
		}
	}
}

func TestNewMPVPlayer_BinaryNotFound(t *testing.T) {
	_, err := NewMPVPlayer(MPVOptions{BinPath: "screenlet-player-definitely-not-a-real-binary"})
	if err == nil {
		t.Fatal("expected error for missing mpv binary, got nil")
	}
}

func TestMPVPlayer_RespawnsAfterCrash(t *testing.T) {
	requireMPV(t)

	p, err := NewMPVPlayer(MPVOptions{ExtraArgs: []string{"--vo=null", "--ao=null"}})
	if err != nil {
		t.Fatalf("NewMPVPlayer() error = %v", err)
	}
	defer p.Close()

	const source = "av://lavfi:testsrc"
	if err := p.Play(source); err != nil {
		t.Fatalf("Play() error = %v", err)
	}

	p.mu.Lock()
	pid := p.cmd.Process.Pid
	p.mu.Unlock()

	// Simulate a real crash (OOM, segfault, anything) — not Close(), which
	// must NOT trigger a respawn (that path is covered by CloseIsClean).
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatalf("failed to kill mpv process for the test: %v", err)
	}

	// Status() deliberately reports the last-known state even while
	// disconnected (so a transient "time-pos unavailable" doesn't look
	// like a hard failure to normal callers), so it can't be used to
	// detect respawn completion — poll for a genuinely new process instead.
	deadline := time.Now().Add(10 * time.Second)
	var newPid int
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for mpv to respawn")
		}
		p.mu.Lock()
		if p.cmd != nil && p.cmd.Process != nil && p.cmd.Process.Pid != pid {
			newPid = p.cmd.Process.Pid
		}
		p.mu.Unlock()
		if newPid != 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("respawned: old PID %d -> new PID %d", pid, newPid)

	// Now confirm playback actually resumed on the new process, not just
	// that a process exists — call() talks to whatever p.conn currently is.
	deadline = time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for playback to resume after respawn")
		}
		if _, err := p.call("get_property", "time-pos"); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	status, err := p.Status()
	if err != nil {
		t.Fatalf("Status() after respawn error = %v", err)
	}
	if !status.Playing || status.Source != source {
		t.Fatalf("Status() after respawn = %+v, want Playing=true Source=%q", status, source)
	}
}

func TestMPVPlayer_CloseIsClean(t *testing.T) {
	requireMPV(t)

	p, err := NewMPVPlayer(MPVOptions{ExtraArgs: []string{"--vo=null", "--ao=null"}})
	if err != nil {
		t.Fatalf("NewMPVPlayer() error = %v", err)
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Give the process a moment to fully exit, then confirm calls against
	// a closed player fail instead of hanging.
	time.Sleep(100 * time.Millisecond)
	if err := p.Play("av://lavfi:testsrc"); err == nil {
		t.Fatal("expected Play() to fail after Close(), got nil error")
	}
}
