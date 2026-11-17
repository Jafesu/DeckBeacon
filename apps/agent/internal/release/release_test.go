package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func keypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func manifest(t *testing.T, version string) ([]byte, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "deckbeacon")
	_ = os.WriteFile(bin, []byte("binary for "+version), 0o755)
	m, err := BuildManifest(version, 1, "0.2.0", "https://github.com/Jafesu/DeckBeacon/releases/tag/agent/v"+version, map[string]string{AssetName("amd64"): bin}, time.Unix(1794890000, 0))
	if err != nil {
		t.Fatal(err)
	}
	return m, bin
}

func TestSignVerifyAndAssets(t *testing.T) {
	pub, priv := keypair(t)
	other, _ := keypair(t)
	m, bin := manifest(t, "0.3.0")
	sig := Sign(m, priv)

	got, err := Verify(m, sig, []ed25519.PublicKey{other, pub}) // rotation list
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "0.3.0" || got.Channel != "stable" || got.MinIntegration != "0.2.0" {
		t.Fatalf("%+v", got)
	}
	f, _ := os.Open(bin)
	defer f.Close()
	if err := got.CheckAsset(AssetName("amd64"), f); err != nil {
		t.Fatal(err)
	}
	if err := got.CheckAsset(AssetName("amd64"), strings.NewReader("tampered binary!!")); err == nil {
		t.Fatal("tampered binary accepted")
	}
	if err := got.CheckAsset(AssetName("arm64"), f); err == nil {
		t.Fatal("missing asset accepted")
	}
}

func TestVerifyRefusals(t *testing.T) {
	pub, priv := keypair(t)
	other, otherPriv := keypair(t)
	m, _ := manifest(t, "0.3.0")
	sig := Sign(m, priv)
	tampered := bytes.Replace(m, []byte(`"0.2.0"`), []byte(`"0.0.1"`), 1)
	cases := map[string]func() error{
		"tampered manifest": func() error { _, err := Verify(tampered, sig, []ed25519.PublicKey{pub}); return err },
		"unknown key":       func() error { _, err := Verify(m, sig, []ed25519.PublicKey{other}); return err },
		"wrong signer":      func() error { _, err := Verify(m, Sign(m, otherPriv), []ed25519.PublicKey{pub}); return err },
		"no signature":      func() error { _, err := Verify(m, nil, []ed25519.PublicKey{pub}); return err },
		"garbage signature": func() error { _, err := Verify(m, []byte("!!!"), []ed25519.PublicKey{pub}); return err },
		"no trusted keys":   func() error { _, err := Verify(m, sig, nil); return err },
	}
	for name, f := range cases {
		if f() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A validly signed but inconsistent manifest is still refused.
	bad := bytes.Replace(m, []byte(`"channel": "stable"`), []byte(`"channel": "beta"`), 1)
	if _, err := Verify(bad, Sign(bad, priv), []ed25519.PublicKey{pub}); err == nil {
		t.Fatal("channel/version mismatch accepted")
	}
}

func TestVersionsAndChannels(t *testing.T) {
	order := []string{"0.2.9", "0.3.0-dev.1", "0.3.0-dev.2", "0.3.0-beta.1", "0.3.0", "0.3.1-dev.0", "1.0.0"}
	for i := 1; i < len(order); i++ {
		a, _ := ParseVersion(order[i-1])
		b, _ := ParseVersion(order[i])
		if a.Compare(b) >= 0 || b.Compare(a) <= 0 {
			t.Errorf("%s should be < %s", order[i-1], order[i])
		}
	}
	for _, bad := range []string{"", "1.2", "1.2.3.4", "01.2.3", "1.2.3-rc.1", "1.2.3-beta", "1.2.3-dev.x", "0.0.0-dev"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	v := func(s string) Version { x, _ := ParseVersion(s); return x }
	if !Accepts("stable", v("1.0.0")) || Accepts("stable", v("1.0.0-beta.1")) || !Accepts("beta", v("1.0.0-beta.1")) || Accepts("beta", v("1.0.0-dev.1")) || !Accepts("dev", v("1.0.0-dev.1")) {
		t.Fatal("channel filtering")
	}
}

// The owner may generate the key with openssl instead of the agent.
func TestOpenSSLKeysInterop(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl not installed")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "k.pem")
	if out, err := exec.Command("openssl", "genpkey", "-algorithm", "ed25519", "-out", key).CombinedOutput(); err != nil {
		t.Skipf("openssl lacks ed25519: %s", out)
	}
	pubPEM, err := exec.Command("openssl", "pkey", "-in", key, "-pubout").Output()
	if err != nil {
		t.Fatal(err)
	}
	pemBytes, _ := os.ReadFile(key)
	priv, err := LoadPrivateKey(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ParsePublicKey(string(pubPEM))
	if err != nil {
		t.Fatal(err)
	}
	m, _ := manifest(t, "0.3.0-beta.1")
	if _, err := Verify(m, Sign(m, priv), []ed25519.PublicKey{pub}); err != nil {
		t.Fatal(err)
	}
	// The base64 form round-trips (what gets embedded).
	again, _ := ParsePublicKey(EncodePublicKey(pub))
	if !again.Equal(pub) {
		t.Fatal("base64 round trip")
	}
}

func TestEmbeddedOwnerKey(t *testing.T) {
	keys := TrustedKeys()
	if len(keys) != 1 || EncodePublicKey(keys[0]) != "Kom6NMhiRPDS7TajzChEUTAMoEUf/kbZj/k4Ut3+cEc=" {
		t.Fatalf("embedded keys %v", keys)
	}
	// The PEM the owner shared decodes to the same key.
	pem := "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAKom6NMhiRPDS7TajzChEUTAMoEUf/kbZj/k4Ut3+cEc=\n-----END PUBLIC KEY-----\n"
	k, err := ParsePublicKey(pem)
	if err != nil || !k.Equal(keys[0]) {
		t.Fatalf("PEM mismatch: %v", err)
	}
}
