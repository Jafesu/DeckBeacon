// Package protocol embeds the DeckBeacon wire-protocol JSON Schemas and validates
// messages against them. The schemas in schemas/ are the source of truth; Go and
// Python both test against the shared fixtures in fixtures/.
package protocol

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Version is the protocol major version described by the embedded schemas.
const Version = 1

// BaseURI is the $id prefix of every v1 schema.
const BaseURI = "https://schemas.deckbeacon.invalid/protocol/v1/"

//go:embed schemas/v1/*.schema.json
var schemaFS embed.FS

var (
	compileOnce sync.Once
	compiled    map[string]*jsonschema.Schema // sender (strict) schemas
	receiver    map[string]*jsonschema.Schema // receiver schemas: unknown fields allowed
	compileErr  error
)

func compileAll() {
	compiled, compileErr = compileSet(false)
	if compileErr == nil {
		receiver, compileErr = compileSet(true)
	}
}

// openObjects removes `"additionalProperties": false`, so a receiver ignores
// fields added by a newer minor version (COMPATIBILITY.md). Types, required
// fields, formats, bounds and enums are still enforced. Objects marked
// `"x-receiver": "closed"` stay closed: unknown command arguments, account
// data in library entries, service calls in notification actions and
// envelope fields are refused on receipt too.
func openObjects(v any) {
	switch x := v.(type) {
	case map[string]any:
		if ap, ok := x["additionalProperties"].(bool); ok && !ap && x["x-receiver"] != "closed" {
			delete(x, "additionalProperties")
		}
		for _, child := range x {
			openObjects(child)
		}
	case []any:
		for _, child := range x {
			openObjects(child)
		}
	}
}

func compileSet(lenient bool) (map[string]*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	c.DefaultDraft(jsonschema.Draft2020)
	names, err := fs.Glob(schemaFS, "schemas/v1/*.schema.json")
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		raw, err := schemaFS.ReadFile(n)
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		if lenient {
			openObjects(doc)
		}
		if err := c.AddResource(BaseURI+path.Base(n), doc); err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
	}
	out := make(map[string]*jsonschema.Schema, len(names))
	for _, n := range names {
		name := strings.TrimSuffix(path.Base(n), ".schema.json")
		s, err := c.Compile(BaseURI + path.Base(n))
		if err != nil {
			return nil, fmt.Errorf("compile %s: %w", name, err)
		}
		out[name] = s
	}
	return out, nil
}

// Schema returns the compiled (sender) schema with the given short name (e.g. "envelope").
func Schema(name string) (*jsonschema.Schema, error) { return schemaFrom(false, name) }

func schemaFrom(lenient bool, name string) (*jsonschema.Schema, error) {
	compileOnce.Do(compileAll)
	if compileErr != nil {
		return nil, compileErr
	}
	set := compiled
	if lenient {
		set = receiver
	}
	s, ok := set[name]
	if !ok {
		return nil, fmt.Errorf("protocol: unknown schema %q", name)
	}
	return s, nil
}

// Validate checks raw JSON against the named schema as a sender must: exactly
// what this minor version may emit. A nil error means valid. The returned error
// is a *jsonschema.ValidationError for schema violations.
func Validate(name string, raw []byte) error { return validate(false, name, raw) }

// ValidateInbound checks a received message as a receiver must: fields a newer
// minor version added are ignored, everything else is enforced.
func ValidateInbound(name string, raw []byte) error { return validate(true, name, raw) }

func validate(lenient bool, name string, raw []byte) error {
	s, err := schemaFrom(lenient, name)
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("protocol: invalid JSON: %w", err)
	}
	return s.Validate(inst)
}

// ValidateMessage validates a full envelope before sending.
func ValidateMessage(raw []byte) error { return Validate("envelope", raw) }

// ValidateInboundMessage validates a received envelope (lenient receiver).
func ValidateInboundMessage(raw []byte) error { return ValidateInbound("envelope", raw) }
