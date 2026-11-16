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
	compiled    map[string]*jsonschema.Schema
	compileErr  error
)

func compileAll() {
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	c.DefaultDraft(jsonschema.Draft2020)
	names, err := fs.Glob(schemaFS, "schemas/v1/*.schema.json")
	if err != nil {
		compileErr = err
		return
	}
	for _, n := range names {
		raw, err := schemaFS.ReadFile(n)
		if err != nil {
			compileErr = err
			return
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			compileErr = fmt.Errorf("%s: %w", n, err)
			return
		}
		if err := c.AddResource(BaseURI+path.Base(n), doc); err != nil {
			compileErr = fmt.Errorf("%s: %w", n, err)
			return
		}
	}
	compiled = make(map[string]*jsonschema.Schema, len(names))
	for _, n := range names {
		name := strings.TrimSuffix(path.Base(n), ".schema.json")
		s, err := c.Compile(BaseURI + path.Base(n))
		if err != nil {
			compileErr = fmt.Errorf("compile %s: %w", name, err)
			return
		}
		compiled[name] = s
	}
}

// Schema returns the compiled schema with the given short name (e.g. "envelope").
func Schema(name string) (*jsonschema.Schema, error) {
	compileOnce.Do(compileAll)
	if compileErr != nil {
		return nil, compileErr
	}
	s, ok := compiled[name]
	if !ok {
		return nil, fmt.Errorf("protocol: unknown schema %q", name)
	}
	return s, nil
}

// Validate checks raw JSON against the named schema. A nil error means valid.
// The returned error is a *jsonschema.ValidationError for schema violations.
func Validate(name string, raw []byte) error {
	s, err := Schema(name)
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("protocol: invalid JSON: %w", err)
	}
	return s.Validate(inst)
}

// ValidateMessage validates a full envelope.
func ValidateMessage(raw []byte) error { return Validate("envelope", raw) }
