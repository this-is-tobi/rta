package app

import (
	"os"
	"testing"
)

// A data directory of this binary's own, for the whole package.
//
// Running a capability now records what it was given, so a later completion
// can offer it back (internal/recent). Without this a test's fixture values
// land in the developer's own ~/.local/share/rta and come back as suggestions
// in their real shell — a dirty test, and a surprising thing to do to
// somebody's machine. Set for every test rather than in the helpers, because
// the rule is about the package and a helper is something a new test can
// forget to call; a test that wants its own directory still overrides it.
//
// COLUMNS is cleared for the same reason. Pretty output is shaped to it
// whenever it is set, a pipe included, and the commands these tests run
// render through termWidth — so a shell rc, an IDE terminal or `watch` that
// exports it wrapped every refusal and every explain page at that width,
// and assertions written for one unbroken line failed on that machine
// alone. A test that wants a width says so itself, with t.Setenv.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rta-app-tests")
	if err != nil {
		panic(err)
	}
	os.Setenv("RTA_DATA_DIR", dir)
	os.Unsetenv("COLUMNS")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
