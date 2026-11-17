// Package update implements managed agent updates (M9, ADR-0010 D2–D5):
// channel-aware discovery of signed releases, user approval, staging with
// verification, an independent updater that switches versions atomically,
// health checks and automatic rollback. Never runs without an approval for the
// exact version and never uses privileges (user units only).
package update

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Layout is the managed install tree under ~/.local/share/deckbeacon:
//
//	versions/<X.Y.Z>/deckbeacon   one directory per installed version
//	bin/deckbeacon -> ../versions/<active>/deckbeacon   (the unit's ExecStart)
//	previous       -> versions/<last good>/deckbeacon
//
// Only the active and previous versions are kept.
type Layout struct{ Dir string }

var versionDir = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-(beta|dev)(\.[0-9]+)?)?$`)

func (l Layout) Bin() string                { return filepath.Join(l.Dir, "bin", "deckbeacon") }
func (l Layout) previousLink() string       { return filepath.Join(l.Dir, "previous") }
func (l Layout) VersionBin(v string) string { return filepath.Join(l.Dir, "versions", v, "deckbeacon") }
func (l Layout) stagingDir(v string) string { return filepath.Join(l.Dir, "versions", ".staging-"+v) }
func validVersionName(v string) bool        { return versionDir.MatchString(v) }
func (l Layout) linkTarget(v string) string { return filepath.Join("..", "versions", v, "deckbeacon") }
func (l Layout) prevTarget(v string) string { return filepath.Join("versions", v, "deckbeacon") }
func (l Layout) Managed() bool              { _, err := os.Readlink(l.Bin()); return err == nil }
func (l Layout) Has(v string) bool {
	st, err := os.Stat(l.VersionBin(v))
	return err == nil && st.Mode().IsRegular()
}
func (l Layout) versionOf(target string) string {
	return filepath.Base(filepath.Dir(target))
}

// Active returns the version bin/deckbeacon points at ("" if unmanaged).
func (l Layout) Active() string {
	t, err := os.Readlink(l.Bin())
	if err != nil {
		return ""
	}
	return l.versionOf(t)
}

// Previous returns the last-good version ("" if none).
func (l Layout) Previous() string {
	t, err := os.Readlink(l.previousLink())
	if err != nil {
		return ""
	}
	return l.versionOf(t)
}

// Adopt copies the currently running (unmanaged) binary into the layout as
// version v so that it can serve as the rollback target. The original file
// is left in place.
func (l Layout) Adopt(currentExe, v string) error {
	if !validVersionName(v) {
		return fmt.Errorf("update: cannot adopt version %q", v)
	}
	if l.Has(v) {
		return nil
	}
	return l.install(currentExe, v)
}

// install copies src to versions/v/deckbeacon atomically (staging + rename).
func (l Layout) install(src, v string) error {
	stage := l.stagingDir(v)
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(filepath.Join(stage, "deckbeacon"), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	final := filepath.Dir(l.VersionBin(v))
	_ = os.RemoveAll(final)
	return os.Rename(stage, final)
}

// symlinkAtomic points link at target via a temporary link and rename.
func symlinkAtomic(target, link string) error {
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	tmp := link + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

// Switch makes v active and records from as previous. Atomic per link.
func (l Layout) Switch(v, from string) error {
	if !l.Has(v) {
		return fmt.Errorf("update: version %s is not installed", v)
	}
	if from != "" && from != v && l.Has(from) {
		if err := symlinkAtomic(l.prevTarget(from), l.previousLink()); err != nil {
			return err
		}
	}
	return symlinkAtomic(l.linkTarget(v), l.Bin())
}

// Prune removes every version except keep... (active and previous).
func (l Layout) Prune(keep ...string) {
	entries, _ := os.ReadDir(filepath.Join(l.Dir, "versions"))
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".staging-") {
			_ = os.RemoveAll(filepath.Join(l.Dir, "versions", name))
			continue
		}
		kept := false
		for _, k := range keep {
			kept = kept || k == name
		}
		if !kept && validVersionName(name) {
			_ = os.RemoveAll(filepath.Join(l.Dir, "versions", name))
		}
	}
}

// ErrNotManaged means there is no previous version to roll back to.
var ErrNotManaged = errors.New("update: no previous version to roll back to")
