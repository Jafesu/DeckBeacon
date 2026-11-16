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
	"os"
	"path/filepath"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/fsutil"
)

// URL is one way to reach HA.
type URL struct {
	URL  string `json:"url"`
	Kind string `json:"kind"` // internal | external | cloud | manual
}

// Remote reports whether this URL counts as the remote transport.
func (u URL) Remote() bool { return u.Kind == "external" || u.Kind == "cloud" }

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
