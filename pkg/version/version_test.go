package version

import (
	"strings"
	"testing"
)

func TestStringContainsAllFields(t *testing.T) {
	s := String()
	for _, want := range []string{Version, Commit, BuildDate} {
		if !strings.Contains(s, want) {
			t.Fatalf("String() = %q, want it to contain %q", s, want)
		}
	}
}
