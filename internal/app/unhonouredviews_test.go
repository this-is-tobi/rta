package app

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

// A config file found in the working directory arranges no screen, because a
// cloned repository must not. The commands that write already said so, and the
// screen drew the automatic dashboard; what was left was the listing of its
// views as if they were screens to choose from, and a check that named the
// profile it ignored and said nothing of the dashboard block beside it.
func TestAnUnhonouredFilesViewsAreNotOfferedAsScreens(t *testing.T) {
	reg := setRegistry(t)
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("RTA_CONFIG", "")
	t.Chdir(t.TempDir())
	if config.TrustedPath() {
		t.Skip("this machine has a config directory")
	}
	const file = "dashboard:\n  views:\n    ops:\n      add:\n      - id: db.status\n"
	if err := os.WriteFile(".rta.yaml", []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	SetInstalled(reg)
	t.Cleanup(func() { SetInstalled(nil) })
	run := func(args ...string) string {
		root := NewRoot(reg, "test")
		var out, errOut bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs(args)
		_ = root.Execute()
		return out.String() + errOut.String()
	}

	if out := run("dashboard", "views", "-o", "json"); !strings.Contains(out, "core.dashboard.untrusted") ||
		!strings.Contains(out, "none of them is drawn") {
		t.Errorf("the views of a file that arranges nothing are listed without saying so:\n%s", out)
	}
	if out := run("config", "check", "-o", "json"); !strings.Contains(out, "dashboard") ||
		!strings.Contains(out, "not honoured") {
		t.Errorf("config check does not name the dashboard it ignores:\n%s", out)
	}
	if got := fileText(t, ".rta.yaml"); got != file {
		t.Errorf("a command changed the file:\n%s", got)
	}
}
