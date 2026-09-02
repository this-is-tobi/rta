package grant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	core "github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
