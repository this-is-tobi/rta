package grant

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/config"
	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// instancedConfig writes a trusted config whose staging profile holds two kv
// connections and no default.
func instancedConfig(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `
profiles:
  staging:
    plugins:
      kv/main:
        set: {path: /main}
      kv/scratch:
        set: {path: /scratch}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", path)
}

// Consent must name which connection: a bare name over several labeled
// instances is refused with the refs a grant would use, never resolved by
// sort order into a consent nobody gave.
func TestAllowRefusesABareNameOverSeveralInstances(t *testing.T) {
	setup(t)
	instancedConfig(t)

	_, err := allowH(context.Background(), req(map[string]any{
		"target": "kv.get", "scope": "db-password", "profile": "staging"}))
	if err == nil {
		t.Fatal("a bare profile name was granted over two labeled instances")
	}
	verr, ok := err.(*view.Error)
	if !ok {
		t.Fatalf("refusal is %T, want *view.Error", err)
	}
	if !strings.Contains(verr.Hint, "staging/main") || !strings.Contains(verr.Hint, "staging/scratch") {
		t.Errorf("refusal does not list the instance refs: %q", verr.Hint)
	}
}

// A mistyped label is told what exists, and issues nothing.
func TestAllowNamesTheInstancesWhenALabelMisses(t *testing.T) {
	setup(t)
	instancedConfig(t)

	_, err := allowH(context.Background(), req(map[string]any{
		"target": "kv.get", "scope": "db-password", "profile": "staging/missing"}))
	if err == nil {
		t.Fatal("an unknown instance was granted")
	}
	if grants, _ := core.Load(); len(grants) != 0 {
		t.Errorf("a refused allow still stored %+v", grants)
	}
}

// An instance grant stores the full ref and pins that instance's connection —
// two instances must never share a pin, or consent for one would keep
// matching after being re-aimed at the other.
func TestAllowPinsTheNamedInstance(t *testing.T) {
	setup(t)
	instancedConfig(t)

	run(t, allowH, map[string]any{"target": "kv.get", "scope": "db-password", "profile": "staging/main"})
	run(t, allowH, map[string]any{"target": "kv.get", "scope": "db-password", "profile": "staging/scratch"})

	grants, verr := core.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(grants) != 2 {
		t.Fatalf("grants = %+v, want two", grants)
	}
	byRef := map[string]string{}
	for _, g := range grants {
		byRef[g.Profile] = g.ProfilePin
	}
	main, scratch := byRef["staging/main"], byRef["staging/scratch"]
	if main == "" || scratch == "" {
		t.Fatalf("refs not stored as given: %+v", byRef)
	}
	if main == scratch {
		t.Error("two instances share a ProfilePin")
	}
}

// Completion offers the refs a grant would accept: offering only the name
// would complete straight into the instance-required refusal.
func TestProfileCompletionOffersInstanceRefs(t *testing.T) {
	setup(t)
	instancedConfig(t)

	got := suggestConfiguredProfiles(context.Background(),
		plugin.NewRequest(map[string]any{"target": "kv.get"}, false, false))
	want := map[string]bool{"staging/main": false, "staging/scratch": false}
	for _, ref := range got {
		if _, ok := want[ref]; ok {
			want[ref] = true
		}
		if ref == "staging" {
			t.Errorf("completion offered the bare name a grant would refuse: %v", got)
		}
	}
	for ref, seen := range want {
		if !seen {
			t.Errorf("completion is missing %q: %v", ref, got)
		}
	}
	if _, err := config.Load(); err != nil {
		t.Fatal(err)
	}
}

// An environment's name is every connection in it, for the commands that take
// grants back or move them. `grant revoke --profile staging` answered "no
// active grant" beside a roster of staging/main and staging/scratch rows, so
// the one command that takes an environment back left every instance of it
// standing. A connection's whole reference still names that connection alone,
// a neighbour whose name merely begins the same way is not inside it, and
// --exact names exactly what it is given.
func TestAnEnvironmentNameTakesEveryConnectionInIt(t *testing.T) {
	setup(t)
	instancedConfig(t)
	now := time.Now()
	held := func(profile string) core.Grant {
		return core.Grant{Target: "kv.get", Profile: profile, Agent: "test", Issued: now, Expires: now.Add(time.Hour)}
	}
	reissue := func() {
		t.Helper()
		if verr := core.Save([]core.Grant{held("staging/main"), held("staging/scratch"), held("staging-db"), held("")}); verr != nil {
			t.Fatal(verr)
		}
	}
	profiles := func() []string {
		var out []string
		for _, g := range standing(t) {
			out = append(out, g.Profile)
		}
		slices.Sort(out)
		return out
	}

	reissue()
	run(t, runRevoke, map[string]any{"profile": "staging/main", "agent": "test"})
	if got := profiles(); !slices.Equal(got, []string{"", "staging-db", "staging/scratch"}) {
		t.Fatalf("revoking one connection left %q, want only staging/main gone", got)
	}

	reissue()
	body := run(t, runRevoke, map[string]any{"profile": "staging", "agent": "test"}).(view.Text).Body
	if !strings.HasPrefix(body, "revoked 2 grants") {
		t.Errorf("revoking the environment said %q, want both of its connections", body)
	}
	if got := profiles(); !slices.Equal(got, []string{"", "staging-db"}) {
		t.Fatalf("revoking the environment left %q, want every staging connection gone and its neighbours kept", got)
	}

	reissue()
	run(t, runRevoke, map[string]any{"target": "kv.get", "profile": "staging", "agent": "test", "exact": true})
	if got := profiles(); len(got) != 4 {
		t.Fatalf("an exact revoke of the bare environment name took %q, want none: it names no grant", got)
	}

	body = run(t, renewH, map[string]any{"profile": "staging", "ttl": "3h"}).(view.Text).Body
	if !strings.HasPrefix(body, "renewed 2 grants") {
		t.Errorf("renewing the environment said %q, want both of its connections", body)
	}
	for _, g := range standing(t) {
		extended := time.Until(g.Expires) > 2*time.Hour
		if want := config.RefName(g.Profile) == "staging"; extended != want {
			t.Errorf("after renewing staging, %q has %s left", g.Profile, time.Until(g.Expires).Round(time.Minute))
		}
	}
}

// Completion offers the environment beside the connections held in it.
func TestHeldProfileCompletionOffersTheEnvironmentToo(t *testing.T) {
	setup(t)
	now := time.Now()
	if verr := core.Save([]core.Grant{{Target: "kv.get", Profile: "staging/main", Agent: "test",
		Issued: now, Expires: now.Add(time.Hour)}}); verr != nil {
		t.Fatal(verr)
	}
	got := suggestHeldProfiles(context.Background(), plugin.NewRequest(nil, false, false))
	if !slices.Equal(got, []string{"staging", "staging/main"}) {
		t.Errorf("completion = %q, want the environment and the connection held in it", got)
	}
}
