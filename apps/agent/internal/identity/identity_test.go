package identity

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestLoadOrCreateIsStable(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.DeviceID != b.DeviceID || string(a.Channel.Public) != string(b.Channel.Public) {
		t.Fatal("identity changed across loads")
	}
	if !regexp.MustCompile(`^[a-z2-7]{26}$`).MatchString(a.DeviceID) {
		t.Fatalf("device id %q does not match protocol format", a.DeviceID)
	}
	for _, f := range []string{"x25519.key", "ed25519.key"} {
		st, err := os.Stat(filepath.Join(dir, "identity", f))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", f, st.Mode().Perm())
		}
	}
}

func TestRejectsWorldReadableKey(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreate(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "identity", "x25519.key"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(dir); err == nil {
		t.Fatal("expected refusal for group/world-readable key")
	}
}

func TestDistinctInstallsDistinctIDs(t *testing.T) {
	a, _ := LoadOrCreate(t.TempDir())
	b, _ := LoadOrCreate(t.TempDir())
	if a.DeviceID == b.DeviceID {
		t.Fatal("two installs share a device id")
	}
}
