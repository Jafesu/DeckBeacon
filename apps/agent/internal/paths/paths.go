// Package paths resolves the agent's XDG directories (spec 03).
package paths

import (
	"errors"
	"os"
	"path/filepath"
)

// Dirs are the agent's directories. State holds identity, trust and the durable
// store (all secrets live here, mode 0700).
type Dirs struct {
	Config  string
	State   string
	Runtime string
}

// Resolve uses DECKBEACON_STATE_DIR / DECKBEACON_CONFIG_DIR overrides (tests, dev),
// then XDG variables, then the XDG defaults.
func Resolve() (Dirs, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, err
	}
	pick := func(override, xdg, def string) string {
		if v := os.Getenv(override); v != "" {
			return v
		}
		if v := os.Getenv(xdg); v != "" && filepath.IsAbs(v) {
			return filepath.Join(v, "deckbeacon")
		}
		return filepath.Join(home, def, "deckbeacon")
	}
	d := Dirs{
		Config: pick("DECKBEACON_CONFIG_DIR", "XDG_CONFIG_HOME", ".config"),
		State:  pick("DECKBEACON_STATE_DIR", "XDG_STATE_HOME", filepath.Join(".local", "state")),
	}
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		d.Runtime = filepath.Join(v, "deckbeacon")
	} else {
		d.Runtime = filepath.Join(d.State, "run")
	}
	return d, nil
}

// Ensure creates all directories with owner-only permissions.
func (d Dirs) Ensure() error {
	var errs []error
	for _, p := range []string{d.Config, d.State, d.Runtime} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, os.Chmod(p, 0o700))
	}
	return errors.Join(errs...)
}
