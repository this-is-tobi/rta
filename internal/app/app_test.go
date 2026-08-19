package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// testRegistry builds a registry with one read and one destructive capability.
func testRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name:    "demo",
		Summary: "demo plugin",
		Capabilities: []plugin.Capability{
			{
				ID:      "demo.item.list",
				Summary: "list items",
				Safety:  plugin.Read,
				Inputs: []plugin.Field{
					{Name: "limit", Type: plugin.Int, Help: "max items", Default: 10},
					{Name: "name", Type: plugin.String, Help: "filter", Positional: true},
				},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					return view.Text{Body: strings.TrimSpace("items " + req.String("name"))}, nil
				},
			},
			{
				ID:      "demo.item.rm",
				Summary: "remove an item",
				Safety:  plugin.Destructive,
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					if req.DryRun {
						return view.Text{Body: "would remove item"}, nil
					}
					return view.Text{Body: "removed"}, nil
				},
			},
			{
				ID:      "demo.item.pick",
				Summary: "has a closed set and a suggested value",
				Safety:  plugin.Read,
				Inputs: []plugin.Field{
					{Name: "name", Type: plugin.String, Positional: true, Required: true,
						Suggest: func(_ context.Context, req plugin.Request) []string {
							// Depends on an earlier answer, which is the whole
							// reason completion gets the request.
							return []string{"alpha\tfirst", "beta-" + req.String("mode")}
						}},
					{Name: "mode", Type: plugin.String, Options: []string{"fast", "slow"}, Help: "how"},
					{Name: "out", Type: plugin.Path, Help: "where to write it"},
					{Name: "surface", Type: plugin.String,
						Suggest: func(_ context.Context, req plugin.Request) []string {
							return []string{string(req.Surface())}
						}},
				},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "picked"}, nil
				},
			},
			{
				ID:      "demo.item.fail",
				Summary: "always fails",
				Safety:  plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return nil, view.Errorf("demo.fail", "it broke").WithHint("try again")
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// run executes the root command with args and captures stdout/stderr.
func run(t *testing.T, reg *registry.Registry, args ...string) (string, string, error) {
	t.Helper()
	// NewRoot calls config.Load(), which reads RTA_CONFIG (or the real XDG
	// config path) unconditionally — every test in this file ran against
	// whatever config happened to be sitting on the machine running the
	// suite, config.Output included, silently changing the default --output
	// format underfoot. Isolated the same way doctor_test.go's isolate() is:
	// its own directory, no ambient key material either.
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_KV_PASSPHRASE", "")
	t.Setenv("RTA_KV_IDENTITY", "")
	root := NewRoot(reg, "test")
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

func TestRunCapability(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "demo", "item", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "items") {
		t.Errorf("output = %q", out)
	}
}

func TestPositionalArg(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "demo", "item", "list", "widgets")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "items widgets") {
		t.Errorf("positional not passed: %q", out)
	}
}

func TestVariadicPositional(t *testing.T) {
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "multi", Summary: "multi",
		Capabilities: []plugin.Capability{{
			ID: "multi.check", Summary: "check many", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "targets", Type: plugin.StringSlice, Positional: true, Required: true, Help: "targets"},
			},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return view.Text{Body: strings.Join(req.StringSlice("targets"), "+")}, nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, reg, "multi", "check", "a", "b", "c")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a+b+c") {
		t.Errorf("variadic args not collected: %q", out)
	}
}

func TestTypedPositionalConversion(t *testing.T) {
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "typed", Summary: "typed",
		Capabilities: []plugin.Capability{{
			ID: "typed.take", Summary: "take an int", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "id"},
			},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return view.Text{Body: fmt.Sprintf("got %d", req.Int("id")+1)}, nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, reg, "typed", "take", "41")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "got 42") {
		t.Errorf("int positional not converted: %q", out)
	}
	// Invalid ints are usage errors.
	_, _, err = run(t, reg, "typed", "take", "abc")
	if ExitCode(err) != 2 {
		t.Errorf("bad int exit = %d, want 2", ExitCode(err))
	}
}

func TestJSONOutput(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "demo", "item", "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out)
	}
	if m["type"] != "text" {
		t.Errorf("type = %v", m["type"])
	}
}

func TestDestructiveRequiresYes(t *testing.T) {
	_, _, err := run(t, testRegistry(t), "demo", "item", "rm")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != CodeConfirmRequired {
		t.Fatalf("want %s, got %v", CodeConfirmRequired, err)
	}
	if ExitCode(err) != 3 {
		t.Errorf("exit code = %d, want 3", ExitCode(err))
	}
}

func TestDestructiveWithYes(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "demo", "item", "rm", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "removed") {
		t.Errorf("output = %q", out)
	}
}

func TestDestructiveDryRunBypassesGate(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "demo", "item", "rm", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "would remove") {
		t.Errorf("output = %q", out)
	}
}

func TestCapabilityErrorRendering(t *testing.T) {
	_, errOut, err := run(t, testRegistry(t), "demo", "item", "fail")
	if ExitCode(err) != 1 {
		t.Errorf("exit code = %d, want 1", ExitCode(err))
	}
	for _, want := range []string{"demo.fail", "it broke", "try again"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q:\n%s", want, errOut)
		}
	}
}

func TestUnknownSubcommandIsUsageError(t *testing.T) {
	_, _, err := run(t, testRegistry(t), "demo", "nope")
	if err == nil {
		t.Fatal("unknown subcommand accepted")
	}
	if ExitCode(err) != 2 {
		t.Errorf("exit code = %d, want 2", ExitCode(err))
	}
}

func TestExitCodeContract(t *testing.T) {
	if ExitCode(nil) != 0 {
		t.Error("nil should be 0")
	}
	if ExitCode(errors.New("usage")) != 2 {
		t.Error("plain errors are usage errors → 2")
	}
	if ExitCode(&view.Error{Code: "x.y", Message: "m"}) != 1 {
		t.Error("capability errors → 1")
	}
	if ExitCode(&view.Error{Code: CodeConfirmRequired, Message: "m"}) != 3 {
		t.Error("confirmation declined → 3")
	}
}

func TestBuiltinRegistryLoads(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Capability("sys.cpu"); !ok {
		t.Error("sys.cpu not registered")
	}
}

// --- Completion ---------------------------------------------------------
//
// cobra answers a shell through a hidden __complete command, so driving that
// is testing exactly what a terminal sees.

func complete(t *testing.T, reg *registry.Registry, args ...string) []string {
	t.Helper()
	out, _, err := run(t, reg, append([]string{"__complete"}, args...)...)
	if err != nil {
		t.Fatalf("__complete: %v", err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l == "" || strings.HasPrefix(l, ":") { // trailing directive line
			continue
		}
		lines = append(lines, l)
	}
	return lines
}

// A closed set completes to exactly its members.
func TestFlagCompletionOffersOptions(t *testing.T) {
	got := complete(t, testRegistry(t), "demo", "item", "pick", "x", "--mode", "")
	if len(got) != 2 || got[0] != "fast" || got[1] != "slow" {
		t.Errorf("--mode completion = %v, want the declared options", got)
	}
}

// A positional completes from what exists, descriptions included — cobra
// shows the text after the tab, which is what makes an id worth choosing.
func TestPositionalCompletionOffersSuggestions(t *testing.T) {
	got := complete(t, testRegistry(t), "demo", "item", "pick", "")
	if len(got) == 0 || !strings.HasPrefix(got[0], "alpha\tfirst") {
		t.Fatalf("positional completion = %v", got)
	}
}

// Suggestions see what the caller has already typed, so a later field can
// depend on an earlier one.
func TestCompletionSeesEarlierAnswers(t *testing.T) {
	got := complete(t, testRegistry(t), "demo", "item", "pick", "--mode", "fast", "")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "beta-fast") {
		t.Errorf("completion ignored the flag already given: %v", got)
	}
}

// A field with nothing to offer must not fall back to completing filenames:
// a list of the current directory is worse than no answer.
func TestCompletionWithoutCandidatesOffersNothing(t *testing.T) {
	if got := complete(t, testRegistry(t), "demo", "item", "list", ""); len(got) != 0 {
		t.Errorf("completion = %v, want nothing", got)
	}
}

// A path is completable with nothing declared: the shell knows what is on the
// filesystem, and directive 0 is how cobra is told to let it answer.
func TestPathCompletionDefersToTheShell(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "__complete", "demo", "item", "pick", "x", "--out", "")
	if err != nil {
		t.Fatal(err)
	}
	directive := ""
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(l, ":") {
			directive = l
		}
	}
	if directive != ":0" {
		t.Errorf("directive = %q, want :0 (default file completion)", directive)
	}
}

// A field with no path type keeps refusing file completion, so the two cases
// stay distinguishable rather than one silently becoming the other.
func TestNonPathCompletionStillRefusesFiles(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "__complete", "demo", "item", "pick", "x", "--mode", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, ":4") {
		t.Errorf("want ShellCompDirectiveNoFileComp, got %q", out)
	}
}

// Completion is a keystroke, not a caller: a Suggest that would prompt has to
// be able to tell, and the surface is how.
func TestCompletionRunsOnItsOwnSurface(t *testing.T) {
	got := complete(t, testRegistry(t), "demo", "item", "pick", "x", "--surface", "")
	if len(got) != 1 || got[0] != string(plugin.SurfaceCompletion) {
		t.Errorf("surface seen by Suggest = %v, want completion", got)
	}
}

// `rta explain` answers "what can I do"; this answers "what do I have".
func TestPluginsInventory(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "plugins", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Rows [][]string `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("plugins is not a table: %v (%s)", err, out)
	}
	if len(env.Rows) != 1 || env.Rows[0][0] != "demo" {
		t.Fatalf("rows = %v", env.Rows)
	}
	// The reach column is the one worth having: "which of these can change my
	// machine?" — answered by the worst class the plugin holds.
	if env.Rows[0][2] != "destructive" {
		t.Errorf("reach = %q, want the worst class the plugin holds", env.Rows[0][2])
	}
}
