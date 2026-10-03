package pluginhost

import (
	"context"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
)

// Looking for plugins and finding none costs next to nothing. The deny set a
// launched plugin is confined by resolves every credential location on the
// machine through its symlinks, which is a thousand allocations or more and
// most of a millisecond, and used to be built before the sweep on every rta
// command whether or not anything was ever launched. Counted rather than
// timed, so a loaded machine cannot fail it: nothing is on $PATH, nothing is
// installed, and the sweep as a whole stays far under what the deny set alone
// took.
func TestLoadingNoPluginsDoesNotBuildTheDenySet(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	const budget = 300
	allocs := testing.AllocsPerRun(10, func() {
		if problems := New().LoadInto(context.Background(), registry.New()); len(problems) != 0 {
			t.Fatalf("an empty machine reported problems: %v", problems)
		}
	})
	if allocs > budget {
		t.Errorf("loading no plugins allocated %.0f times, over %d: the deny set is being resolved before a plugin needs it", allocs, budget)
	}
}
