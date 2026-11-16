package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeSys struct {
	calls   []string
	enabled bool
	root    string
	user    string
	failOn  string
}

func (f *fakeSys) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if f.failOn != "" && strings.Contains(call, f.failOn) {
		return nil, errors.New("Access denied")
	}
	switch {
	case strings.HasSuffix(call, "is-enabled "+UnitName):
		if f.enabled {
			return []byte("enabled\n"), nil
		}
		return []byte("disabled\n"), errors.New("exit status 1")
	case strings.Contains(call, "enable --now"):
		f.enabled = true
	case strings.Contains(call, "disable --now"):
		f.enabled = false
	case call == "loginctl enable-linger":
		p := filepath.Join(f.root, "var/lib/systemd/linger", f.user)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, nil, 0o644)
	case call == "loginctl disable-linger":
		_ = os.Remove(filepath.Join(f.root, "var/lib/systemd/linger", f.user))
	}
	return nil, nil
}

func setup(t *testing.T) (*fakeSys, Manager) {
	root := t.TempDir()
	f := &fakeSys{root: root, user: "deck"}
	return f, Manager{Root: root, UnitDir: filepath.Join(root, "home/deck/.config/systemd/user"), StateDir: filepath.Join(root, "state"),
		Exe: "/home/deck/.local/share/deckbeacon/bin/deckbeacon", User: "deck", Run: f.run}
}

func TestInstallIdempotentAndUninstall(t *testing.T) {
	f, m := setup(t)
	ctx := context.Background()
	ch, err := m.Install(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl --user daemon-reload",
		"systemctl --user is-enabled deckbeacon.service",
		"systemctl --user enable --now deckbeacon.service",
		"loginctl enable-linger",
	}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
	if len(ch) != 3 || !m.Lingering() {
		t.Fatalf("changes %v linger %v", ch, m.Lingering())
	}
	unit, _ := os.ReadFile(filepath.Join(m.UnitDir, UnitName))
	if !strings.Contains(string(unit), `ExecStart="/home/deck/.local/share/deckbeacon/bin/deckbeacon" run`) {
		t.Fatalf("unit:\n%s", unit)
	}

	f.calls = nil
	ch, err = m.Install(ctx, true)
	if err != nil || len(ch) != 0 {
		t.Fatalf("second install changed %v (%v)", ch, err)
	}
	if strings.Join(f.calls, "|") != "systemctl --user is-enabled deckbeacon.service" {
		t.Fatalf("second install calls %v", f.calls)
	}
	st := m.Status(ctx)
	if !st.UnitInstalled || !st.UnitCurrent || !st.StartAtBoot || !st.LingerOwned {
		t.Fatalf("status %+v", st)
	}

	f.calls = nil
	if _, err := m.Uninstall(ctx); err != nil {
		t.Fatal(err)
	}
	if m.Lingering() || f.enabled {
		t.Fatal("uninstall left linger or unit enabled")
	}
	if _, err := os.Stat(filepath.Join(m.UnitDir, UnitName)); !os.IsNotExist(err) {
		t.Fatal("unit file not removed")
	}
	for _, c := range f.calls {
		if strings.Contains(c, "sudo") || strings.Contains(c, "pkexec") {
			t.Fatalf("privileged call %q", c)
		}
	}
}

func TestDoesNotDisableForeignLinger(t *testing.T) {
	f, m := setup(t)
	_ = os.MkdirAll(filepath.Join(m.Root, "var/lib/systemd/linger"), 0o755)
	_ = os.WriteFile(filepath.Join(m.Root, "var/lib/systemd/linger/deck"), nil, 0o644) // enabled by someone else
	ctx := context.Background()
	if _, err := m.Install(ctx, true); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "loginctl") {
			t.Fatalf("unexpected %q", c)
		}
	}
	if _, err := m.Uninstall(ctx); err != nil {
		t.Fatal(err)
	}
	if !m.Lingering() {
		t.Fatal("uninstall removed lingering it did not enable")
	}
	if _, err := m.SetStartAtBoot(ctx, false, false); err == nil {
		t.Fatal("disabling foreign linger without --force must fail")
	}
	if _, err := m.SetStartAtBoot(ctx, false, true); err != nil || m.Lingering() {
		t.Fatalf("force disable: %v linger=%v", err, m.Lingering())
	}
}

func TestLingerDeniedIsReported(t *testing.T) {
	f, m := setup(t)
	f.failOn = "enable-linger"
	_, err := m.Install(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "SteamOS may restrict self-linger") {
		t.Fatalf("err %v", err)
	}
	if !f.enabled {
		t.Fatal("unit should still be enabled when only lingering failed")
	}
}

func TestRenderEscaping(t *testing.T) {
	u, err := Render("/home/deck/100% apps/deckbeacon")
	if err != nil || !strings.Contains(u, `ExecStart="/home/deck/100%% apps/deckbeacon" run`) {
		t.Fatalf("%v\n%s", err, u)
	}
	for _, bad := range []string{"relative/path", "/x\"y", "/x\ny"} {
		if _, err := Render(bad); !errors.Is(err, ErrBadPath) {
			t.Errorf("Render(%q) = %v", bad, err)
		}
	}
}

func TestTemplateMatchesPackagingFile(t *testing.T) {
	ref, err := os.ReadFile(filepath.Join("..", "..", "packaging", "systemd", UnitName))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := Render("/x")
	strip := func(s string) string {
		var out []string
		for _, l := range strings.Split(s, "\n") {
			if !strings.HasPrefix(l, "ExecStart=") {
				out = append(out, l)
			}
		}
		return strings.Join(out, "\n")
	}
	if strip(string(ref)) != strip(got) {
		t.Fatalf("packaging/systemd/%s differs from the embedded template (except ExecStart)", UnitName)
	}
}
