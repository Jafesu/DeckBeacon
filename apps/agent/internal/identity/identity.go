// Package identity manages the per-install device keys and stable device ID (ADR-0002).
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jafesu/deckbeacon/apps/agent/internal/fsutil"
	"github.com/jafesu/deckbeacon/packages/protocol/noise"
)

// Identity is the device's long-term key material. Never log or serialise it.
type Identity struct {
	DeviceID string
	Channel  noise.KeyPair      // X25519 static (Noise)
	Signing  ed25519.PrivateKey // Ed25519 (device ID derivation, future signed exports)
}

const (
	channelFile = "x25519.key"
	signingFile = "ed25519.key"
)

// DeviceIDFor derives the stable device ID from the Ed25519 public key.
func DeviceIDFor(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))[:26]
}

// LoadOrCreate loads keys from dir/identity, creating them on first run.
func LoadOrCreate(stateDir string) (*Identity, error) {
	dir := filepath.Join(stateDir, "identity")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	chPriv, err := loadOrCreateKey(filepath.Join(dir, channelFile), 32, func() ([]byte, error) {
		kp, err := noise.GenerateKeyPair(rand.Reader)
		return kp.Private, err
	})
	if err != nil {
		return nil, err
	}
	sgSeed, err := loadOrCreateKey(filepath.Join(dir, signingFile), ed25519.SeedSize, func() ([]byte, error) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		return priv.Seed(), err
	})
	if err != nil {
		return nil, err
	}
	ch, err := noise.KeyPairFromPrivate(chPriv)
	if err != nil {
		return nil, err
	}
	sg := ed25519.NewKeyFromSeed(sgSeed)
	return &Identity{DeviceID: DeviceIDFor(sg.Public().(ed25519.PublicKey)), Channel: ch, Signing: sg}, nil
}

func loadOrCreateKey(path string, size int, gen func() ([]byte, error)) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		st, statErr := os.Stat(path)
		if statErr != nil {
			return nil, statErr
		}
		if st.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("identity: %s is accessible by other users (mode %v); refusing to use it", path, st.Mode().Perm())
		}
		if len(b) != size {
			return nil, fmt.Errorf("identity: %s has wrong length", path)
		}
		return b, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	b, err = gen()
	if err != nil {
		return nil, err
	}
	if err := fsutil.WriteFileAtomic(path, b, 0o600); err != nil {
		return nil, err
	}
	return b, nil
}
