package plugindist

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/plugintrust"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A bulk upgrade decides on its own whether to land bytes, so it needs the
// half of the declaration diff that is not merely news: the changes that hand
// the plugin more than the operator last agreed to.
//
// Every case below is one an operator would want stopped at, and every
// non-case is one that would make a gate people turn off.
func TestWideningsNameEveryGrowthAndNothingElse(t *testing.T) {
	cap_ := func(id string, s plugin.Safety, grant bool) plugin.Capability {
		return plugin.Capability{ID: id, Safety: s, NeedsGrant: grant}
	}
	decl := func(caps []plugin.Capability, needs ...plugin.Need) plugin.Plugin {
		return plugin.Plugin{Name: "pg", Capabilities: caps, Needs: needs}
	}

	cases := []struct {
		name string
		old  plugin.Plugin
		next plugin.Plugin
		want []string
	}{{
		name: "an unchanged declaration widens nothing",
		old:  decl([]plugin.Capability{cap_("pg.query", plugin.Read, false)}),
		next: decl([]plugin.Capability{cap_("pg.query", plugin.Read, false)}),
	}, {
		name: "a new read capability that needs no grant is news, not growth",
		old:  decl([]plugin.Capability{cap_("pg.query", plugin.Read, false)}),
		next: decl([]plugin.Capability{cap_("pg.query", plugin.Read, false), cap_("pg.status", plugin.Read, false)}),
	}, {
		name: "a capability going away widens nothing",
		old:  decl([]plugin.Capability{cap_("pg.query", plugin.Read, false), cap_("pg.status", plugin.Read, false)}),
		next: decl([]plugin.Capability{cap_("pg.query", plugin.Read, false)}),
	}, {
		name: "a new destructive capability is the event upgrade exists to catch",
		old:  decl([]plugin.Capability{cap_("pg.query", plugin.Read, false)}),
		next: decl([]plugin.Capability{cap_("pg.query", plugin.Read, false), cap_("pg.table.drop", plugin.Destructive, true)}),
		want: []string{"+ pg.table.drop  destructive, needs a grant"},
	}, {
		name: "a new write capability is growth even ungated",
		old:  decl([]plugin.Capability{cap_("pg.query", plugin.Read, false)}),
		next: decl([]plugin.Capability{cap_("pg.query", plugin.Read, false), cap_("pg.vacuum", plugin.Write, false)}),
		want: []string{"+ pg.vacuum  write"},
	}, {
		name: "a new read capability behind a grant is growth: the grant is the tell",
		old:  decl([]plugin.Capability{cap_("pg.query", plugin.Read, false)}),
		next: decl([]plugin.Capability{cap_("pg.query", plugin.Read, false), cap_("pg.dump", plugin.Read, true)}),
		want: []string{"+ pg.dump  read, needs a grant"},
	}, {
		name: "a safety class rising under an id the operator already approved",
		old:  decl([]plugin.Capability{cap_("pg.query", plugin.Read, false)}),
		next: decl([]plugin.Capability{cap_("pg.query", plugin.Write, false)}),
		want: []string{"! pg.query  read → write"},
	}, {
		name: "a safety class falling widens nothing",
		old:  decl([]plugin.Capability{cap_("pg.query", plugin.Destructive, false)}),
		next: decl([]plugin.Capability{cap_("pg.query", plugin.Read, false)}),
	}, {
		name: "a capability newly put behind a grant widens nothing",
		old:  decl([]plugin.Capability{cap_("pg.vacuum", plugin.Write, false)}),
		next: decl([]plugin.Capability{cap_("pg.vacuum", plugin.Write, true)}),
	}, {
		// The loosening the old diff printed without weighting: a destructive
		// capability that stops needing a grant is the same event as one that
		// appears, reached from the other side.
		name: "a capability that stops needing a grant is growth",
		old:  decl([]plugin.Capability{cap_("pg.vacuum", plugin.Write, true)}),
		next: decl([]plugin.Capability{cap_("pg.vacuum", plugin.Write, false)}),
		want: []string{"! pg.vacuum  no longer needs a grant"},
	}, {
		name: "a new credential need is growth",
		old:  decl([]plugin.Capability{cap_("pg.query", plugin.Read, false)}),
		next: decl([]plugin.Capability{cap_("pg.query", plugin.Read, false)}, plugin.NeedKubeconfig),
		want: []string{"+ asks to read kubeconfig"},
	}, {
		name: "a credential need going away widens nothing",
		old:  decl([]plugin.Capability{cap_("pg.query", plugin.Read, false)}, plugin.NeedKubeconfig),
		next: decl([]plugin.Capability{cap_("pg.query", plugin.Read, false)}),
	}, {
		name: "every axis at once, in the order the diff lists them",
		old: decl([]plugin.Capability{cap_("pg.query", plugin.Read, false),
			cap_("pg.vacuum", plugin.Write, true)}),
		next: decl([]plugin.Capability{cap_("pg.query", plugin.Write, false),
			cap_("pg.vacuum", plugin.Write, false),
			cap_("pg.table.drop", plugin.Destructive, true)}, plugin.NeedSSH),
		want: []string{
			"! pg.query  read → write",
			"! pg.vacuum  no longer needs a grant",
			"+ pg.table.drop  destructive, needs a grant",
			"+ asks to read ssh",
		},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := widenings(tc.old, tc.next)
			if len(got) != len(tc.want) {
				t.Fatalf("widenings = %q, want %q", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("widenings[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// Needs are a fourth dimension an authorization hangs off, and the diff was
// blind to them: a plugin that grew an appetite for ~/.aws reported its
// declaration unchanged.
func TestTheDeclarationDiffNamesCredentialNeeds(t *testing.T) {
	old := plugin.Plugin{Name: "pg",
		Capabilities: []plugin.Capability{{ID: "pg.query", Safety: plugin.Read}},
		Needs:        []plugin.Need{plugin.NeedKubeconfig}}
	next := plugin.Plugin{Name: "pg",
		Capabilities: []plugin.Capability{{ID: "pg.query", Safety: plugin.Read}},
		Needs:        []plugin.Need{plugin.NeedAWS, plugin.NeedSSH}}

	want := []string{"+ asks to read aws", "+ asks to read ssh", "- asks to read kubeconfig"}
	diff := declarationDiff(old, next)
	if len(diff) != len(want) {
		t.Fatalf("diff = %q, want %q", diff, want)
	}
	for i := range want {
		if diff[i] != want[i] {
			t.Errorf("diff[%d] = %q, want %q", i, diff[i], want[i])
		}
	}
}

// The gate has to sit exactly where installFrom knows the new declaration and
// has written nothing: `place`, `plugintrust.Add` and `recordInstall` all come
// after that point. A gate that ran any later would be withdrawing an install
// rather than refusing one — and an operator who reads "skipped" would still
// have the bytes, the trust entry and the moved pin.
//
// The gate passed here refuses unconditionally rather than being the real
// predicate: what the seam owes its caller is that a refusal costs nothing,
// and which declarations deserve a refusal is decided — and tested — in
// widenings, one layer up.
func TestARefusedGateLeavesNoBytesNoTrustAndNoRecord(t *testing.T) {
	testData(t)
	bin := hello(t)
	repo := gitFixture(t, map[string]string{"hello": helloManifest(t, bin, "")})
	if verr := AddIndex(context.Background(), "lab", repo); verr != nil {
		t.Fatal(verr)
	}
	listed, verr := Resolve("lab/hello")
	if verr != nil {
		t.Fatal(verr)
	}
	digest := sha256Of(t, bin)

	var saw plugin.Plugin
	refuse := func(declared plugin.Plugin) *view.Error {
		saw = declared
		return view.Errorf("plugin.upgrade.authority", "not on my watch")
	}
	if _, verr := installFrom(context.Background(), listed, io.Discard, false, refuse); verr == nil {
		t.Fatal("a refused gate installed anyway")
	} else if verr.Code != "plugin.upgrade.authority" {
		t.Fatalf("install = %s (%s), want the gate's own refusal", verr.Code, verr.Message)
	}

	// The gate is handed what rta learned by running the artifact, not what
	// the index claimed — deciding on the claim would decide on the wrong
	// thing, which is the whole reason install verifies at all.
	if saw.Name != "hello" || len(saw.Capabilities) == 0 {
		t.Fatalf("the gate saw %+v, want the launched declaration", saw)
	}

	if entries := ReadLock(); len(entries) != 0 {
		t.Errorf("a refused install recorded %v", entries)
	}
	if plugintrust.Load().Trusts(digest) {
		t.Error("a refused install trusted the artifact")
	}
	if _, err := os.Stat(filepath.Join(StoreDir(), "hello", digest)); err == nil {
		t.Error("a refused install left bytes in the store")
	}

	// The control: the same install with nothing in its way does land. Without
	// this, a fixture broken in any other way would read as a working gate.
	if _, verr := installFrom(context.Background(), listed, io.Discard, false, nil); verr != nil {
		t.Fatalf("an ungated install of the same artifact failed: %v", verr)
	}
	if !plugintrust.Load().Trusts(digest) {
		t.Error("the ungated install did not trust the artifact")
	}
}
