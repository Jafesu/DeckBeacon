package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/fsutil"
	"github.com/jafesu/deckbeacon/apps/agent/internal/release"
)

// Release implements `deckbeacon release …` (ADR-0010 D1): tools for the
// release workflow and the owner. None of them touch the device's agent state.
//
//	keygen  --out key.pem               new Ed25519 key (owner's machine only)
//	manifest --version V --min-integration X [--min-protocol 1] [--notes URL] name=path...
//	sign    --key key.pem --in release.json --out release.json.sig
//	verify  --pub KEY --in release.json --sig release.json.sig [name=path...]
func Release(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: deckbeacon release keygen|manifest|sign|verify ...")
	}
	fs := flag.NewFlagSet("release "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	switch args[0] {
	case "keygen":
		out := fs.String("out", "deckbeacon-release.pem", "private key file (keep it secret)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		pemBytes, err := release.EncodePrivateKey(priv)
		if err != nil {
			return err
		}
		if _, err := os.Stat(*out); err == nil {
			return fmt.Errorf("%s exists; refusing to overwrite a key", *out)
		}
		if err := fsutil.WriteFileAtomic(*out, pemBytes, 0o600); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Private key written to %s (mode 0600). Store it as the DECKBEACON_RELEASE_KEY Actions secret and keep a backup offline.\n", *out)
		fmt.Fprintf(stdout, "Public key (safe to share; it gets embedded in the agent):\n%s\n", release.EncodePublicKey(pub))
		return nil
	case "manifest":
		version := fs.String("version", "", "release version (X.Y.Z, X.Y.Z-beta.N, X.Y.Z-dev.N)")
		minProto := fs.Int("min-protocol", 1, "minimum protocol major")
		minInt := fs.String("min-integration", "", "minimum HA integration version")
		notes := fs.String("notes", "", "release notes URL")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		files, err := namePaths(fs.Args())
		if err != nil {
			return err
		}
		m, err := release.BuildManifest(*version, *minProto, *minInt, *notes, files, time.Now())
		if err != nil {
			return err
		}
		_, err = stdout.Write(m)
		return err
	case "sign":
		keyPath := fs.String("key", "", "private key PEM ('-' or empty: $DECKBEACON_RELEASE_KEY)")
		in := fs.String("in", "release.json", "manifest")
		out := fs.String("out", "release.json.sig", "signature file")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		var pemBytes []byte
		if *keyPath == "" || *keyPath == "-" {
			pemBytes = []byte(os.Getenv("DECKBEACON_RELEASE_KEY"))
		} else if b, err := os.ReadFile(*keyPath); err == nil {
			pemBytes = b
		}
		if len(pemBytes) == 0 {
			return errors.New("no signing key (refusing to publish unsigned)")
		}
		key, err := release.LoadPrivateKey(pemBytes)
		if err != nil {
			return err
		}
		m, err := os.ReadFile(*in)
		if err != nil {
			return err
		}
		return os.WriteFile(*out, release.Sign(m, key), 0o644)
	case "verify":
		pubs := fs.String("pub", "", "public key(s), comma-separated (default: the keys built into this agent)")
		in := fs.String("in", "release.json", "manifest")
		sigPath := fs.String("sig", "release.json.sig", "signature")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		keys := release.TrustedKeys()
		if *pubs != "" {
			keys = nil
			for _, s := range strings.Split(*pubs, ",") {
				k, err := release.ParsePublicKey(s)
				if err != nil {
					return err
				}
				keys = append(keys, k)
			}
		}
		m, err := os.ReadFile(*in)
		if err != nil {
			return err
		}
		sig, err := os.ReadFile(*sigPath)
		if err != nil {
			return err
		}
		man, err := release.Verify(m, sig, keys)
		if err != nil {
			return err
		}
		files, err := namePaths(fs.Args())
		if err != nil {
			return err
		}
		for name, path := range files {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			err = man.CheckAsset(name, f)
			f.Close()
			if err != nil {
				return err
			}
		}
		fmt.Fprintf(stdout, "OK: %s (%s), %d asset(s) listed, %d checked\n", man.Version, man.Channel, len(man.Assets), len(files))
		return nil
	}
	return fmt.Errorf("unknown release command %q", args[0])
}

func namePaths(args []string) (map[string]string, error) {
	out := map[string]string{}
	for _, a := range args {
		name, path, ok := strings.Cut(a, "=")
		if !ok || name == "" || path == "" {
			return nil, fmt.Errorf("expected name=path, got %q", a)
		}
		out[name] = path
	}
	return out, nil
}
