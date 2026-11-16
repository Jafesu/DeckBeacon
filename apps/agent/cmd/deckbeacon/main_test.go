package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRun(t *testing.T) {
	cases := []struct {
		args []string
		code int
	}{
		{nil, 2}, {[]string{"version"}, 0}, {[]string{"help"}, 0}, {[]string{"bogus"}, 2},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		if got := run(c.args, &out, &errb); got != c.code {
			t.Errorf("run(%v) = %d, want %d (stderr=%q)", c.args, got, c.code, errb.String())
		}
	}
}

func TestVersionJSON(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version", "--json"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v["protocol"].(float64) != 1 {
		t.Fatalf("protocol = %v", v["protocol"])
	}
}
