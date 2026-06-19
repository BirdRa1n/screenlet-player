package storage

import "testing"

func TestSaveAndLoad(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DeviceID != "" {
		t.Fatalf("expected empty Config on first run, got %+v", cfg)
	}

	cfg.DeviceID = "test-device-id"
	cfg.ChannelID = "channel-123"
	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load() after Save() error = %v", err)
	}
	if reloaded.DeviceID != cfg.DeviceID || reloaded.ChannelID != cfg.ChannelID {
		t.Fatalf("Load() = %+v, want %+v", reloaded, cfg)
	}
}
