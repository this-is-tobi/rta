package grant

import (
	"context"
	"strings"
	"testing"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// **A plugin-wide grant says how many capabilities it covers, and a grant
// that names a profile covers fewer.** It matches the connection exactly, so
// it covers a capability only if a call of it can name one; the count was made
// without asking, and `grant allow http --profile staging` said it covered
// six where the same grant with no profile covered five — the sixth takes no
// profile at all, and no call of it is covered by anything naming one.
func TestAProfiledPluginWideGrantCountsOnlyWhatItCanCover(t *testing.T) {
	// db: status and query take a profile and need a grant only for one;
	// vacuum and drop take one and need a grant anyway; tool needs a grant and
	// takes none; version is a free read that takes none; dump is for the
	// person at the terminal.
	without := breadthNote(envCatalog, core.Grant{Target: "db"})
	if want := "note: db covers 3 capabilities an agent needs a grant for, 1 destructive (db.drop) — name one to allow only it"; without != want {
		t.Errorf("with no profile = %q, want %q", without, want)
	}
	with := breadthNote(envCatalog, core.Grant{Target: "db", Profile: "staging"})
	if want := "note: db covers 4 capabilities an agent needs a grant for, 1 destructive (db.drop) — name one to allow only it"; with != want {
		t.Errorf("under a profile = %q, want %q: tool and version take none, and the four that do are all gated by it", with, want)
	}
}

// And when nothing in the plugin takes a profile, the grant covers no call at
// all, which the receipt says instead of saying nothing.
func TestAGrantNamingAProfileOnAPluginThatTakesNoneSaysItCoversNoCall(t *testing.T) {
	web := func() []plugin.Capability {
		return []plugin.Capability{
			{ID: "web.get", Safety: plugin.Read, NeedsGrant: true},
			{ID: "web.status", Safety: plugin.Read, NeedsGrant: true},
		}
	}
	got := breadthNote(web, core.Grant{Target: "web", Profile: "staging"})
	if want := "note: no capability of web takes a profile, so a grant naming staging covers no call — leave the profile out"; got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
	if got := breadthNote(web, core.Grant{Target: "web"}); !strings.HasPrefix(got, "note: web covers 2 capabilities") {
		t.Errorf("without a profile the same grant covers both: %q", got)
	}
	if got := breadthNote(web, core.Grant{Target: "nosuch", Profile: "staging"}); got != "" {
		t.Errorf("a plugin that does not exist is told %q", got)
	}
}

// The same count, as the receipt of the command says it.
func TestTheReceiptCountsWhatTheProfiledGrantCovers(t *testing.T) {
	envSetup(t)
	v, err := runAllow(context.Background(), plugin.NewRequest(map[string]any{
		"target": "db", "profile": "staging", "ttl": "1h", "agent": "test",
	}, true, true).WithSurface(plugin.SurfaceCLI), envCatalog, builtIn)
	if err != nil {
		t.Fatal(err)
	}
	if body := v.(view.Text).Body; !strings.Contains(body, "db covers 4 capabilities") {
		t.Errorf("the receipt says %q", body)
	}
}

// What a grant reveals is counted the same way: a reveal that takes no profile
// is not one a profiled grant lets an agent read.
func TestAProfiledGrantCountsOnlyTheRevealsItCanCover(t *testing.T) {
	if n := revealNote(revealingCatalog, core.Grant{Target: "vault"}); !strings.Contains(n, "vault covers 2 capabilities that reveal") {
		t.Fatalf("with no profile = %q", n)
	}
	if n := revealNote(revealingCatalog, core.Grant{Target: "vault", Profile: "staging"}); n != "" {
		t.Errorf("under a profile = %q: none of them takes one, so none is covered", n)
	}
	got := revealNote(envCatalog, core.Grant{Target: "vault", Profile: "staging"})
	if !strings.Contains(got, "vault.get reveals the stored value itself") {
		t.Errorf("a reveal that takes a profile = %q", got)
	}
}
