package steam

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVDF(t *testing.T) {
	n, err := ParseVDF(`// comment
"AppState"
{
	"appid"		"1091500"
	"name"		"Cyberpunk \"2077\""
	"StateFlags"		"4"
	"InstalledDepots"
	{
		"1091501" { "manifest" "123" "size" "999" }
	}
	unquoted value
	"cond" "x" [$WIN32]
}`)
	if err != nil {
		t.Fatal(err)
	}
	a := n.Get("appstate")
	if a.Str("APPID") != "1091500" || a.Str("name") != `Cyberpunk "2077"` || a.Str("stateflags") != "4" {
		t.Fatalf("parsed %+v", a.Children)
	}
	if a.Str("installeddepots", "1091501", "size") != "999" || a.Str("unquoted") != "value" || a.Str("cond") != "x" {
		t.Fatal("nested/unquoted/conditional handling wrong")
	}
	if a.Order[0] != "appid" {
		t.Fatalf("order %v", a.Order)
	}
}

func TestParseVDFErrors(t *testing.T) {
	for _, bad := range []string{`"a" { "b" "c"`, `"a" "b`, `}`, `"a"`, strings.Repeat(`"a"{`, 40) + strings.Repeat("}", 40)} {
		if _, err := ParseVDF(bad); err == nil {
			t.Errorf("accepted %q", bad[:min(len(bad), 30)])
		}
	}
}

func TestReadVDFRefusesCredentialFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"config.vdf", "LocalConfig.vdf", "loginusers.vdf", "ssfn123456"} {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte(`"x" "CANARY-SECRET"`), 0o600)
		if _, err := ReadVDF(p); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s: err %v", name, err)
		}
	}
}
