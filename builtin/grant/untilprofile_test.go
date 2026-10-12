package grant

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func needlessFor(t *testing.T, target string) *view.Error {
	t.Helper()
	completionSetup(t)
	_, err := runAllow(context.Background(), req(map[string]any{"target": target, "agent": "test"}), completionCatalog, builtIn)
	var verr *view.Error
	if err == nil || !asError(err, &verr) {
		t.Fatalf("allow %s = %v, want a refusal", target, err)
	}
	if verr.Code != "grant.needless" {
		t.Fatalf("allow %s = %+v, want it refused as needless", target, verr)
	}
	return verr
}

// A read needs no grant on the base connection and one on every other, so
// "agents can already call it" is true only until a profile names one — and a
// grant that names one is what somebody who typed a service's name was after.
func TestARefusalAsNeedlessSaysWhereAGrantDoesApply(t *testing.T) {
	one := needlessFor(t, "dir.user.list")
	if want := "dir.user.list needs no grant — it is a read, and agents can already call it until a profile names one"; one.Message != want {
		t.Errorf("message = %q, want %q", one.Message, want)
	}
	if want := "`rta grant allow dir.user.list --profile <name>`"; !strings.Contains(one.Hint, want) {
		t.Errorf("hint = %q, want it to offer %s", one.Hint, want)
	}
	all := needlessFor(t, "dir")
	if want := "dir needs no grant — everything in it is a read, and agents can already call those until a profile names one"; all.Message != want {
		t.Errorf("message = %q, want %q", all.Message, want)
	}

	// A read that takes no profile never costs one, and the sentence stands.
	free := needlessFor(t, "db.version")
	if free.Message != "db.version needs no grant — it is a read, and agents can already call it" || strings.Contains(free.Hint, "--profile") {
		t.Errorf("a read that takes no profile = %+v", free)
	}
}

func tierTitles(t *testing.T, cat func() []plugin.Capability) map[string]string {
	t.Helper()
	completionSetup(t)
	v, err := runList(context.Background(), req(map[string]any{"detail": true}), cat, builtIn)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]string{}
	for _, item := range v.(view.Sections).Items {
		titles[item.Key()] = item.Title
	}
	return titles
}

// The detail page says what an agent reaches with no grant at all, and a read
// that takes a profile is that only until one is named.
func TestTheDetailPageSaysWhereReachableByDefaultEnds(t *testing.T) {
	if got := tierTitles(t, completionCatalog)["default"]; got != "reachable by default, until a profile names one" {
		t.Errorf("title = %q", got)
	}
	// With nothing in the tier that takes a profile there is nothing to
	// qualify, and the title is what it always was.
	reads := func() []plugin.Capability {
		return []plugin.Capability{{ID: "db.version", Safety: plugin.Read}, {ID: "db.tool", Safety: plugin.Write}}
	}
	titles := tierTitles(t, reads)
	if titles["default"] != "reachable by default" || titles["grant"] != "needs a grant a person issues" {
		t.Errorf("titles = %v", titles)
	}
}
