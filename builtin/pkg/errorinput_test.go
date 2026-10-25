package pkg

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A refusal of one input names it (view.Error.Input), by the name the
// capability that refused it declares, so a form opens again on that box: the
// tools list too, which comes from the configuration and which a form shows in
// its box to be overridden for one run. What this machine is — a manager not on
// $PATH, rta not root — names none, since no value typed differently changes it.
func TestARefusalOfOneInputNamesIt(t *testing.T) {
	gobin := t.TempDir()
	tooMany := make([]string, maxTools+1)
	for i := range tooMany {
		tooMany[i] = "t" + strconv.Itoa(i) + "=github:o/r"
	}
	cases := []struct {
		name   string
		capID  string
		values map[string]any
		code   string
		input  string
	}{
		{"a package that reads as a flag", "pkg.upgrade",
			map[string]any{"target": "brew", "package": "-x"}, "pkg.upgrade.package", "package"},
		{"a package for a tool", "pkg.upgrade",
			map[string]any{"target": "fzf", "package": "x", "tools": []string{"fzf=github:junegunn/fzf"}}, "pkg.upgrade.package", "package"},
		{"no package for a manager that takes one at a time", "pkg.upgrade",
			map[string]any{"target": "cargo"}, "pkg.upgrade.package", "package"},
		{"a binary go install did not build", "pkg.upgrade",
			map[string]any{"target": "go", "package": "notgo"}, "pkg.go.unknown", "package"},
		{"a target that is no manager and no tool", "pkg.upgrade",
			map[string]any{"target": "nope"}, "pkg.upgrade.unknown", "target"},
		{"a tools entry out of the grammar, upgrading", "pkg.upgrade",
			map[string]any{"target": "fzf", "tools": []string{"bad"}}, "pkg.tools.entry", "tools"},
		{"a tools entry out of the grammar, listing", "pkg.tools",
			map[string]any{"tools": []string{"bad"}}, "pkg.tools.entry", "tools"},
		{"a tools entry out of the grammar, at a glance", "pkg.overview",
			map[string]any{"tools": []string{"bad"}}, "pkg.tools.entry", "tools"},
		{"more tools than the cap", "pkg.tools",
			map[string]any{"tools": tooMany}, "pkg.tools.toomany", "tools"},
		{"a manager pkg does not know", "pkg.outdated",
			map[string]any{"manager": "nope"}, "pkg.manager.unknown", "manager"},
		{"a manager that needs root", "pkg.upgrade",
			map[string]any{"target": "apt"}, "pkg.upgrade.root", ""},
		{"a manager not on $PATH", "pkg.upgrade",
			map[string]any{"target": "brew"}, "pkg.manager.missing", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			install(t, &fake{bins: map[string]bool{"cargo": true, "apt-get": true, "go": true}, answers: map[string]fakeAnswer{
				"go env GOBIN": {out: gobin + "\n"},
				"go version -m " + filepath.Join(gobin, "notgo"): {code: 1, stderr: "not a Go executable"},
			}})
			var c plugin.Capability
			for _, d := range Plugin().Capabilities {
				if d.ID == tc.capID {
					c = d
				}
			}

			_, err := c.Run(context.Background(), req(t, tc.capID, tc.values))
			ve := view.AsError(err, "x")
			if ve == nil || ve.Code != tc.code {
				t.Fatalf("refused as %+v, want %s", ve, tc.code)
			}
			if tc.input == "" {
				if ve.Input != "" {
					t.Errorf("%s names the input %q, want none", ve.Code, ve.Input)
				}
				return
			}
			if f, ok := c.ErrorInput(ve); !ok || f.Name != tc.input {
				t.Errorf("%s names the input %q, want %s, which %s declares", ve.Code, ve.Input, tc.input, c.ID)
			}
		})
	}
}
