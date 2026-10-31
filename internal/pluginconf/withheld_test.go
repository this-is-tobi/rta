package pluginconf

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

// A section of the operator's own file that cannot be applied withholds the
// plugin: a stale pin after an upgrade, a heading with no pin or one too short
// to trust, and a pin on a built-in. Each is the operator having said where
// the plugin goes, and a call run without it goes somewhere else.
func TestASectionThatCannotApplyWithholdsItsPlugin(t *testing.T) {
	for _, tc := range []struct {
		name, heading, ns, hint string
	}{
		{"a stale pin", "pg@0000000000", "pg", "`rta config repin --plugin pg`"},
		{"no pin", "pg", "pg", "`rta config repin --plugin pg`"},
		{"a pin too short", "pg@1a2b", "pg", "`rta config repin --plugin pg`"},
		{"a pin on a built-in", "sys@1a2b3c4d", "sys", "write it as `sys:`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := Resolve(trusted(t, config.Config{Plugins: map[string]map[string]any{
				tc.heading: {"host": "db.internal"},
			}}), installed)
			p, withheld := r.Withheld(tc.ns)
			if !withheld {
				t.Fatalf("%s is not withheld", tc.heading)
			}
			verr := p.Refusal()
			if verr.Code != "core.config.unapplied" || !strings.Contains(verr.Message, "plugins."+tc.heading) {
				t.Errorf("the refusal does not name the section: %v", verr)
			}
			if !strings.Contains(verr.Hint, tc.hint) {
				t.Errorf("the refusal does not say how to fix it (%s): %q", tc.hint, verr.Hint)
			}
		})
	}
}

// Not withheld: a plugin whose section applied, beside the old pin an upgrade
// left behind it; a plugin with no section at all; and every section of a file
// read from the working directory, which a planted file could otherwise use to
// stop a plugin from running.
func TestOnlyAnOperatorsUnappliedSectionWithholds(t *testing.T) {
	r, problems := Resolve(trusted(t, config.Config{Plugins: map[string]map[string]any{
		"pg@0000000000":     {"host": "old.internal"},
		"pg@" + digest[:12]: {"host": "db.internal"},
	}}), installed)
	if _, withheld := r.Withheld("pg"); withheld {
		t.Error("a plugin whose section applied was withheld for the old one beside it")
	}
	if len(problems) != 1 {
		t.Errorf("the leftover is not reported: %v", problems)
	}
	if _, withheld := r.Withheld("sys"); withheld {
		t.Error("a plugin with no section was withheld")
	}

	untrusted, _ := Resolve(config.Config{Plugins: map[string]map[string]any{
		"pg@0000000000": {"host": "db.internal"},
	}}, installed)
	if _, withheld := untrusted.Withheld("pg"); withheld {
		t.Error("a working-directory file's section withheld its plugin")
	}
}

// A run that gives a namespace another build drops what was withheld with
// the section, since it was the installed build's, and the namespace is
// displaced from then on.
func TestARunWithAnotherBuildIsDisplacedAndNotWithheld(t *testing.T) {
	r, _ := Resolve(trusted(t, config.Config{Plugins: map[string]map[string]any{
		"pg@0000000000": {"host": "db.internal"},
	}}), installed)
	if r.Displaced("pg") {
		t.Fatal("a namespace is displaced before any run moved it")
	}
	dev := r.Without("pg")
	if _, withheld := dev.Withheld("pg"); withheld {
		t.Error("the dev build is refused for the installed build's section")
	}
	if !dev.Displaced("pg") || dev.Displaced("sys") {
		t.Error("Without displaced the wrong namespaces")
	}
	if _, withheld := r.Withheld("pg"); !withheld {
		t.Error("Without changed the resolver it was called on")
	}
}
