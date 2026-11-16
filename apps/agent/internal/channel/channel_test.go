package channel

import "testing"

func TestWSURL(t *testing.T) {
	ok := map[string]string{
		"http://ha.local:8123":            "ws://ha.local:8123/api/deckbeacon/v1/ws",
		"https://x.ui.nabu.casa/":         "wss://x.ui.nabu.casa/api/deckbeacon/v1/ws",
		"https://example.com/ha?x=1#frag": "wss://example.com/ha/api/deckbeacon/v1/ws",
		"wss://example.com":               "wss://example.com/api/deckbeacon/v1/ws",
	}
	for in, want := range ok {
		got, err := WSURL(in, PathSession)
		if err != nil || got != want {
			t.Errorf("WSURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"ftp://x", "javascript:alert(1)", "http://", "::"} {
		if _, err := WSURL(bad, PathSession); err == nil {
			t.Errorf("WSURL(%q) accepted", bad)
		}
	}
}
