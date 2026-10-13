package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

// A profile can write its screen in its own entry, and the commands that read
// and write views treat that block as the view it is: one named after the
// profile.

// stagingEntry is an environment whole, with the screen it shows written in
// the entry, as a person puts it in a drop-in.
const stagingEntry = `profiles:
  staging:
    note: shared
    plugins:
      db:
        set:
          host: staging.internal
    dashboard:
      # the database first
      add:
      - id: db.status
        with:
          dbname: orders
      hidden:
      - sys.overview
      order:
      - db.status
`

// dropIn writes a drop-in beside the config file a session runs against and
// returns its path.
func dropIn(t *testing.T, name, text string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(config.Path()), "config.d", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// --dashboard names a view, and naming one over a block would throw the block
// away, which no flag spelling should do without being asked: `none` included,
// since it means "back to the dashboard: block" and not "forget my tiles".
func TestProfileSetDashboardRefusesAProfileWhoseScreenIsItsOwn(t *testing.T) {
	run := session(t, setRegistry(t))
	path := dropIn(t, "20-staging.yaml", stagingEntry)
	if _, errOut, err := run("dashboard", "add", "db.status", "--view", "ops", "--set", "dbname=shop"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	for _, named := range []string{"ops", "none", "default", "ghost"} {
		_, errOut, err := run("profile", "set", "staging", "--dashboard", named)
		if err == nil || !strings.Contains(errOut, "core.profile.dashboard.inline") ||
			!strings.Contains(errOut, "1 tile") || !strings.Contains(errOut, "--view staging") {
			t.Errorf("--dashboard %s: %v\n%s", named, err, errOut)
		}
	}
	if got := fileText(t, path); got != stagingEntry {
		t.Errorf("a refusal wrote to the drop-in:\n%s", got)
	}
}

// What the flag does not mention is left as it was, the block included.
func TestProfileSetKeepsTheBlockWhenItIsAboutSomethingElse(t *testing.T) {
	run := session(t, setRegistry(t))
	path := dropIn(t, "20-staging.yaml", stagingEntry)
	if _, errOut, err := run("profile", "set", "staging", "--note", "the shared one"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	p := loadedConfig(t).Profiles["staging"]
	if p.Note != "the shared one" || p.Dashboard.Inline == nil || len(p.Dashboard.Inline.Add) != 1 {
		t.Errorf("after the write: %+v", p)
	}
	text := fileText(t, path)
	for _, want := range []string{"# the database first", "dbname: orders", "- sys.overview"} {
		if !strings.Contains(text, want) {
			t.Errorf("the drop-in lost %q:\n%s", want, text)
		}
	}
}

// The screen of a profile is listed with the views: what selects it, the file
// it is in — the drop-in that holds the entry — and, once the environment is
// switched on, that it is the one drawn and why.
func TestViewsListsAProfilesOwnScreenWithItsFileAndWhyItIsDrawn(t *testing.T) {
	run := session(t, setRegistry(t))
	dropIn(t, "20-staging.yaml", stagingEntry)
	out, errOut, err := run("dashboard", "views", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	for _, want := range []string{`"staging"`, "staging (its own)", filepath.Join("config.d", "20-staging.yaml")} {
		if !strings.Contains(out, want) {
			t.Errorf("views does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "is switched on") {
		t.Errorf("a view is drawn with nothing switched on:\n%s", out)
	}
	if _, errOut, err := run("use", "staging"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if out, _, _ = run("dashboard", "views", "-o", "json"); !strings.Contains(out, "yes — staging is switched on") {
		t.Errorf("the drawn view does not say why:\n%s", out)
	}
}

// Switching the environment on is what makes its screen the one every
// dashboard command acts on and draws, and switching off puts the
// dashboard: block back.
func TestTheDashboardCommandsDrawAProfilesOwnScreenWhileItIsOn(t *testing.T) {
	run := session(t, setRegistry(t))
	dropIn(t, "20-staging.yaml", stagingEntry)
	has := func(args ...string) bool {
		t.Helper()
		out, errOut, err := run(append(args, "-o", "json")...)
		if err != nil {
			t.Fatalf("%v: %v %q", args, err, errOut)
		}
		return strings.Contains(out, "orders")
	}
	if has("dashboard", "list") {
		t.Fatal("the dashboard: block draws a tile only staging's screen adds")
	}
	if !has("dashboard", "list", "--view", "staging") {
		t.Error("--view staging does not draw the screen written in the entry")
	}
	if _, errOut, err := run("use", "staging"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !has("dashboard", "list") {
		t.Error("with staging switched on, the screen it states is not the one listed")
	}
	if has("dashboard", "list", "--view", "default") {
		t.Error("--view default does not name the dashboard: block while staging is on")
	}
	if _, errOut, err := run("use", "--off"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if has("dashboard", "list") {
		t.Error("the screen stayed after the environment was switched off")
	}
}

// A profile's own screen is drawn while that environment is switched on, and a
// receipt that told a person to select it with `profile set --dashboard`, which
// that very profile refuses, would send them to a command that throws it away.
func TestAddToAProfilesOwnScreenSaysWhenItIsDrawn(t *testing.T) {
	run := session(t, setRegistry(t))
	dropIn(t, "20-staging.yaml", stagingEntry)
	out, errOut, err := run("dashboard", "add", "db.status", "--profile", "staging", "--view", "staging")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "written in profile staging's own entry") || !strings.Contains(out, "`rta use staging`") ||
		strings.Contains(out, "--dashboard") {
		t.Errorf("the receipt does not say when the screen is drawn:\n%s", out)
	}
}

func TestShellCompletionOffersAProfilesOwnScreenAsAView(t *testing.T) {
	run := session(t, setRegistry(t))
	dropIn(t, "20-staging.yaml", stagingEntry)
	out, _, err := run("__complete", "dashboard", "list", "--view", "")
	if err != nil || !strings.Contains(out, "staging\t1 tile, written in profile staging") {
		t.Errorf("--view completes to:\n%s (%v)", out, err)
	}
}
