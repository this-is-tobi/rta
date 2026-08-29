package app

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// A capability command inherits every persistent flag above it, and cobra
// resolves a subcommand's own flag first — so a plugin input named after one
// of them does not collide, it silently replaces it. plugin.reservedInputs is
// the host's declaration of which names that applies to, and it lives in the
// SDK, which cannot see cobra or this package. Nothing derives one from the
// other, so this test is the only thing that can keep them in step: it had one
// entry while the CLI had grown five more names, and --dry-run was one of them.
func TestTheCLIReservesEveryNameItOwns(t *testing.T) {
	reserved := plugin.ReservedInputs()

	root := NewRoot(testRegistry(t), "test")

	// Ask a real capability command what flags it has, rather than
	// reconstructing the answer from the tree. cmd.Flags() after
	// InitDefaultHelpFlag is exactly the set pflag will resolve a declared
	// input against — inherited persistent flags, cobra's own --help, and
	// nothing else.
	//
	// The first version of this walked every command's PersistentFlags and
	// then added root's *local* flags to cover --help. That over-collects:
	// --version is also local to root, and cobra gives it to root alone, so
	// the test would have demanded "version" be reserved to protect a flag no
	// capability can reach. Asking the command that actually parses the input
	// cannot make that mistake.
	// A capability command specifically, not any leaf: these are the commands
	// built from a plugin's declarations, and they are the only ones a
	// declared input can collide inside.
	reg := testRegistry(t)
	if len(reg.Capabilities()) == 0 {
		t.Fatal("the test registry has no capabilities, so this test checks nothing")
	}
	words := reg.Capabilities()[0].Words()
	leaf, _, err := root.Find(words)
	if err != nil || leaf == root {
		t.Fatalf("cannot reach the capability command %v: %v", words, err)
	}
	leaf.InitDefaultHelpFlag()

	leaf.Flags().VisitAll(func(f *pflag.Flag) {
		if slices.Contains(reserved, f.Name) {
			return
		}
		t.Errorf("%s can be given --%s and nothing reserves that name: a plugin declaring an "+
			"input called %q silently takes the flag over, because cobra resolves a "+
			"command's own flag before an inherited one. Add it to plugin.reservedInputs "+
			"with a reason.", leaf.CommandPath(), f.Name, f.Name)
	})

	// The reverse direction, so the list cannot go stale either: a name
	// reserved for a flag that no longer exists is a name refused for nothing.
	// "detail" is exempt — attach() adds it only to capabilities that declare
	// Detailed, which this leaf may not be.
	for _, name := range reserved {
		if name == "detail" {
			continue
		}
		if leaf.Flags().Lookup(name) == nil {
			t.Errorf("%q is reserved but %s has no flag by that name; either it is stale, "+
				"or the flag it protects was renamed", name, leaf.CommandPath())
		}
	}
}

// A plugin's namespace becomes a top-level command, so the same defect exists
// one level up from the flags: `rta doctor` against a plugin named "doctor"
// prints the plugin's usage and exits 0, having run none of the checks — the
// command most likely to reveal a hostile plugin is one a hostile plugin can
// switch off. RegisterFrom already refuses a namespace another *plugin* holds;
// rta's own commands are not plugins and nothing protected them.
func TestTheCLIReservesEveryTopLevelCommandItOwns(t *testing.T) {
	reserved := plugin.ReservedNamespaces()

	reg := testRegistry(t)
	root := NewRoot(reg, "test")
	// Both are lazy, like the help flag: cobra adds them when the command
	// runs, so a tree inspected before that has neither.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

	namespaces := map[string]bool{}
	for _, p := range reg.Plugins() {
		namespaces[p.Name] = true
	}

	owned := map[string]bool{}
	for _, c := range root.Commands() {
		name := c.Name()
		if namespaces[name] {
			continue // a plugin's own command, which is the point of it
		}
		if _, yields := c.Annotations[yieldsToPlugin]; yields {
			// A command that steps aside for a plugin of the same name
			// cannot be masked by one: being replaced is what it is for.
			// See yieldsToPlugin — today only the `rta ai` explainer in a
			// build without the AI engine.
			continue
		}
		owned[name] = true
		if !slices.Contains(reserved, name) {
			t.Errorf("rta owns the command %q and nothing reserves it: a plugin taking that "+
				"namespace replaces it, and `rta %s` then runs the plugin instead. "+
				"Add it to plugin.reservedNamespaces with a reason.", name, name)
		}
	}
	// And the reverse, so the list cannot outlive the commands it protects.
	for _, name := range reserved {
		if !owned[name] {
			t.Errorf("%q is reserved but rta has no top-level command by that name; "+
				"either it is stale, or the command was renamed", name)
		}
	}
}

// The input reproduction, kept as the regression: with a plugin input named
// "dry-run", `rta acme wipe --yes --dry-run` used to exit 0, print success,
// and perform the wipe. It is refused at registration now, which is where a
// declaration error belongs — before any surface has been built from it.
func TestAReservedInputNameIsRefusedAtRegistration(t *testing.T) {
	for _, name := range plugin.ReservedInputs() {
		t.Run(name, func(t *testing.T) {
			reg := registry.New()
			err := reg.Register(plugin.Plugin{
				Name: "acme", Summary: "acme", Version: "1",
				Capabilities: []plugin.Capability{{
					ID: "acme.wipe", Summary: "wipe everything", Safety: plugin.Destructive,
					Inputs: []plugin.Field{{Name: name, Type: plugin.Bool, Help: "an ordinary input"}},
					Run: func(context.Context, plugin.Request) (view.View, error) {
						return view.Text{Body: "WIPED"}, nil
					},
				}},
			})
			if err == nil {
				t.Fatalf("an input named %q registered; it shadows the host's own --%s", name, name)
			}
			// The author has to be able to act on it, so the message names the
			// input and says why it is spoken for.
			for _, want := range []string{name, "reserved"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// The namespace half of the same regression.
func TestAReservedNamespaceIsRefusedAtRegistration(t *testing.T) {
	for _, ns := range plugin.ReservedNamespaces() {
		t.Run(ns, func(t *testing.T) {
			reg := registry.New()
			err := reg.Register(plugin.Plugin{
				Name: ns, Summary: "hostile", Version: "1",
				Capabilities: []plugin.Capability{{
					ID: ns + ".ping", Summary: "ping", Safety: plugin.Read,
					Run: func(context.Context, plugin.Request) (view.View, error) {
						return view.Text{Body: "PLUGIN RAN"}, nil
					},
				}},
			})
			if err == nil {
				t.Fatalf("a plugin registered as %q; it replaces rta's own `rta %s`", ns, ns)
			}
			if !strings.Contains(err.Error(), "reserved") {
				t.Errorf("error %q does not say the name is reserved", err)
			}
		})
	}
}
