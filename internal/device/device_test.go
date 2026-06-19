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

func TestAPITokenEmptyUntilGenerated(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	token, err := APIToken()
	if err != nil {
		t.Fatalf("APIToken() error = %v", err)
	}
	if token != "" {
		t.Fatalf("expected no token before claiming, got %q", token)
	}
}

func TestGenerateAPITokenPersistsTokenAndStudioURL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	token, err := GenerateAPIToken("http://192.168.1.10:7095")
	if err != nil {
		t.Fatalf("GenerateAPIToken() error = %v", err)
	}
	if len(token) < 32 {
		t.Fatalf("expected a long random token, got %q (len %d)", token, len(token))
	}

	gotToken, err := APIToken()
	if err != nil {
		t.Fatalf("APIToken() error = %v", err)
	}
	if gotToken != token {
		t.Fatalf("APIToken() = %q, want the just-generated %q", gotToken, token)
	}

	gotURL, err := StudioURL()
	if err != nil {
		t.Fatalf("StudioURL() error = %v", err)
	}
	if gotURL != "http://192.168.1.10:7095" {
		t.Fatalf("StudioURL() = %q, want the URL passed to GenerateAPIToken", gotURL)
	}
}

func TestGenerateAPITokenKeepsExistingStudioURLWhenNotGiven(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if _, err := GenerateAPIToken("http://192.168.1.10:7095"); err != nil {
		t.Fatalf("first GenerateAPIToken() error = %v", err)
	}
	if _, err := GenerateAPIToken(""); err != nil {
		t.Fatalf("second GenerateAPIToken() error = %v", err)
	}

	gotURL, err := StudioURL()
	if err != nil {
		t.Fatalf("StudioURL() error = %v", err)
	}
	if gotURL != "http://192.168.1.10:7095" {
		t.Fatalf("StudioURL() = %q, want the previously persisted URL to survive an empty re-mint", gotURL)
	}
}

func TestResetWipesEverything(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	id, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate() error = %v", err)
	}
	if _, err := PairingCode(); err != nil {
		t.Fatalf("PairingCode() error = %v", err)
	}
	if _, err := GenerateAPIToken("http://192.168.1.10:7095"); err != nil {
		t.Fatalf("GenerateAPIToken() error = %v", err)
	}

	if err := Reset(); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}

	token, err := APIToken()
	if err != nil {
		t.Fatalf("APIToken() after reset error = %v", err)
	}
	if token != "" {
		t.Fatalf("expected no token after Reset(), got %q", token)
	}

	url, err := StudioURL()
	if err != nil {
		t.Fatalf("StudioURL() after reset error = %v", err)
	}
	if url != "" {
		t.Fatalf("expected no studio URL after Reset(), got %q", url)
	}

	newID, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate() after reset error = %v", err)
	}
	if newID.ID == id.ID {
		t.Fatalf("expected a fresh device ID after Reset(), got the same one: %q", newID.ID)
	}
}
