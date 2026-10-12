package app

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/recent"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Completion that offers what the fence will refuse lists everything a
// connection can see beside a profile that says it reaches a few — the
// operator's inventory, one keystroke from being accepted into a call that is
// then refused. Shell completion is narrowed to the allowlist, the shortlist of
// what was used is narrowed with it, and an input with nothing to complete
// from offers the allowlist itself.

func fenceCompletionRegistry(t *testing.T, liveCalled *bool) *registry.Registry {
	t.Helper()
	reg := registry.New()
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	if err := reg.Register(plugin.Plugin{Name: "app", Summary: "app", Capabilities: []plugin.Capability{{
		ID: "app.status", Summary: "status", Safety: plugin.Read, Run: run,
		Inputs: []plugin.Field{
			// Nothing of its own to complete from.
			{Name: "namespace", Type: plugin.String, Config: "namespace"},
			// What exists right now, which includes the connection's other namespaces.
			{Name: "cluster", Type: plugin.String, Config: "cluster",
				Suggest: func(context.Context, plugin.Request) []string {
					return []string{"alpha\tthe first", "beta", "gamma"}
				}},
			// A listing that asks the service, and so is never asked in a shell.
			{Name: "bucket", Type: plugin.String, Config: "bucket", Live: true,
				Suggest: func(context.Context, plugin.Request) []string {
					*liveCalled = true
					return []string{"everything", "else"}
				}},
			{Name: "mode", Type: plugin.String, Config: "mode", Options: []string{"fast", "safe", "slow"}},
			{Name: "replicas", Type: plugin.Int, Config: "replicas"},
			{Name: "unfenced", Type: plugin.String, Config: "unfenced",
				Suggest: func(context.Context, plugin.Request) []string { return []string{"one", "two"} }},
		},
	}}}); err != nil {
		t.Fatal(err)
	}
	return reg
}

const fencedCompletion = `
profiles:
  staging:
    plugins:
      app:
        only:
          namespace: [staging, staging-jobs]
          cluster: [beta, delta]
          bucket: [shop-assets]
          mode: [safe]
          replicas: [2, 3]
`

// offered is the values a __complete run printed, descriptions dropped.
func offered(out string) []string {
	var values []string
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "Completion ended") {
			continue
		}
		value, _, _ := strings.Cut(line, "\t")
		values = append(values, value)
	}
	return values
}

func TestShellCompletionIsNarrowedToTheFenceOfTheProfileOnTheLine(t *testing.T) {
	var live bool
	reg := fenceCompletionRegistry(t, &live)
	prior := remembered
	remembered = func() recent.Values {
		return recent.Values{
			recent.Key("staging", "app.status", "namespace"): {"prod", "staging", "other"},
			recent.Key("staging", "app.status", "bucket"):    {"prod-assets", "shop-assets"},
		}
	}
	t.Cleanup(func() { remembered = prior })

	for _, tc := range []struct {
		flag string
		want []string
	}{
		// No Suggest of its own: the allowlist, and of what was used before only
		// what it holds.
		{"--namespace", []string{"staging", "staging-jobs"}},
		// A Suggest: what exists, narrowed — the description stays.
		{"--cluster", []string{"beta"}},
		// A listing that asks the service is not asked here, so the allowlist is
		// what there is, with the shortlist narrowed to it.
		{"--bucket", []string{"shop-assets"}},
		// A closed set, narrowed.
		{"--mode", []string{"safe"}},
		// A number's entries are numbers.
		{"--replicas", []string{"2", "3"}},
	} {
		out, errOut, err := runWith(t, reg, fencedCompletion,
			"__complete", "app", "status", "--profile", "staging", tc.flag, "")
		if err != nil {
			t.Fatalf("%s: %v %q", tc.flag, err, errOut)
		}
		got := offered(out)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s offered %v, want %v\n%s", tc.flag, got, tc.want, out)
		}
	}
	if live {
		t.Error("shell completion called a Live Suggest")
	}
}

// An input the fence does not name, and a command line naming no profile,
// complete as they always did.
func TestCompletionOutsideAFenceIsUntouched(t *testing.T) {
	var live bool
	reg := fenceCompletionRegistry(t, &live)
	out, errOut, err := runWith(t, reg, fencedCompletion,
		"__complete", "app", "status", "--profile", "staging", "--unfenced", "")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if got := offered(out); strings.Join(got, ",") != "one,two" {
		t.Errorf("an unfenced input offered %v", got)
	}
	out, _, err = runWith(t, reg, fencedCompletion, "__complete", "app", "status", "--cluster", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := offered(out); strings.Join(got, ",") != "alpha,beta,gamma" {
		t.Errorf("with no profile on the line, --cluster offered %v", got)
	}
}
