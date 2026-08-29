package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func TestExplainCatalog(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, reg, "explain")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sys.cpu", "cert.inspect", "net.ping", "read"} {
		if !strings.Contains(out, want) {
			t.Errorf("catalog missing %q", want)
		}
	}
}

func TestExplainCard(t *testing.T) {
	reg, _ := NewRegistry()
	out, _, err := run(t, reg, "explain", "net.port")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"net.port", "rta net port <host>", "net_port", "input:ports"} {
		if !strings.Contains(out, want) {
			t.Errorf("card missing %q:\n%s", want, out)
		}
	}
}

// The card is where a person or an agent is sent to find out how to invoke a
// capability, and it was the one surface that never mentioned --detail: the
// tool description advertises it in CLI syntax and the MCP schema publishes
// it as a boolean, so `rta explain sys.overview` described a capability whose
// richest view appeared not to exist. Asserted over the whole catalogue
// rather than one ID, because the disagreement was catalogue-wide.
func TestExplainCardShowsTheDetailFlag(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	detailed := 0
	for _, c := range reg.Capabilities() {
		if !c.Detailed {
			continue
		}
		detailed++
		out, _, err := run(t, reg, "explain", c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "--detail") {
			t.Errorf("%s: invocation omits --detail:\n%s", c.ID, out)
		}
		if !strings.Contains(out, "input:detail") {
			t.Errorf("%s: card does not say what --detail does:\n%s", c.ID, out)
		}
	}
	if detailed == 0 {
		t.Fatal("no Detailed capability in the catalogue: this test proves nothing")
	}
}

// …and it is the flag of a capability that has a detail view, not decoration
// on every card.
func TestExplainCardOmitsDetailWhereThereIsNone(t *testing.T) {
	reg, _ := NewRegistry()
	out, _, err := run(t, reg, "explain", "net.port")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "detail") {
		t.Errorf("net.port has no detail view but its card offers one:\n%s", out)
	}
}

func TestExplainSuggestsClosestMatch(t *testing.T) {
	reg, _ := NewRegistry()
	_, errOut, err := run(t, reg, "explain", "sys.cpuu")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "core.capability.unknown" {
		t.Fatalf("want core.capability.unknown, got %v", err)
	}
	if !strings.Contains(errOut, "sys.cpu") {
		t.Errorf("suggestion missing from stderr:\n%s", errOut)
	}
	if ExitCode(err) != 1 {
		t.Errorf("exit = %d, want 1", ExitCode(err))
	}
}

func TestDoctorReport(t *testing.T) {
	reg, _ := NewRegistry()
	out, _, err := run(t, reg, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"capabilities", "config", "exec-tier"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor missing %q:\n%s", want, out)
		}
	}
}

// The card answers "why did that host appear?" and "what do I write?" — and
// until it named the file, not "into which file". That third answer lived in
// `rta doctor`, which is a different command for the last third of one
// question.
func TestExplainNamesTheFileTheConfigBlockGoesIn(t *testing.T) {
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "demo", Summary: "demo",
		Capabilities: []plugin.Capability{
			{ID: "demo.configured", Summary: "reads config", Safety: plugin.Read,
				Inputs: []plugin.Field{{Name: "host", Type: plugin.String, Config: "host", Help: "h"}},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "ok"}, nil
				}},
			{ID: "demo.plain", Summary: "reads nothing", Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "ok"}, nil
				}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, reg, "explain", "demo.configured")
	if err != nil {
		t.Fatal(err)
	}
	// run() points RTA_CONFIG at its own temp file and t.Setenv restores it
	// at the end of the test, so config.Path() here is the path the command
	// just used. Asserting on that rather than on the substring "config.yaml"
	// keeps this about the real location and not about a filename.
	if want := config.Path(); !strings.Contains(out, want) {
		t.Errorf("explain does not name the config file %s:\n%s", want, out)
	}

	// And says nothing when nothing here reads one — a path printed under a
	// capability that cannot be configured is an invitation to edit a file
	// that will not change anything.
	out, _, err = run(t, reg, "explain", "demo.plain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "config file") {
		t.Errorf("explain named a config file for a capability that reads none:\n%s", out)
	}
}

// The card names an environment variable for the inputs that read one, and
// for no others.
//
// Local and EnvFallback were the same set when this line was written, so
// "Local" was a correct test for "filled from $RTA_<NS>_<INPUT>". Splitting
// EnvFallback out of it separated
// them — EnvFallback is for the fields that genuinely are credentials — and
// Closing the redirect hole then marked every connection input on pg, s3
// and vault Local *without*
// EnvFallback, deliberately, because a field that merely chooses a
// destination must not be fillable from an ambient variable. The card did not
// follow: `rta explain pg.status` told operators that --host comes from
// $RTA_PG_HOST, and eleven inputs across the three plugins documented a
// channel that reads nothing.
//
// Worth a test rather than a fix alone, because the failure is silent in
// exactly the wrong place: the page is what somebody consults when a
// connection is already failing, so a fictitious variable sends them to
// export something that cannot help and to stop looking at the config key
// that would have.
//
// Written against a synthetic capability rather than pg's declaration: what
// is under test is which *rule* the card applies, and that has to hold for
// plugins nobody has written yet.
func TestExplainNamesAnEnvironmentVariableOnlyWhereOneIsRead(t *testing.T) {
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "demo", Summary: "demo",
		Capabilities: []plugin.Capability{
			{ID: "demo.connect", Summary: "connects somewhere", Safety: plugin.Read,
				Inputs: []plugin.Field{
					// Chooses a destination. Local so no MCP caller may aim it,
					// and no EnvFallback, so nothing fills it from the ambient
					// environment. Exactly plugins/pg's `host`.
					{Name: "host", Type: plugin.String, Config: "host", Local: true, Help: "where"},
					// A credential. The one shape that does read a variable.
					{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "secret"},
					// Not Local at all: an ordinary payload input.
					{Name: "table", Type: plugin.String, Help: "what"},
				},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "ok"}, nil
				}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, reg, "explain", "demo.connect")
	if err != nil {
		t.Fatal(err)
	}

	// The credential's variable is named: "how do I give this thing its
	// password" must stay answerable from this page.
	if want := plugin.LocalEnvVar("demo.connect", "password"); !strings.Contains(out, "$"+want) {
		t.Errorf("card does not name $%s, so nothing says where the credential comes from:\n%s", want, out)
	}
	// The destination's is not, because nothing reads it.
	if bogus := plugin.LocalEnvVar("demo.connect", "host"); strings.Contains(out, "$"+bogus) {
		t.Errorf("card claims --host is filled from $%s, which plugin.Resolve never reads:\n%s", bogus, out)
	}
	// Both are still marked local: the fix must not cost the asymmetry
	// between the CLI and MCP its one mention.
	if n := strings.Count(out, "local (never offered to MCP callers)"); n != 2 {
		t.Errorf("expected both Local inputs marked local, got %d:\n%s", n, out)
	}
	// And the config key survives, which is the answer the operator actually
	// needs once the fictitious variable is gone.
	if !strings.Contains(out, "host") || !strings.Contains(out, "from config") {
		t.Errorf("card no longer says where --host does come from:\n%s", out)
	}
}
