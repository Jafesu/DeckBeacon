package trust

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRoundTripAndRemove(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("want ErrNotPaired, got %v", err)
	}
	in := &Trust{ServerStatic: strings.Repeat("ab", 32), InstanceName: "Home", URLs: []URL{{"http://ha:8123", "internal"}, {"https://x.ui.nabu.casa", "cloud"}}, PairedAt: time.Unix(1, 0).UTC()}
	if err := Save(dir, in); err != nil {
		t.Fatal(err)
	}
	out, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if out.InstanceName != "Home" || !out.URLs[1].Remote() || out.URLs[0].Remote() {
		t.Fatalf("unexpected %+v", out)
	}
	if err := Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := Remove(dir); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsBadKey(t *testing.T) {
	dir := t.TempDir()
	_ = Save(dir, &Trust{ServerStatic: "zz", URLs: []URL{{"http://x", "internal"}}})
	if _, err := Load(dir); err == nil {
		t.Fatal("accepted invalid key")
	}
}
