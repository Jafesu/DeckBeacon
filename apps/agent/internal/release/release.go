// Package release defines signed agent releases (M9, ADR-0010 D1).
//
// A release is a set of binaries plus release.json, a manifest that lists every
// asset's sha256 and size and the release's compatibility. The manifest is
// signed with Ed25519; the agent trusts only keys embedded at build time. The
// signature covers the exact manifest bytes, and the hashes bind the binaries.
package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// ManifestSchema is the release.json format version.
const ManifestSchema = 1

// Channels, from most to least conservative.
var Channels = []string{"stable", "beta", "dev"}

// Manifest is release.json.
type Manifest struct {
	Schema         int              `json:"schema"`
	Version        string           `json:"version"`
	Channel        string           `json:"channel"`
	Published      time.Time        `json:"published"`
	MinProtocol    int              `json:"min_protocol"`
	MinIntegration string           `json:"min_integration"`
	NotesURL       string           `json:"notes_url,omitempty"`
	Assets         map[string]Asset `json:"assets"`
}

// Asset is one downloadable file.
type Asset struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// AssetName is the binary for an architecture ("amd64", "arm64").
func AssetName(goarch string) string { return "deckbeacon-linux-" + goarch }

var (
	ErrUnsigned     = errors.New("release: signature missing or malformed")
	ErrBadSignature = errors.New("release: signature does not verify with any trusted key")
	ErrNoKeys       = errors.New("release: this build trusts no release key")
)

// Verify checks sig over manifest with any trusted key, then parses it.
// Nothing in the manifest is used before the signature verifies.
func Verify(manifest, sig []byte, trusted []ed25519.PublicKey) (*Manifest, error) {
	if len(trusted) == 0 {
		return nil, ErrNoKeys
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return nil, ErrUnsigned
	}
	ok := false
	for _, k := range trusted {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, manifest, raw) {
			ok = true
			break
		}
	}
	if !ok {
		return nil, ErrBadSignature
	}
	var m Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		return nil, fmt.Errorf("release: manifest: %w", err)
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	if m.Schema != ManifestSchema {
		return fmt.Errorf("release: unsupported manifest schema %d", m.Schema)
	}
	v, err := ParseVersion(m.Version)
	if err != nil {
		return err
	}
	if m.Channel != v.Channel() {
		return fmt.Errorf("release: version %s does not belong to channel %q", m.Version, m.Channel)
	}
	if m.MinProtocol < 1 || len(m.Assets) == 0 {
		return errors.New("release: incomplete manifest")
	}
	for name, a := range m.Assets {
		if !strings.HasPrefix(name, "deckbeacon-linux-") || strings.ContainsAny(name, "/\\") || a.Size <= 0 || len(a.SHA256) != 64 {
			return fmt.Errorf("release: bad asset entry %q", name)
		}
		if _, err := hex.DecodeString(a.SHA256); err != nil {
			return fmt.Errorf("release: bad asset hash for %q", name)
		}
	}
	return nil
}

// CheckAsset verifies a downloaded file against the (verified) manifest entry.
func (m *Manifest) CheckAsset(name string, r io.Reader) error {
	a, ok := m.Assets[name]
	if !ok {
		return fmt.Errorf("release: no asset %s in this release", name)
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(r, a.Size+1))
	if err != nil {
		return err
	}
	if n != a.Size {
		return fmt.Errorf("release: %s is %d bytes, manifest says %d", name, n, a.Size)
	}
	if hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return fmt.Errorf("release: %s checksum mismatch", name)
	}
	return nil
}

// Sign returns the base64 signature line for manifest.
func Sign(manifest []byte, key ed25519.PrivateKey) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, manifest)) + "\n")
}

// LoadPrivateKey reads a PKCS#8 PEM Ed25519 key, the format of
// `openssl genpkey -algorithm ed25519` and `deckbeacon release keygen`.
func LoadPrivateKey(pemBytes []byte) (ed25519.PrivateKey, error) {
	b, _ := pem.Decode(pemBytes)
	if b == nil {
		return nil, errors.New("release: no PEM block in key")
	}
	k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		return nil, fmt.Errorf("release: key: %w", err)
	}
	ek, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("release: key is not Ed25519")
	}
	return ek, nil
}

// EncodePrivateKey writes a PKCS#8 PEM.
func EncodePrivateKey(k ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// ParsePublicKey accepts the base64 raw key (as embedded) or a PEM SPKI key
// (as `openssl pkey -pubout` prints).
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	s = strings.TrimSpace(s)
	if b, _ := pem.Decode([]byte(s)); b != nil {
		k, err := x509.ParsePKIXPublicKey(b.Bytes)
		if err != nil {
			return nil, err
		}
		ek, ok := k.(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("release: public key is not Ed25519")
		}
		return ek, nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("release: public key must be base64 of 32 bytes or a PEM block")
	}
	return ed25519.PublicKey(raw), nil
}

// EncodePublicKey is the base64 form embedded in the agent.
func EncodePublicKey(k ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(k) }

// BuildManifest hashes files into a manifest (used by the release workflow).
func BuildManifest(version string, minProtocol int, minIntegration, notesURL string, files map[string]string, now time.Time) ([]byte, error) {
	v, err := ParseVersion(version)
	if err != nil {
		return nil, err
	}
	m := Manifest{Schema: ManifestSchema, Version: version, Channel: v.Channel(), Published: now.UTC().Truncate(time.Second), MinProtocol: minProtocol, MinIntegration: minIntegration, NotesURL: notesURL, Assets: map[string]Asset{}}
	for name, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		m.Assets[name] = Asset{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(b))}
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Version is a semantic version restricted to the agent's channels:
// X.Y.Z (stable), X.Y.Z-beta.N, X.Y.Z-dev.N.
type Version struct {
	Major, Minor, Patch int
	Pre                 string // "", "beta", "dev"
	PreN                int
}

// ParseVersion parses an agent version (a leading "v" is allowed).
func ParseVersion(s string) (Version, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	var v Version
	if len(parts) != 3 {
		return v, fmt.Errorf("release: bad version %q", s)
	}
	nums := []*int{&v.Major, &v.Minor, &v.Patch}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (len(p) > 1 && p[0] == '0') {
			return v, fmt.Errorf("release: bad version %q", s)
		}
		*nums[i] = n
	}
	if hasPre {
		kind, num, ok := strings.Cut(pre, ".")
		n, err := strconv.Atoi(num)
		if !ok || (kind != "beta" && kind != "dev") || err != nil || n < 0 {
			return v, fmt.Errorf("release: bad pre-release in %q (want -beta.N or -dev.N)", s)
		}
		v.Pre, v.PreN = kind, n
	}
	return v, nil
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += fmt.Sprintf("-%s.%d", v.Pre, v.PreN)
	}
	return s
}

// Channel is the channel a version belongs to.
func (v Version) Channel() string {
	if v.Pre == "" {
		return "stable"
	}
	return v.Pre
}

var preRank = map[string]int{"dev": 0, "beta": 1, "": 2}

// Compare orders versions semver-style; for the same X.Y.Z: dev < beta < stable.
func (v Version) Compare(o Version) int {
	for _, d := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}, {preRank[v.Pre], preRank[o.Pre]}, {v.PreN, o.PreN}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Accepts reports whether a device on channel may install a release of
// version v: stable sees stable; beta sees beta+stable; dev sees everything.
func Accepts(channel string, v Version) bool {
	switch channel {
	case "dev":
		return true
	case "beta":
		return v.Pre != "dev"
	default:
		return v.Pre == ""
	}
}
