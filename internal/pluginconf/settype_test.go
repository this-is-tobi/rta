package pluginconf

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The same failure `internal/profile` reports about a `set:` block, in the
// base `plugins:` block that a profile only overlays: a stated value of the
// wrong shape was not ignored, it was read as the zero. `tls: "true"` left a
// connection unencrypted while the file said otherwise, and Check — whose
// entire job is to name a stated value the catalogue cannot use — looked only
// for keys nothing reads and values outside a declared Options set.
//
// The host refuses every call reading such a value now (plugin.CheckInputs);
// this reports it once, in `rta doctor`, before any call does.

func sysRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "sys", Summary: "sys", Capabilities: []plugin.Capability{{
			ID: "sys.status", Summary: "status", Safety: plugin.Read,
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.Text{Body: "ok"}, nil
			},
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Config: "host", Local: true},
				{Name: "port", Type: plugin.Int, Default: 5432, Config: "port", Local: true},
				{Name: "tls", Type: plugin.Bool, Default: true, Config: "tls", Local: true},
				{Name: "mode", Type: plugin.String, Config: "mode", Local: true,
					Options: []string{"fast", "safe"}},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return reg
}

func checkOf(t *testing.T, values map[string]any) []Problem {
	t.Helper()
	reg := sysRegistry(t)
	r, _ := Resolve(trusted(t, config.Config{Plugins: map[string]map[string]any{"sys": values}}), installed.declaring(reg))
	return r.Check(reg)
}

func TestABaseValueTheHandlerWouldReadAsZeroIsReported(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]any
	}{
		{"quoted bool", map[string]any{"tls": "true"}},
		{"bare yes decoded as text", map[string]any{"tls": "yes"}},
		{"quoted int", map[string]any{"port": "5432"}},
		{"number for text", map[string]any{"host": 2024}},
		{"fraction for int", map[string]any{"port": 2.5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := checkOf(t, tc.values)
			if len(problems) == 0 {
				t.Fatal("accepted — the handler reads the zero and doctor says nothing")
			}
			if problems[0].Section != "sys" {
				t.Errorf("the problem does not name the section: %+v", problems[0])
			}
			if problems[0].Hint == "" {
				t.Error("no hint")
			}
			if !strings.Contains(problems[0].Reason, "every call reading it is refused") {
				t.Errorf("the report does not say what the run does: %s", problems[0].Reason)
			}
		})
	}
}

// A correctly typed section stays quiet, including the Options key the older
// check already covered.
func TestACorrectlyTypedSectionIsAccepted(t *testing.T) {
	problems := checkOf(t, map[string]any{
		"host": "db.internal", "port": uint64(6432), "tls": false, "mode": "safe",
	})
	if len(problems) != 0 {
		t.Fatalf("a correctly typed section was reported: %v", problems)
	}
}

// The type check runs before the Options check and stops there. Options
// compares the text of a value, so it has nothing to say about one that is
// not text — and reporting both would bury the fixable complaint under a
// second one caused by it.
func TestATypeProblemIsNotAlsoReportedAsAnOptionsProblem(t *testing.T) {
	problems := checkOf(t, map[string]any{"mode": 3})
	if len(problems) != 1 {
		t.Fatalf("want one problem, got %d: %v", len(problems), problems)
	}
	if strings.Contains(problems[0].Reason, "one of") ||
		strings.Contains(problems[0].Reason, "accepts") {
		t.Errorf("reported as an Options problem rather than a type one: %+v", problems[0])
	}
}

// A value outside the declared set is still reported as that, unchanged.
func TestTheOptionsCheckStillReportsAValueOutsideTheSet(t *testing.T) {
	problems := checkOf(t, map[string]any{"mode": "reckless"})
	if len(problems) != 1 || !strings.Contains(problems[0].Hint, "fast") {
		t.Fatalf("the Options check stopped working: %v", problems)
	}
}

// And the stated value is not echoed back.
func TestTheSectionReportDoesNotEchoTheStatedValue(t *testing.T) {
	for _, p := range checkOf(t, map[string]any{"tls": "hunter2-not-a-bool"}) {
		if strings.Contains(p.Reason+p.Hint, "hunter2") {
			t.Errorf("the stated value reached the report: %+v", p)
		}
	}
}

// A key a plugin declares as a dotted path is one key written flat or nested,
// and applied either way. Stated both ways in one section it is refused: the
// two are the same key, and nothing can tell which value was meant.
func TestADottedKeyIsAppliedFlatOrNestedAndRefusedStatedTwice(t *testing.T) {
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "sys", Summary: "sys", Capabilities: []plugin.Capability{{
			ID: "sys.password", Summary: "password", Safety: plugin.Read,
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
			Inputs: []plugin.Field{
				{Name: "symbols", Type: plugin.Bool, Config: "password.symbols"},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	cat := installed.declaring(reg)
	for name, values := range map[string]map[string]any{
		"flat":   {"password.symbols": true},
		"nested": {"password": map[string]any{"symbols": true}},
	} {
		r, problems := Resolve(trusted(t, config.Config{Plugins: map[string]map[string]any{"sys": values}}), cat)
		if len(problems) != 0 || r.For("sys")["password.symbols"] != true {
			t.Errorf("%s: applied %v, problems %v", name, r.For("sys"), problems)
		}
		if problems := r.Check(reg); len(problems) != 0 {
			t.Errorf("%s: reported %v", name, problems)
		}
		// And the handler gets it: the section a call runs with is the
		// flattened one, read by its dotted key.
		c, _ := reg.Capability("sys.password")
		if got := plugin.Resolve(c, plugin.Inputs{Config: r.For("sys")})["symbols"]; got != true {
			t.Errorf("%s: the handler got symbols = %v", name, got)
		}
	}
	twice := map[string]any{"password.symbols": true, "password": map[string]any{"symbols": false}}
	r, _ := Resolve(trusted(t, config.Config{Plugins: map[string]map[string]any{"sys": twice}}), cat)
	p, withheld := r.Withheld("sys")
	if !withheld || p.Key != "password.symbols" || !strings.Contains(p.Reason, "stated twice") || r.For("sys") != nil {
		t.Fatalf("withheld %v, %+v, applied %v", withheld, p, r.For("sys"))
	}
}

// A key nothing reads withholds the section, and every call of the plugin is
// refused, where it used to be reported by `rta doctor` and run on the
// plugin's default: a typo in a host ran against wherever the plugin points
// by default. The refusal says what the key was likely meant as.
func TestAMisspeltSectionKeyWithholdsTheSectionSayingWhatItWasMeantFor(t *testing.T) {
	withheldOf := func(values map[string]any) (*Resolver, Problem, bool) {
		r, _ := Resolve(trusted(t, config.Config{Plugins: map[string]map[string]any{"sys": values}}),
			installed.declaring(sysRegistry(t)))
		p, withheld := r.Withheld("sys")
		return r, p, withheld
	}
	r, p, withheld := withheldOf(map[string]any{"prot": 80, "host": "db"})
	if !withheld || p.Key != "prot" || !strings.Contains(p.Hint, `did you mean "port"?`) || r.For("sys") != nil {
		t.Fatalf("withheld %v, %+v, applied %v", withheld, p, r.For("sys"))
	}
	if verr := p.Refusal(); verr.Code != "core.config.unapplied" || !strings.Contains(verr.Message, `nothing in "sys" reads "prot"`) {
		t.Errorf("refusal = %v", verr)
	}
	if _, p, _ = withheldOf(map[string]any{"zzzzzzzz": 1}); !strings.Contains(p.Hint, "rta explain") {
		t.Fatalf("a key nothing resembles lost the pointer to explain: %+v", p)
	}
}

// A key the plugin renamed is told what replaced it, with that key's help,
// rather than guessed at: only the plugin knows the old spelling is no typo.
// An entry whose new key nothing reads is not used.
func TestARenamedKeyIsToldWhatReplacedIt(t *testing.T) {
	declare := func(renamed map[string]string) *registry.Registry {
		reg := registry.New()
		if err := reg.Register(plugin.Plugin{
			Name: "sys", Summary: "sys", Renamed: renamed, Capabilities: []plugin.Capability{{
				ID: "sys.status", Summary: "status", Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
				Inputs: []plugin.Field{
					{Name: "tls", Type: plugin.String, Config: "tls", Help: "TLS mode, verify-full checking the name"},
					{Name: "host", Type: plugin.String, Config: "host"},
				},
			}},
		}); err != nil {
			t.Fatal(err)
		}
		return reg
	}
	hint := func(renamed map[string]string, values map[string]any) string {
		r, _ := Resolve(trusted(t, config.Config{Plugins: map[string]map[string]any{"sys": values}}),
			installed.declaring(declare(renamed)))
		p, _ := r.Withheld("sys")
		return p.Hint
	}
	got := hint(map[string]string{"sslmode": "tls"}, map[string]any{"sslmode": "verify-full"})
	if got != `sys reads "tls" in its place: TLS mode, verify-full checking the name` {
		t.Errorf("hint = %q", got)
	}
	if got := hint(map[string]string{"sslmode": "gone"}, map[string]any{"sslmode": "verify-full"}); strings.Contains(got, "gone") {
		t.Errorf("an entry naming a key nothing reads was used: %q", got)
	}
}

// A key stated for the whole plugin when the plugin takes it once for each
// capability is told where it went, since a word that is only the end of two
// keys is nothing an edit distance finds.
func TestAKeyThatIsTheEndOfADottedOneIsToldTheSpelling(t *testing.T) {
	reg := registry.New()
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	if err := reg.Register(plugin.Plugin{
		Name: "sys", Summary: "sys", Capabilities: []plugin.Capability{
			{ID: "sys.usage", Summary: "usage", Safety: plugin.Read, Run: run,
				Inputs: []plugin.Field{{Name: "limit", Type: plugin.Int, Config: "usage.limit"}}},
			{ID: "sys.tree", Summary: "tree", Safety: plugin.Read, Run: run,
				Inputs: []plugin.Field{{Name: "limit", Type: plugin.Int, Config: "tree.limit"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	_, problems := Resolve(trusted(t, config.Config{Plugins: map[string]map[string]any{"sys": {"limit": 5}}}),
		installed.declaring(reg))
	if len(problems) != 1 || !strings.Contains(problems[0].Hint, `"tree.limit" or "usage.limit"`) {
		t.Fatalf("problems = %v, want one that names tree.limit and usage.limit", problems)
	}
}
