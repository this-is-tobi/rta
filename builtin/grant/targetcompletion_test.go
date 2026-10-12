package grant

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// completionCatalog is what target completion is asked about: a directory
// plugin of nothing but reads that takes a profile, a database plugin with a
// read, a free read that takes none and a write that takes none, a capability
// for the person at the terminal, and a plugin no environment configures.
func completionCatalog() []plugin.Capability {
	conn := []plugin.Field{{Name: "dsn", Type: plugin.String, Config: "dsn"}}
	return []plugin.Capability{
		{ID: "dir.user.list", Summary: "List users", Safety: plugin.Read, Inputs: conn},
		{ID: "dir.group.list", Summary: "List groups", Safety: plugin.Read, Inputs: conn},
		{ID: "db.status", Summary: "Database status", Safety: plugin.Read, Inputs: conn},
		{ID: "db.version", Summary: "Server version", Safety: plugin.Read},
		{ID: "db.tool", Summary: "Run a tool", Safety: plugin.Write},
		{ID: "db.dump", Summary: "Dump it", Safety: plugin.Write, HumanOnly: true, Inputs: conn},
		{ID: "net.probe", Summary: "Probe a host", Safety: plugin.Read, Inputs: conn},
	}
}

const completionConfig = `
profiles:
  staging:
    plugins:
      dir:
        set: {dsn: directory}
      db:
        set: {dsn: database}
`

// offered is the targets `grant allow` completes, by value, for the values
// typed so far.
func offered(t *testing.T, values map[string]any) []string {
	t.Helper()
	var target plugin.Field
	for _, c := range Plugin(completionCatalog, builtIn).Capabilities {
		if c.ID != "grant.allow" {
			continue
		}
		for _, f := range c.Inputs {
			if f.Name == "target" {
				target = f
			}
		}
	}
	var out []string
	for _, c := range target.Candidates(context.Background(), req(values)) {
		out = append(out, plugin.CandidateValue(c))
	}
	slices.Sort(out)
	return out
}

func completionSetup(t *testing.T) {
	t.Helper()
	setup(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, completionConfig)
	t.Setenv("RTA_CONFIG", path)
}

// **A plugin of nothing but reads needs a grant the moment a profile names
// it**, and target completion hid it: it asked each capability whether it
// needs a grant with no profile, so a directory plugin whose every capability
// is a read was never offered, however many environments configured it. What
// is offered now is what a grant could be spent on — a capability that needs
// one outright, and one that takes a profile and is configured by an
// environment, where the profile is what it needs the grant for.
func TestAPluginOfReadsAnEnvironmentConfiguresIsOffered(t *testing.T) {
	completionSetup(t)
	got := offered(t, nil)
	want := []string{"db", "db.status", "db.tool", "dir", "dir.group.list", "dir.user.list"}
	if !slices.Equal(got, want) {
		t.Errorf("targets offered = %q, want %q: a profile makes the directory's reads grantable, and nothing configures net", got, want)
	}
}

// With the profile typed the question has its answer: what that environment
// configures and a call can name it on, and nothing that would be a grant no
// call could spend.
func TestTargetsAreOfferedForTheProfileTyped(t *testing.T) {
	completionSetup(t)
	got := offered(t, map[string]any{"profile": "staging/analytics"})
	want := []string{"db", "db.status", "dir", "dir.group.list", "dir.user.list"}
	if !slices.Equal(got, want) {
		t.Errorf("targets offered = %q, want %q: db.tool takes no profile, so a grant naming one covers no call of it", got, want)
	}
}

// A profile that is not one falls back to what is true without it, rather
// than offering nothing to somebody who has only mistyped.
func TestAProfileThatIsNoneOfOursOffersWhatNeedsNoProfile(t *testing.T) {
	completionSetup(t)
	got := offered(t, map[string]any{"profile": "prod"})
	want := []string{"db", "db.status", "db.tool", "dir", "dir.group.list", "dir.user.list"}
	if !slices.Equal(got, want) {
		t.Errorf("targets offered = %q, want %q", got, want)
	}
}

// With no environment configured, what a grant needs outright is all there is.
func TestWithNoEnvironmentOnlyWhatNeedsAGrantIsOffered(t *testing.T) {
	setup(t)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	got := offered(t, nil)
	if want := []string{"db", "db.tool"}; !slices.Equal(got, want) {
		t.Errorf("targets offered = %q, want %q", got, want)
	}
}
