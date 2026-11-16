package protocol

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type fixture struct {
	Description string          `json:"description"`
	Schema      string          `json:"schema"`
	ErrorPath   *string         `json:"error_path"`
	Message     json.RawMessage `json:"message"`
}

func loadFixtures(t *testing.T, kind string) map[string]fixture {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("fixtures", kind, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no %s fixtures found (err=%v)", kind, err)
	}
	out := make(map[string]fixture, len(files))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var fx fixture
		if err := json.Unmarshal(raw, &fx); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out[strings.TrimSuffix(filepath.Base(f), ".json")] = fx
	}
	return out
}

func TestAllSchemasCompile(t *testing.T) {
	for _, name := range []string{"envelope", "common", "hello", "welcome", "capabilities", "telemetry", "event",
		"command", "command_result", "notification", "notification_status", "ack", "ping", "error", "pair_request", "pair_confirm", "pair_complete", "pair_abort"} {
		if _, err := Schema(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestValidFixtures(t *testing.T) {
	for name, fx := range loadFixtures(t, "valid") {
		t.Run(name, func(t *testing.T) {
			if err := Validate(fx.Schema, fx.Message); err != nil {
				t.Fatalf("expected valid: %v", err)
			}
		})
	}
}

func TestInvalidFixtures(t *testing.T) {
	for name, fx := range loadFixtures(t, "invalid") {
		t.Run(name, func(t *testing.T) {
			if fx.ErrorPath == nil {
				t.Fatal("invalid fixture must declare error_path")
			}
			err := Validate(fx.Schema, fx.Message)
			var ve *jsonschema.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("expected schema violation (%s), got %v", fx.Description, err)
			}
			got := leafPaths(ve)
			if len(got) != 1 || got[0] != *fx.ErrorPath {
				t.Fatalf("%s: failing locations %v, want [%q]", fx.Description, got, *fx.ErrorPath)
			}
		})
	}
}

func TestUnknownSchemaAndBadJSON(t *testing.T) {
	if err := Validate("nope", []byte(`{}`)); err == nil {
		t.Fatal("expected error for unknown schema")
	}
	if err := ValidateMessage([]byte(`{`)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// leafPaths returns the distinct JSON Pointers of the innermost failures, mirroring
// the Python test's walk over error.context.
func leafPaths(ve *jsonschema.ValidationError) []string {
	set := map[string]struct{}{}
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			p := ""
			for _, part := range e.InstanceLocation {
				p += "/" + part
			}
			set[p] = struct{}{}
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
