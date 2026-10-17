package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
