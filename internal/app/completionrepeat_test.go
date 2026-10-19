package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/internal/recent"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// tidyRegistry has one capability that takes several ids, of which the ones
// that exist right now are what it suggests, as `note done` does.
func tidyRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "tidy", Summary: "tidy plugin",
		Capabilities: []plugin.Capability{{
			ID: "tidy.done", Summary: "check ids off", Safety: plugin.Write,
			Inputs: []plugin.Field{{
				Name: "id", Type: plugin.StringSlice, Positional: true, Required: true, Help: "id, or several",
				Suggest: func(context.Context, plugin.Request) []string { return []string{"1\tone", "2\ttwo", "3\tthree"} },
			}},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "done"}, nil },
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func withHistory(t *testing.T, v recent.Values) {
	t.Helper()
	saved := remembered
	t.Cleanup(func() { remembered = saved })
	remembered = func() recent.Values { return v }
}

func values(completions []string) []string {
	out := make([]string, len(completions))
	for i, c := range completions {
		out[i], _, _ = strings.Cut(c, "\t")
	}
	return out
}

// An id already on the line is not offered again, and the ones left are.
func TestCompletingSeveralIDsDoesNotOfferOneAlreadyTyped(t *testing.T) {
	withHistory(t, nil)
	got := values(complete(t, tidyRegistry(t), "tidy", "done", "2", ""))
	if !slices.Equal(got, []string{"1", "3"}) {
		t.Errorf("after 2 the offer is %v, want 1 and 3", got)
	}
	got = values(complete(t, tidyRegistry(t), "tidy", "done", "1", "3", ""))
	if !slices.Equal(got, []string{"2"}) {
		t.Errorf("after 1 and 3 the offer is %v, want 2", got)
	}
}

// What was used once is not an id that exists: a note checked off last week
// came back as a candidate to check off again, behind the list of the ones that
// are open. A field that lists what exists is the whole answer.
func TestAListOfWhatExistsIsNotPaddedWithWhatWasUsedBefore(t *testing.T) {
	withHistory(t, recent.Values{recent.Key("", "tidy.done", "id"): {"9", "2"}})
	got := values(complete(t, tidyRegistry(t), "tidy", "done", ""))
	if !slices.Equal(got, []string{"1", "2", "3"}) {
		t.Errorf("the offer is %v, want only 1, 2 and 3", got)
	}
}

// tagRegistry has one capability with a list flag, the shape of `note add
// --tag`: a comma list whose elements are suggested from what exists.
func tagRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "tidy", Summary: "tidy plugin",
		Capabilities: []plugin.Capability{{
			ID: "tidy.tag", Summary: "tag something", Safety: plugin.Write,
			Inputs: []plugin.Field{{
				Name: "tag", Type: plugin.StringSlice, Help: "tags, comma-separated",
				Suggest: func(context.Context, plugin.Request) []string {
					return []string{"recipe\tfood", "ideas", "italian\tcooking"}
				},
			}},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "tagged"}, nil },
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// completeDirective is complete with the directive cobra prints last: the bit
// that tells the shell whether to put a space after the word.
func completeDirective(t *testing.T, reg *registry.Registry, args ...string) ([]string, cobra.ShellCompDirective) {
	t.Helper()
	out, _, err := run(t, reg, append([]string{"__complete"}, args...)...)
	if err != nil {
		t.Fatalf("__complete: %v", err)
	}
	var lines []string
	var directive int
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		switch {
		case l == "":
		case strings.HasPrefix(l, ":"):
			directive, _ = strconv.Atoi(l[1:])
		default:
			lines = append(lines, l)
		}
	}
	return lines, cobra.ShellCompDirective(directive)
}

// A list flag completes the element being typed, as the TUI's box does. The
// shell hands the word as it stands, `recipe,it`, and filters what it is given
// by that prefix, so the second element was never offered: every candidate came
// back bare and none of them began with the word. Each comes back as the whole
// word it would make, and the shell is told not to add a space, since a comma
// may follow.
func TestAListFlagCompletesTheElementAfterTheComma(t *testing.T) {
	withHistory(t, nil)
	reg := tagRegistry(t)

	got, directive := completeDirective(t, reg, "tidy", "tag", "--tag", "recipe,it")
	if want := []string{"recipe,italian\tcooking"}; !slices.Equal(got, want) {
		t.Errorf("after recipe,it the offer is %q, want %q", got, want)
	}
	if directive&cobra.ShellCompDirectiveNoSpace == 0 {
		t.Errorf("directive = %d, want a list flag to keep the cursor against the word", directive)
	}

	got, _ = completeDirective(t, reg, "tidy", "tag", "--tag=recipe,it")
	if want := []string{"recipe,italian\tcooking"}; !slices.Equal(got, want) {
		t.Errorf("--tag=recipe,it offers %q, want %q", got, want)
	}

	got, _ = completeDirective(t, reg, "tidy", "tag", "--tag", "")
	if want := []string{"recipe\tfood", "ideas", "italian\tcooking"}; !slices.Equal(got, want) {
		t.Errorf("on an empty word the offer is %q, want all three with their descriptions", got)
	}

	got, _ = completeDirective(t, reg, "tidy", "tag", "--tag", "recipe,")
	if want := []string{"recipe,ideas", "recipe,italian\tcooking"}; !slices.Equal(got, want) {
		t.Errorf("after recipe, the offer is %q, want the two it does not list", got)
	}
}

// A list flag given again is the same list: the tag the first occurrence named is
// not offered to the second, as it is not offered after a comma.
func TestAListFlagGivenAgainDoesNotOfferWhatTheFirstGave(t *testing.T) {
	withHistory(t, nil)
	reg := tagRegistry(t)

	got, _ := completeDirective(t, reg, "tidy", "tag", "--tag", "recipe", "--tag", "")
	if want := []string{"ideas", "italian\tcooking"}; !slices.Equal(got, want) {
		t.Errorf("after --tag recipe the offer is %q, want the two it does not name", got)
	}
	got, _ = completeDirective(t, reg, "tidy", "tag", "--tag", "recipe,ideas", "--tag", "")
	if want := []string{"italian\tcooking"}; !slices.Equal(got, want) {
		t.Errorf("after --tag recipe,ideas the offer is %q, want the one left", got)
	}
}

// A list flag nobody gave holds its default, and a default is not something the
// line chose: it is no reason to hide the value from the offer, however it
// reads back from the flag.
func TestAListFlagsOwnDefaultIsStillOffered(t *testing.T) {
	withHistory(t, nil)
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "dflt", Summary: "dflt plugin",
		Capabilities: []plugin.Capability{{
			ID: "dflt.list", Summary: "list", Safety: plugin.Read,
			Inputs: []plugin.Field{{
				Name: "tag", Type: plugin.StringSlice, Default: []string{"x"}, Help: "tags",
				Suggest: func(context.Context, plugin.Request) []string { return []string{"x", "y"} },
			}},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := completeDirective(t, reg, "dflt", "list", "--tag", "")
	if want := []string{"x", "y"}; !slices.Equal(got, want) {
		t.Errorf("an unset --tag offers %q, want its default among the candidates", got)
	}
	got, _ = completeDirective(t, reg, "dflt", "list", "--tag", "x", "--tag", "")
	if want := []string{"y"}; !slices.Equal(got, want) {
		t.Errorf("after --tag x the offer is %q, want only the one not given", got)
	}
}

// A flag that takes one value keeps its space, and an argument that takes
// several is one value per word: only a list inside one word runs on.
func TestOnlyAListFlagRunsOnAfterACompletion(t *testing.T) {
	withHistory(t, nil)
	_, directive := completeDirective(t, tidyRegistry(t), "tidy", "done", "2", "")
	if directive&cobra.ShellCompDirectiveNoSpace != 0 {
		t.Errorf("directive = %d for a positional, want a space after each word", directive)
	}
	got, _ := completeDirective(t, tagRegistry(t), "tidy", "tag", "--tag", "zzz")
	if len(got) != 0 {
		t.Errorf("a word nothing continues is offered %q", got)
	}
}

// The hostnames on the machine, through the registry they ship in: a name on
// both the IPv4 and the IPv6 loopback line is one word to complete, and one
// already on the line is not offered again.
func TestAHostnameOnTwoLinesIsCompletedOnce(t *testing.T) {
	withHistory(t, nil)
	file := filepath.Join(t.TempDir(), "hosts")
	hosts := "127.0.0.1 localhost app.local\n::1 localhost\n10.0.0.2 other.local\n"
	if err := os.WriteFile(file, []byte(hosts), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}

	got := values(complete(t, reg, "net", "hosts", "rm", "--file", file, ""))
	if want := []string{"localhost", "app.local", "other.local"}; !slices.Equal(got, want) {
		t.Errorf("net hosts rm offers %q, want each name once", got)
	}
	got = values(complete(t, reg, "net", "hosts", "rm", "--file", file, "app.local", ""))
	if want := []string{"localhost", "other.local"}; !slices.Equal(got, want) {
		t.Errorf("after app.local the offer is %q, want the two it does not list", got)
	}
}

// The usage line says the argument repeats.
func TestTheUsageLineOfACommandThatTakesSeveralIDsSaysSo(t *testing.T) {
	root := NewRoot(tidyRegistry(t), "test")
	cmd, _, err := root.Find([]string{"tidy", "done"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cmd.Use, "done <id...>"; got != want {
		t.Errorf("use = %q, want %q", got, want)
	}
	if _, _, err := run(t, tidyRegistry(t), "tidy", "done"); err == nil ||
		!strings.Contains(err.Error(), "usage: rta tidy done <id...>") {
		t.Errorf("a missing id says %v, want the usage with <id...>", err)
	}
}
