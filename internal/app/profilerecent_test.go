package app

import (
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/recent"
)

const twoUnfencedProfiles = `
output: pretty
profiles:
  one:
    plugins:
      app:
        set:
          namespace: base-one
  two:
    plugins:
      app:
        set:
          namespace: base-two
`

// "you used this" is a statement about the environment the value was typed
// under: a namespace typed under `one` is offered under `one`, and not under
// `two` or with no profile at all, where it is one keystroke from being
// accepted against somewhere else.
func TestWhatWasTypedUnderOneProfileIsOfferedUnderThatProfileOnly(t *testing.T) {
	prior := remembered
	remembered = recent.Load
	t.Cleanup(func() { remembered = prior })

	reg := fenceCLIRegistry(t)
	run := session(t, reg)
	if err := os.WriteFile(config.Path(), []byte(twoUnfencedProfiles), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := run("app", "status", "--profile", "one", "--namespace", "seen-in-one"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}

	for _, tc := range []struct {
		line []string
		want bool
	}{
		{[]string{"--profile", "one"}, true},
		{[]string{"--profile", "two"}, false},
		{nil, false},
	} {
		out, errOut, err := run(append([]string{"__complete", "app", "status"}, append(tc.line, "--namespace", "")...)...)
		if err != nil {
			t.Fatalf("%v: %v %q", tc.line, err, errOut)
		}
		if got := strings.Contains(out, "seen-in-one\tyou used this"); got != tc.want {
			t.Errorf("%v: offered = %v, want %v\n%s", tc.line, got, tc.want, out)
		}
	}

	// And a run through no profile is the base configuration's own shortlist.
	if _, errOut, err := run("app", "status", "--namespace", "typed-at-base"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	out, _, err := run("__complete", "app", "status", "--namespace", "")
	if err != nil || !strings.Contains(out, "typed-at-base") || strings.Contains(out, "seen-in-one") {
		t.Errorf("the base configuration offers %q (%v)", out, err)
	}
}

// The switched-on environment records and offers under its own name, as a
// command that names it does.
func TestTheSwitchedOnEnvironmentHasTheShortlistOfItsName(t *testing.T) {
	prior := remembered
	remembered = recent.Load
	t.Cleanup(func() { remembered = prior })

	run := session(t, fenceCLIRegistry(t))
	if err := os.WriteFile(config.Path(), []byte(twoUnfencedProfiles), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := run("use", "one"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if _, errOut, err := run("app", "status", "--namespace", "typed-while-on"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if got := recent.Load().For("one", "app.status", "namespace"); len(got) != 1 || got[0] != "typed-while-on" {
		t.Errorf("the shortlist of one = %v", got)
	}
	if got := recent.Load().For("", "app.status", "namespace"); len(got) != 0 {
		t.Errorf("the base configuration was offered what was typed under one: %v", got)
	}
}
