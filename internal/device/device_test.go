package device

import "testing"

func TestLoadOrCreatePersistsID(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	first, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate() error = %v", err)
	}
	if first.ID == "" {
		t.Fatalf("expected a generated device ID, got empty string")
	}

	second, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate() second call error = %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("device ID changed across calls: %q != %q", first.ID, second.ID)
	}
}
