package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/recent"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A form built for a fenced environment offers what that environment reaches
// and nothing it would refuse: its own Suggest and the listing a deliberate tab
// fetched are narrowed to the allowlist, so is the shortlist of what was used
// before, and a box with nothing to complete from offers the allowlist itself.

func fencedCap(liveCalled *bool) plugin.Capability {
	return plugin.Capability{
		ID: "store.list", Summary: "list", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "bucket", Type: plugin.String, Config: "bucket"},
			{Name: "cluster", Type: plugin.String, Config: "cluster",
				Suggest: func(context.Context, plugin.Request) []string { return []string{"alpha", "beta", "gamma"} }},
			{Name: "prefix", Type: plugin.String, Config: "prefix", Live: true,
				Suggest: func(context.Context, plugin.Request) []string {
					*liveCalled = true
					return []string{"everything"}
				}},
			{Name: "mode", Type: plugin.String, Config: "mode", Options: []string{"fast", "safe", "slow"}},
			{Name: "free", Type: plugin.String, Config: "free"},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
}

var fenceOfTheEnvironment = plugin.Fence{
	"bucket":  {"shop-assets", "shop-logs"},
	"cluster": {"beta"},
	"prefix":  {"reports/"},
	"mode":    {"safe"},
}

func TestAFormForAFencedEnvironmentOffersOnlyWhatItReaches(t *testing.T) {
	noHistory(t)
	var live bool
	c := fencedCap(&live)
	recent.Record(plugin.SurfaceCLI, c, "", map[string]any{"bucket": "prod-assets"})
	recent.Record(plugin.SurfaceCLI, c, "", map[string]any{"bucket": "shop-logs"})
	recent.Record(plugin.SurfaceCLI, c, "", map[string]any{"free": "mine"})

	cf := newCapForm(c, c.Inputs, nil, true, nil, withEnvironment("", fenceOfTheEnvironment))
	field := func(name string) plugin.Field {
		for _, f := range c.Inputs {
			if f.Name == name {
				return f
			}
		}
		t.Fatalf("no %s input", name)
		return plugin.Field{}
	}
	for _, tc := range []struct {
		input string
		want  string
	}{
		// Nothing to complete from: the allowlist, with what was used narrowed
		// to it (shop-logs is both; prod-assets is the other environment's).
		{"bucket", "shop-assets,shop-logs"},
		// A Suggest: what exists, narrowed.
		{"cluster", "beta"},
		// A Live one is not asked per keystroke, so the allowlist is what there is.
		{"prefix", "reports/"},
		// An input the fence does not name is untouched, recents included.
		{"free", "mine"},
	} {
		if got := strings.Join(cf.candidates(field(tc.input)), ","); got != tc.want {
			t.Errorf("%s offers %q, want %q", tc.input, got, tc.want)
		}
	}
	if live {
		t.Error("the keystroke channel called a Live Suggest")
	}

	// What a deliberate tab fetched from the service is every prefix the
	// connection can see, and what lands on the widget is the part it reaches.
	cf.setFetched("prefix", []string{"everything", "reports/", "archive/"})
	if got := strings.Join(cf.candidates(field("prefix")), ","); got != "reports/" {
		t.Errorf("a fetched listing offers %q, want only what the fence holds", got)
	}

	// The same form with no fence is what it always was.
	plain := newCapForm(c, c.Inputs, nil, true, nil)
	if got := strings.Join(plain.candidates(field("bucket")), ","); got != "shop-logs,prod-assets" {
		t.Errorf("an unfenced bucket offers %q", got)
	}
	if got := strings.Join(plain.candidates(field("cluster")), ","); got != "alpha,beta,gamma" {
		t.Errorf("an unfenced cluster offers %q", got)
	}
}

// A closed set is a picker, and a picker lists what could be chosen: a choice
// the call would refuse is not one to put on screen.
func TestTheFencedPickerListsOnlyWhatTheEnvironmentReaches(t *testing.T) {
	noHistory(t)
	var live bool
	c := fencedCap(&live)
	shown := func(cf *capForm) string {
		cf.form = startedForm(cf)
		return ansi.Strip(cf.form.View())
	}
	fenced := shown(newCapForm(c, []plugin.Field{c.Inputs[3]}, nil, true, nil, withEnvironment("", fenceOfTheEnvironment)))
	if !strings.Contains(fenced, "safe") || strings.Contains(fenced, "fast") || strings.Contains(fenced, "slow") {
		t.Errorf("the fenced picker shows:\n%s", fenced)
	}
	plain := shown(newCapForm(c, []plugin.Field{c.Inputs[3]}, nil, true, nil))
	if !strings.Contains(plain, "fast") || !strings.Contains(plain, "slow") {
		t.Errorf("the unfenced picker lost options:\n%s", plain)
	}
}
