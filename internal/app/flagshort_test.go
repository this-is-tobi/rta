package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// shortRegistry has a capability whose handler says what it was given, with a
// one-letter flag on an int, a bool, a list and a duration.
func shortRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "brief", Summary: "brief plugin",
		Capabilities: []plugin.Capability{{
			ID: "brief.show", Summary: "show what was given", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "limit", Type: plugin.Int, Short: "n", Default: 10, Help: "how many"},
				{Name: "quiet", Type: plugin.Bool, Short: "q", Help: "say less"},
				{Name: "tag", Type: plugin.StringSlice, Short: "t", Help: "tags"},
				{Name: "wait", Type: plugin.Duration, Short: "w", Default: "30s", Min: "1s", Max: "2d", Help: "how long"},
			},
			Examples: []plugin.Example{
				{Title: "three, quietly", Inputs: map[string]any{"limit": 3, "quiet": true}},
				{Title: "an hour", Inputs: map[string]any{"wait": "1h"}},
			},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return view.Text{Body: fmt.Sprintf("limit=%d quiet=%v tags=%v wait=%v", req.Int("limit"),
					req.Bool("quiet"), req.StringSlice("tag"), req.Duration("wait"))}, nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// A declared one-letter flag was accepted by the contract and never registered,
// so `-n 3` was an unknown shorthand on the one surface that has flags.
func TestADeclaredOneLetterFlagIsGivenOnTheCommandLine(t *testing.T) {
	out, _, err := run(t, shortRegistry(t), "brief", "show", "-n", "3", "-q", "-t", "a", "-t", "b", "-w", "1d")
	if err != nil {
		t.Fatal(err)
	}
	if want := "limit=3 quiet=true tags=[a b] wait=24h0m0s"; !strings.Contains(out, want) {
		t.Errorf("output = %q, want %q", out, want)
	}
	help, _, err := run(t, shortRegistry(t), "brief", "show", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-n, --limit", "-q, --quiet", "-t, --tag", "-w, --wait"} {
		if !strings.Contains(help, want) {
			t.Errorf("help does not show %q:\n%s", want, help)
		}
	}
}

// A duration is a flag of that type: text with no unit is refused where it is
// typed, as a duration, with the range the capability declared beside it.
func TestADurationFlagRefusesATextThatIsNoDurationNamingTheRange(t *testing.T) {
	_, _, err := run(t, shortRegistry(t), "brief", "show", "--wait", "30")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != CodeUsage {
		t.Fatalf("err = %#v, want a usage error", err)
	}
	for _, want := range []string{"takes a duration such as 30s, 15m, 2h or 1d", "from 1s to 2d", `for --wait, not "30"`} {
		if !strings.Contains(ve.Message, want) {
			t.Errorf("message = %q, want it to say %q", ve.Message, want)
		}
	}
}

// The default is what the flag prints and what a caller who gives none gets.
func TestADurationFlagKeepsItsDefault(t *testing.T) {
	out, _, err := run(t, shortRegistry(t), "brief", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "wait=30s") {
		t.Errorf("output = %q, want the default of 30s", out)
	}
}

// A capability's declared calls are shown where a person looks one up: under
// EXAMPLES in --help, spelled for the command line, and on the card
// `rta explain` prints. They were declared and shown nowhere.
func TestADeclaredExampleIsShownInHelpAndOnTheExplainCard(t *testing.T) {
	help, _, err := run(t, shortRegistry(t), "brief", "show", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"EXAMPLES", "rta brief show --limit 3 --quiet   # three, quietly", "rta brief show --wait 1h   # an hour"} {
		if !strings.Contains(help, want) {
			t.Errorf("help does not show %q:\n%s", want, help)
		}
	}
	card, _, err := run(t, shortRegistry(t), "explain", "brief.show")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rta brief show --limit 3 --quiet   # three, quietly", "rta brief show --wait 1h   # an hour"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card does not show %q:\n%s", want, card)
		}
	}
}
