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

func TestPairingCodePersistsAndIsUnambiguous(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	first, err := PairingCode()
	if err != nil {
		t.Fatalf("PairingCode() error = %v", err)
	}
	if len(first) != 5 {
		t.Fatalf("expected a 5-character code, got %q", first)
	}
	for _, c := range first {
		if c == '0' || c == 'O' || c == '1' || c == 'I' {
			t.Fatalf("code %q contains an ambiguous character %q", first, c)
		}
	}

	second, err := PairingCode()
	if err != nil {
		t.Fatalf("PairingCode() second call error = %v", err)
	}
	if second != first {
		t.Fatalf("pairing code changed across calls: %q != %q", first, second)
	}
}
