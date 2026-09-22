package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/policy"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The small helpers behind the command tree's sentences and completions,
// which no test called: each is one rule, and a wrong one prints a wrong
// word to a person or seeds a wrong value into a form.

func TestAConnectionsAddressIsTheMostAddressLikeThingItStates(t *testing.T) {
	for _, tc := range []struct {
		conn config.Connection
		want string
	}{
		{config.Connection{Kube: "homelab/db/svc/pg:5432", Set: map[string]any{"host": "x"}}, "homelab/db/svc/pg:5432"},
		{config.Connection{SSH: "bastion/pg:5432"}, "bastion/pg:5432"},
		{config.Connection{Set: map[string]any{"port": 5432, "host": "prod.internal"}}, "prod.internal"},
		{config.Connection{Set: map[string]any{"bucket": "backups", "url": "https://s3"}}, "https://s3"},
		{config.Connection{Set: map[string]any{"port": 5432}}, "the default"},
		{config.Connection{}, "the default"},
	} {
		if got := connAddress(tc.conn, "the default"); got != tc.want {
			t.Errorf("connAddress(%+v) = %q, want %q", tc.conn, got, tc.want)
		}
	}
}

func TestATileNeedsNoInputARequiredOneWithoutADefaultWouldAskFor(t *testing.T) {
	free := plugin.Capability{ID: "a.b", Inputs: []plugin.Field{
		{Name: "n", Type: plugin.Int, Required: true, Default: 3},
		{Name: "s", Type: plugin.String},
	}}
	asks := plugin.Capability{ID: "c.d", Inputs: []plugin.Field{{Name: "host", Type: plugin.String, Required: true}}}
	if hasRequiredInputs(free) || !hasRequiredInputs(asks) {
		t.Errorf("hasRequiredInputs: free=%v asks=%v", hasRequiredInputs(free), hasRequiredInputs(asks))
	}
	tiles := []config.Tile{{ID: "sys.cpu"}, {ID: "pg.overview", Profile: "prod"}}
	if got := strings.Join(tileIDs(tiles), " "); got != "sys.cpu pg.overview" {
		t.Errorf("tileIDs = %q", got)
	}
}

func TestTheOneWordHelpersSayTheRightWord(t *testing.T) {
	if yesNo(true) != "yes" || yesNo(false) != "no" {
		t.Error("yesNo")
	}
	if pick(1, "entry", "entries") != "entry" || pick(2, "entry", "entries") != "entries" || pick(0, "entry", "entries") != "entries" {
		t.Error("pick")
	}
	if !containsArg([]string{"--all", "-v"}, "--all") || containsArg([]string{"--all"}, "-v") {
		t.Error("containsArg")
	}
	if got := needLine(plugin.Need("nothing-of-the-kind")); got != "nothing-of-the-kind" {
		t.Errorf("needLine of a need with no path = %q, want the bare need", got)
	}
	dir := t.TempDir()
	present := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(present, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := presentText(present); got != present {
		t.Errorf("presentText of a present file = %q", got)
	}
	missing := filepath.Join(dir, "absent.yaml")
	if got := presentText(missing); got != missing+" (not present)" {
		t.Errorf("presentText of a missing file = %q", got)
	}
	if got := repoPolicyText(policy.Ceiling{RepoFound: true, From: []string{"a", "b"}}); got != "a, b" {
		t.Errorf("repoPolicyText found = %q", got)
	}
	if got := repoPolicyText(policy.Ceiling{SearchedFrom: "/work"}); got != "none found walking up from /work" {
		t.Errorf("repoPolicyText absent = %q", got)
	}
}

func TestAnInstallReportDescribesWhatAPluginDeclares(t *testing.T) {
	reads := plugin.Plugin{Name: "db", Capabilities: []plugin.Capability{
		{ID: "db.status", Safety: plugin.Read},
		{ID: "db.list", Safety: plugin.Read},
	}}
	if got := declaresLine(reads); got != "db.list, db.status — all read · none needs a grant" {
		t.Errorf("declaresLine(all read) = %q", got)
	}
	mixed := plugin.Plugin{Name: "db", Capabilities: []plugin.Capability{
		{ID: "db.status", Safety: plugin.Read},
		{ID: "db.drop", Safety: plugin.Destructive, NeedsGrant: true},
	}}
	got := declaresLine(mixed)
	if !strings.HasPrefix(got, "db.drop, db.status — ") || !strings.Contains(got, "1 read") ||
		!strings.Contains(got, "1 destructive") || !strings.HasSuffix(got, "1 needs a grant") {
		t.Errorf("declaresLine(mixed) = %q", got)
	}
	if firstCapability(mixed) != "db.drop" || firstCapability(plugin.Plugin{Name: "empty"}) != "empty" {
		t.Error("firstCapability")
	}
}

func TestTheCredentialVariablesAreTheOnesSomethingReads(t *testing.T) {
	p := plugin.Plugin{Name: "db", Capabilities: []plugin.Capability{
		{ID: "db.query", Inputs: []plugin.Field{
			{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true},
			{Name: "token", Type: plugin.Secret, Local: true},
			{Name: "host", Type: plugin.String, Local: true, EnvFallback: true},
		}},
		{ID: "db.dump", Inputs: []plugin.Field{
			{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true},
		}},
	}}
	// One variable, not two: it is derived per plugin, so the same input on
	// two capabilities is one channel and is named once.
	want := "$" + plugin.LocalEnvVar("db.query", "password")
	if got := credentialVars(p); strings.Join(got, " ") != want {
		t.Errorf("credentialVars = %v, want [%s] — the secret input with an environment fallback, once", got, want)
	}
}

func TestTheFirstIndexProblemIsRaisedWithTheCountOfTheRest(t *testing.T) {
	one := view.Errorf("plugin.index.unreadable", "community is not an index").WithHint("run `rta plugin index update`")
	if got := withOthers([]*view.Error{one}); got.Hint != one.Hint {
		t.Errorf("one problem: hint = %q, want it untouched", got.Hint)
	}
	// "2 more attached indexes", the word alone after the number: the hint
	// used to read "2 more attached 2 indexes".
	got := withOthers([]*view.Error{one, view.Errorf("x", "y"), view.Errorf("z", "w")})
	if !strings.HasPrefix(got.Hint, one.Hint+". ") || !strings.Contains(got.Hint, "2 more attached indexes could") {
		t.Errorf("three problems: hint = %q", got.Hint)
	}
	two := withOthers([]*view.Error{one, view.Errorf("x", "y")})
	if !strings.Contains(two.Hint, "1 more attached index could") {
		t.Errorf("two problems: hint = %q", two.Hint)
	}
	if got := asWarnings([]*view.Error{one}); len(got) != 1 || got[0].Code != one.Code {
		t.Errorf("asWarnings = %+v", got)
	}
}

func TestAnUnknownProfileNamesTheConfiguredOnes(t *testing.T) {
	none := unknownProfile(config.Config{}, "prod")
	if none.Code != "core.profile.unknown" || !strings.Contains(none.Hint, "no profiles are configured") {
		t.Errorf("no profiles: %+v", none)
	}
	some := unknownProfile(config.Config{Profiles: map[string]config.Profile{"staging": {}, "lab": {}}}, "prod")
	if some.Hint != "configured: lab, staging" {
		t.Errorf("some profiles: hint = %q", some.Hint)
	}
}
