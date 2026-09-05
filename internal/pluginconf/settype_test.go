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
// wrong shape is not ignored, it is read as the zero. `tls: "true"` leaves a
// connection unencrypted while the file says otherwise, and Check — whose
// entire job is to name a stated value the catalogue cannot use — looked only
// for keys nothing reads and values outside a declared Options set.
//
// Reported rather than refused, which is this function's existing severity for
// every problem it finds: `rta doctor` prints them and nothing fails a call
// over one. Loud enough to fix, and the same place an operator already looks.

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
	r, _ := Resolve(trusted(t, config.Config{Plugins: map[string]map[string]any{"sys": values}}), installed)
	return r.Check(sysRegistry(t))
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
