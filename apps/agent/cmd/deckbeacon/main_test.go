package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestUsageAndVersion(t *testing.T) {
	cases := []struct {
		args []string
		code int
	}{
		{nil, 2}, {[]string{"version"}, 0}, {[]string{"help"}, 0}, {[]string{"bogus"}, 2}, {[]string{"pair", "--nope"}, 2},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		if got := run(c.args, strings.NewReader(""), &out, &errb); got != c.code {
			t.Errorf("run(%v) = %d, want %d (stderr=%q)", c.args, got, c.code, errb.String())
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"version", "--json"}, nil, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil || v["protocol"].(float64) != 1 {
		t.Fatalf("version json %q", out.String())
	}
}

func TestRefusesNonSteamOS(t *testing.T) {
	t.Setenv("DECKBEACON_STATE_DIR", t.TempDir())
	t.Setenv("DECKBEACON_CONFIG_DIR", t.TempDir())
	root, _ := filepath.Abs("../../testdata/roots/not-steamos")
	var out, errb bytes.Buffer
	if code := run([]string{"run", "--root", root}, nil, &out, &errb); code != 1 || !strings.Contains(errb.String(), "SteamOS only") {
		t.Fatalf("code %d stderr %q", code, errb.String())
	}
}

func TestStatusUnpaired(t *testing.T) {
	t.Setenv("DECKBEACON_STATE_DIR", t.TempDir())
	t.Setenv("DECKBEACON_CONFIG_DIR", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	var out, errb bytes.Buffer
	if code := run([]string{"status", "--json"}, nil, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	var s map[string]any
	_ = json.Unmarshal(out.Bytes(), &s)
	if s["paired_with"] != nil || len(s["device_id"].(string)) != 26 {
		t.Fatalf("status %v", s)
	}
}

func TestProbeRecordedG15(t *testing.T) {
	t.Setenv("DECKBEACON_LOGIND_ADDRESS", "none")
	t.Setenv("DECKBEACON_STATE_DIR", t.TempDir())
	root, _ := filepath.Abs("../../testdata/roots/dell-g15-5520")
	var out, errb bytes.Buffer
	if code := run([]string{"probe", "--json", "--root", root}, nil, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	var p struct {
		Device       map[string]any   `json:"device"`
		Metrics      []map[string]any `json:"metrics"`
		Capabilities []map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Device["model"] != "Dell G15 5520" || len(p.Metrics) < 8 || len(p.Capabilities) < 10 {
		t.Fatalf("probe %+v", p.Device)
	}
}
