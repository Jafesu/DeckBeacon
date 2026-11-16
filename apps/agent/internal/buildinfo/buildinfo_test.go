package buildinfo

import (
	"strings"
	"testing"
)

func TestGetDefaults(t *testing.T) {
	i := Get()
	if i.Protocol != 1 {
		t.Fatalf("protocol = %d, want 1", i.Protocol)
	}
	if !strings.Contains(i.String(), "protocol v1") || !strings.HasPrefix(i.String(), "deckbeacon-agent ") {
		t.Fatalf("unexpected string %q", i.String())
	}
}
