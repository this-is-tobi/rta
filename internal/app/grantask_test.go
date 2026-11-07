package app

import (
	"context"
	"slices"
	"strings"
	"testing"

	grantbuiltin "github.com/this-is-tobi/rta/builtin/grant"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// wideRegistry is the grant built-in beside a plugin a whole-plugin grant on
// which is worth a question: one capability that reveals what it stores, one
// that destroys, one that only reads.
func wideRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	if err := reg.Register(grantbuiltin.Plugin(reg.Capabilities, reg.Artifact)); err != nil {
		t.Fatal(err)
	}
	ran := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ran"}, nil }
	if err := reg.Register(plugin.Plugin{
		Name: "vault", Summary: "a store",
		Capabilities: []plugin.Capability{
			{ID: "vault.get", Summary: "reveal a value", Safety: plugin.Write, Reveals: true, NeedsGrant: true,
				Scope: "key", Inputs: []plugin.Field{{Name: "key", Type: plugin.String, Positional: true}}, Run: ran},
			{ID: "vault.rm", Summary: "remove a value", Safety: plugin.Destructive, Run: ran},
			{ID: "vault.list", Summary: "list values", Safety: plugin.Read, Run: ran},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// `rta grant allow vault` at a terminal shows what the grant covers and asks
// before it is issued. A no exits 3, the destructive prompt's code, which the
// built-in spells out because it cannot import this package: this is the test
// that holds the two together.
func TestAWholePluginGrantIsAskedAboutAtATerminal(t *testing.T) {
	run := session(t, wideRegistry(t))
	// The no first: what it leaves is checked against an empty store.
	for _, want := range []struct {
		answer string
		exit   int
		held   []string
	}{{"n\n", 3, nil}, {"y\n", 0, []string{"vault@@claude"}}} {
		answer := want.answer
		asked := askedOf(t, answer, nil)
		_, errOut, err := run("grant", "allow", "vault", "--agent", "claude")
		if got := ExitCode(err); got != want.exit {
			t.Errorf("answered %q: exit %d, want %d (%v)", answer, got, want.exit, err)
		}
		if !slices.Equal(*asked, []string{"Go ahead? [y/N] "}) {
			t.Errorf("answered %q: asked %q", answer, *asked)
		}
		for _, line := range []string{"would let claude call vault", "vault.rm", "vault.get"} {
			if !strings.Contains(errOut, line) {
				t.Errorf("answered %q: the card does not say %q:\n%s", answer, line, errOut)
			}
		}
		if got := heldTargets(t); !slices.Equal(got, want.held) {
			t.Errorf("answered %q: held %v, want %v", answer, got, want.held)
		}
	}
}

// Where nobody is typing, or the output is a program's, the grant is issued as it
// always was: the question is for a person and never a gate.
func TestAWholePluginGrantIsNotAskedWhereNobodyTypes(t *testing.T) {
	for name, tc := range map[string]struct {
		args     []string
		terminal bool
	}{
		"with --yes":   {[]string{"--yes"}, true},
		"with -o json": {[]string{"-o", "json"}, true},
		"in a script":  {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			run := session(t, wideRegistry(t))
			asked := askedOf(t, "n\n", nil)
			if !tc.terminal {
				confirmTerminal = func() bool { return false }
			}
			if _, _, err := run(append([]string{"grant", "allow", "vault", "--agent", "claude"}, tc.args...)...); err != nil {
				t.Fatal(err)
			}
			if len(*asked) != 0 {
				t.Errorf("asked %q", *asked)
			}
			if got := heldTargets(t); !slices.Equal(got, []string{"vault@@claude"}) {
				t.Errorf("held %v", got)
			}
		})
	}
}
