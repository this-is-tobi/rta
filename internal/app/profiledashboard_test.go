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
