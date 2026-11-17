package release

import "crypto/ed25519"

// trustedKeys are the base64 Ed25519 public keys release manifests must be
// signed with. Rotation: ship a release that lists the old and the new key,
// then drop the old one. The private key never enters this repository; the
// owner keeps it and stores it as the DECKBEACON_RELEASE_KEY Actions secret.
var trustedKeys = []string{
	"Kom6NMhiRPDS7TajzChEUTAMoEUf/kbZj/k4Ut3+cEc=", // owner release key #1 (2026-11-17)
}

// TrustedKeys returns the embedded keys (invalid entries are skipped).
func TrustedKeys() []ed25519.PublicKey {
	var out []ed25519.PublicKey
	for _, s := range trustedKeys {
		if k, err := ParsePublicKey(s); err == nil {
			out = append(out, k)
		}
	}
	return out
}
