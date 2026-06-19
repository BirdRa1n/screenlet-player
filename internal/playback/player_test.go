package playback

import "testing"

func TestNoopPlayer(t *testing.T) {
	p := NewNoopPlayer()

	status, err := p.Status()
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Playing {
		t.Fatalf("expected Playing=false before Play(), got true")
	}

	const source = "http://example.com/playlist.m3u"
	if err := p.Play(source); err != nil {
		t.Fatalf("Play() error = %v", err)
	}
	status, _ = p.Status()
	if !status.Playing || status.Source != source {
		t.Fatalf("Status() = %+v, want Playing=true Source=%q", status, source)
	}

	if err := p.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	status, _ = p.Status()
	if status.Playing {
		t.Fatalf("expected Playing=false after Stop(), got true")
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
