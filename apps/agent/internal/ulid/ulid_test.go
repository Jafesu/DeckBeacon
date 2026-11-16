package ulid

import (
	"regexp"
	"testing"
	"time"
)

var re = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

func TestFormatAndOrdering(t *testing.T) {
	a := At(time.UnixMilli(1_700_000_000_000))
	b := At(time.UnixMilli(1_700_000_000_001))
	if !re.MatchString(a) || !re.MatchString(b) {
		t.Fatalf("bad ulid %q %q", a, b)
	}
	if a[:10] >= b[:10] {
		t.Fatalf("timestamp prefix not ordered: %s %s", a, b)
	}
	if New() == New() {
		t.Fatal("duplicate ulid")
	}
}

func TestKnownTimestampPrefix(t *testing.T) {
	// 0 ms -> 10 zero chars.
	if got := At(time.UnixMilli(0))[:10]; got != "0000000000" {
		t.Fatalf("got %s", got)
	}
	// Max 48-bit ms -> "7ZZZZZZZZZ" per the ULID spec.
	if got := At(time.UnixMilli(1<<48 - 1))[:10]; got != "7ZZZZZZZZZ" {
		t.Fatalf("got %s", got)
	}
}
