// Package trust stores the pairing result: the pinned HA static key and the
// connection profile (ADR-0002). It contains no secrets, but changes to it are
// security relevant, so it is written atomically with owner-only permissions.
package trust

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/fsutil"
)

// URL is one way to reach HA.
type URL struct {
	URL  string `json:"url"`
	Kind string `json:"kind"` // internal | external | cloud | manual | manual_remote | discovered
}

// Remote reports whether this URL counts as the remote transport.
func (u URL) Remote() bool {
	return u.Kind == "external" || u.Kind == "cloud" || u.Kind == "manual_remote"
}

var profileKinds = map[string]bool{"internal": true, "external": true, "cloud": true, "manual": true, "manual_remote": true}

// ValidateProfile cleans a connection profile from HA (ADR-0011): http/https
// only, no credentials, bounded, known kinds, duplicates dropped. It returns
// an error if nothing usable remains, so callers never wipe a working list.
func ValidateProfile(in []URL) ([]URL, error) {
	var out []URL
	seen := map[string]bool{}
	for _, u := range in {
		if len(out) == 8 || !profileKinds[u.Kind] || len(u.URL) > 512 {
			continue
		}
		p, err := url.Parse(u.URL)
		if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" || p.User != nil || p.RawQuery != "" || p.Fragment != "" {
			continue
		}
		clean := strings.TrimRight(p.String(), "/")
		if seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, URL{URL: clean, Kind: u.Kind})
	}
	if len(out) == 0 {
		return nil, errors.New("trust: connection profile has no usable URL")
	}
	return out, nil
}

// SameURLs reports whether two profiles are identical (order matters).
func SameURLs(a, b []URL) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Trust is the persisted pairing.
type Trust struct {
	ServerStatic string    `json:"server_static"` // hex X25519 public key
	InstanceName string    `json:"instance_name"`
	URLs         []URL     `json:"urls"`
	PairedAt     time.Time `json:"paired_at"`
	Revoked      bool      `json:"revoked,omitempty"`
}

// ServerKey decodes the pinned key.
func (t *Trust) ServerKey() ([]byte, error) {
	k, err := hex.DecodeString(t.ServerStatic)
	if err != nil || len(k) != 32 {
		return nil, errors.New("trust: invalid server key")
	}
	return k, nil
}

// ErrNotPaired is returned when no trust file exists.
var ErrNotPaired = errors.New("not paired")

func file(stateDir string) string { return filepath.Join(stateDir, "trust.json") }

// Load reads the trust file.
func Load(stateDir string) (*Trust, error) {
	b, err := os.ReadFile(file(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotPaired
	}
	if err != nil {
		return nil, err
	}
	var t Trust
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("trust: %w", err)
	}
	if _, err := t.ServerKey(); err != nil {
		return nil, err
	}
	if len(t.URLs) == 0 {
		return nil, errors.New("trust: no URLs")
	}
	return &t, nil
}

// Save writes the trust file atomically.
func Save(stateDir string, t *Trust) error {
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(file(stateDir), b, 0o600)
}

// Remove deletes the trust file (unpair). Missing is not an error.
func Remove(stateDir string) error {
	err := os.Remove(file(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
