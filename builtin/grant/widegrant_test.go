package grant

import (
	"context"
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func storeCatalog() []plugin.Capability {
	return []plugin.Capability{
		{ID: "kv.get", Safety: plugin.Write, Reveals: true},
		{ID: "kv.env", Safety: plugin.Write, Reveals: true},
		{ID: "kv.rm", Safety: plugin.Destructive},
		{ID: "kv.list", Safety: plugin.Read},
	}
}

// `grant allow kv` reads like the smallest row on the roster and is the widest
// one: the receipt said so, once, the minute it was issued, and the roster that
// is read at three in the morning for what is dangerous carried only the bare
// word beside a row that reads `kv.get deploy-key`.
func TestTheRosterSaysWhatAWholePluginGrantReaches(t *testing.T) {
	setup(t)
	now := time.Now()
	if verr := core.Save([]core.Grant{
		{Target: "kv", Agent: "claude", Issued: now, Expires: now.Add(time.Hour)},
		{Target: "kv.get", Scope: "deploy-key", Agent: "claude", Issued: now, Expires: now.Add(time.Hour)},
	}); verr != nil {
		t.Fatal(verr)
	}
	v, err := runList(context.Background(), req(nil), storeCatalog, builtIn)
	if err != nil {
		t.Fatal(err)
	}
	var wide []view.Error
	for _, w := range listed(t, v).Warnings {
		if w.Code == "grant.wide" {
			wide = append(wide, w)
		}
	}
	if len(wide) != 1 || !wide[0].Advisory {
		t.Fatalf("the roster holds one whole-plugin grant and warns %+v", wide)
	}
	want := "kv is a whole-plugin grant for claude: it covers 3 capabilities an agent needs a grant for, " +
		"1 destructive (kv.rm), and 2 that reveal stored values (kv.env, kv.get), each for every record it reaches"
	if wide[0].Message != want {
		t.Errorf("the warning reads\n %q\nwant\n %q", wide[0].Message, want)
	}
	if !strings.Contains(wide[0].Hint, "`rta grant revoke kv`") {
		t.Errorf("the hint reads %q, want the command that takes it back", wide[0].Hint)
	}
}

// A grant on one capability, a plugin with nothing that needs a grant and a
// grant on a plugin that is not here reach nothing a person would be surprised
// by, and say nothing.
func TestOnlyAWholePluginGrantThatReachesSomethingIsSaidToBeWide(t *testing.T) {
	grants := []core.Grant{{Target: "kv.get"}, {Target: "kv.rm", Scope: "x"}, {Target: "nosuch"}}
	if got := wideWarnings(plugin.SurfaceCLI, storeCatalog, grants); len(got) != 0 {
		t.Errorf("single-capability and unknown grants warn %+v", got)
	}
	free := func() []plugin.Capability { return []plugin.Capability{{ID: "sys.cpu", Safety: plugin.Read}} }
	if got := wideWarnings(plugin.SurfaceCLI, free, []core.Grant{{Target: "sys"}}); len(got) != 0 {
		t.Errorf("a plugin with nothing that needs a grant warns %+v", got)
	}
}
