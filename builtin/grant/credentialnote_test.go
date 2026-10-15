package grant

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Three entries a profile keeps from an agent: one shared by two profiles, one
// that only an instance of a plugin reads, and one under a folder.
const hiddenCredentials = `
profiles:
  staging:
    plugins:
      pg:
        set: {host: db.staging.internal}
        secrets: {password: "kv:staging-db-password"}
      pg/analytics:
        set: {host: analytics.staging.internal}
        secrets: {password: "kv:analytics-password"}
  prod:
    plugins:
      pg:
        set: {host: db.prod.internal}
        secrets: {password: "kv:prod/db-password"}
      redis:
        set: {host: cache.prod.internal}
        secrets: {password: "kv:staging-db-password"}
`

// kvCatalog is the store as an agent meets it: two capabilities that read a
// value, scoped to the record they name, and one that only removes one.
func kvCatalog() []plugin.Capability {
	key := []plugin.Field{{Name: "key", Type: plugin.String}}
	return []plugin.Capability{
		{ID: "kv.get", Safety: plugin.Write, NeedsGrant: true, Reveals: true, Scope: "key", Inputs: key},
		{ID: "kv.env", Safety: plugin.Write, NeedsGrant: true, Reveals: true, Scope: "key", Inputs: key},
		{ID: "kv.rm", Safety: plugin.Destructive, Scope: "key", Inputs: key},
	}
}

func allowReceipt(t *testing.T, catalog func() []plugin.Capability, values map[string]any) string {
	t.Helper()
	v, err := runAllow(context.Background(), req(values), catalog, builtIn)
	if err != nil {
		t.Fatalf("allow %v: %v", values, err)
	}
	return v.(view.Text).Body
}

// The developer recipe paired a grant on the profile with `grant allow kv.get
// staging-db-password`, the password the profile keeps from the agent, and the
// agent read it. Each grant is right alone, so nothing refuses; the receipt of
// the second says what it undoes, naming the profile.
func TestAGrantOnTheEntryAProfileKeepsFromTheAgentSaysSo(t *testing.T) {
	configDir, _ := roleSetup(t)
	writeFile(t, filepath.Join(configDir, "config.yaml"), hiddenCredentials)

	body := allowReceipt(t, kvCatalog, map[string]any{"target": "kv.get", "scope": "staging-db-password"})
	for _, want := range []string{
		"note: kv entry staging-db-password is kept from the agent by profiles prod and staging, and this grant lets it read it",
		"a grant on the profile lets it use the connection without reading the credential",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the receipt does not say %q:\n%s", want, body)
		}
	}

	// A connection that is an instance of its plugin is named the way a grant
	// names it, not by the profile alone.
	if body := allowReceipt(t, kvCatalog, map[string]any{"target": "kv.env", "scope": "analytics-password"}); !strings.Contains(body, "kept from the agent by profile staging/analytics,") {
		t.Errorf("an instance's credential is not named by the instance:\n%s", body)
	}

	// An entry no profile maps is an ordinary secret, and says nothing.
	if body := allowReceipt(t, kvCatalog, map[string]any{"target": "kv.get", "scope": "deploy-token"}); strings.Contains(body, "kept from the agent") {
		t.Errorf("an entry no profile maps was warned about:\n%s", body)
	}
}

// A folder reaches every entry under it, and a grant naming no record or a
// whole plugin reaches all of them: the receipt counts what it reaches, by the
// rule the gate reads a record by.
func TestAWideGrantSaysHowManyCredentialsAProfileKeepsItReaches(t *testing.T) {
	configDir, _ := roleSetup(t)
	writeFile(t, filepath.Join(configDir, "config.yaml"), hiddenCredentials)

	if body := allowReceipt(t, kvCatalog, map[string]any{"target": "kv.get", "scope": "prod/"}); !strings.Contains(body,
		"kv entry prod/db-password is kept from the agent by profile prod, and this grant lets it read it") {
		t.Errorf("a folder holding one such entry does not name it:\n%s", body)
	}

	const all = "note: this grant lets the agent read 3 kv entries that profiles keep from it " +
		"(analytics-password for staging/analytics; prod/db-password for prod; staging-db-password for prod, staging)"
	for name, values := range map[string]map[string]any{
		"the whole plugin":        {"target": "kv"},
		"a capability, no record": {"target": "kv.get"},
		"a revealing sibling":     {"target": "kv.env"},
	} {
		if body := allowReceipt(t, kvCatalog, values); !strings.Contains(body, all) {
			t.Errorf("%s: the receipt does not count what the grant reaches:\n%s", name, body)
		}
	}
}

// The list is cut where a receipt stays a note, with the rest counted.
func TestTheListOfCredentialsAGrantReachesIsCut(t *testing.T) {
	configDir, _ := roleSetup(t)
	var yaml strings.Builder
	yaml.WriteString("profiles:\n")
	for i := range 6 {
		fmt.Fprintf(&yaml, "  env%d:\n    plugins:\n      pg:\n        secrets: {password: \"kv:secret-%d\"}\n", i, i)
	}
	writeFile(t, filepath.Join(configDir, "config.yaml"), yaml.String())

	body := allowReceipt(t, kvCatalog, map[string]any{"target": "kv"})
	if !strings.Contains(body, "read 6 kv entries that profiles keep from it (secret-0 for env0; secret-1 for env1; secret-2 for env2 and 3 others)") {
		t.Errorf("the receipt does not cut the list:\n%s", body)
	}
}

// Only a grant that lets an agent read the store can hand it a credential kept
// there: one that removes an entry, or reads another plugin's store, is not one,
// whatever its scope would cover among these entries.
func TestAGrantThatReadsNoStoreEntryIsNotWarned(t *testing.T) {
	configDir, _ := roleSetup(t)
	writeFile(t, filepath.Join(configDir, "config.yaml"), hiddenCredentials)

	vault := func() []plugin.Capability {
		return []plugin.Capability{{ID: "vault.get", Safety: plugin.Write, NeedsGrant: true, Reveals: true, Scope: "key",
			Inputs: []plugin.Field{{Name: "key", Type: plugin.String}}}}
	}
	for name, tc := range map[string]struct {
		catalog func() []plugin.Capability
		values  map[string]any
	}{
		"a capability that removes":   {kvCatalog, map[string]any{"target": "kv.rm", "scope": "staging-db-password"}},
		"another plugin's reveal":     {vault, map[string]any{"target": "vault.get"}},
		"another plugin, plugin-wide": {vault, map[string]any{"target": "vault"}},
	} {
		body := allowReceipt(t, tc.catalog, tc.values)
		if strings.Contains(body, "kept from the agent") || strings.Contains(body, "profiles keep from it") {
			t.Errorf("%s: warned about a credential this grant cannot read:\n%s", name, body)
		}
	}
}

// The operator channel says the same sentences, since it prepares the same
// grant for the same operator to sign.
func TestTheOperatorChannelWarnsOfAKeptCredentialToo(t *testing.T) {
	configDir, _ := roleSetup(t)
	writeFile(t, filepath.Join(configDir, "config.yaml"), hiddenCredentials)

	p, verr := PrepareRemote(kvCatalog, builtIn)(operatorid.IssueSpec{Target: "kv.get", Scope: "staging-db-password", Agent: "claude"}, "alice")
	if verr != nil {
		t.Fatal(verr)
	}
	if !strings.Contains(strings.Join(p.Notes, "\n"), "kv entry staging-db-password is kept from the agent by profiles prod and staging") {
		t.Errorf("the prepared grant carries no warning: %v", p.Notes)
	}
}
