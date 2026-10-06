package grant

import (
	"context"
	"strings"
	"testing"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func revealingCatalog() []plugin.Capability {
	return []plugin.Capability{
		{ID: "vault.item.get", Summary: "the value of a record", Safety: plugin.Write, NeedsGrant: true,
			Scope: "key", Reveals: true, Inputs: []plugin.Field{{Name: "key", Type: plugin.String, Positional: true}}},
		{ID: "vault.item.env", Summary: "records as exports", Safety: plugin.Write, NeedsGrant: true,
			Scope: "key", Reveals: true, Inputs: []plugin.Field{{Name: "key", Type: plugin.String, Positional: true}}},
		{ID: "vault.item.show", Summary: "what a record is", Safety: plugin.Write, NeedsGrant: true,
			Scope: "key", Inputs: []plugin.Field{{Name: "key", Type: plugin.String, Positional: true}}},
		{ID: "vault.item.edit", Summary: "edit by hand", Safety: plugin.Write, HumanOnly: true, Reveals: true},
	}
}

// A grant on a capability that reveals, with no record named, is every record it
// reaches, and the person typing it is told so while the clock is running.
func TestAGrantWithNoRecordOnARevealSaysItRevealsEveryRecord(t *testing.T) {
	g := core.Grant{Target: "vault.item.get"}
	got := revealNote(revealingCatalog, g)
	if !strings.Contains(got, "vault.item.get reveals the stored value itself") ||
		!strings.Contains(got, "every record it reaches") {
		t.Errorf("the note is %q", got)
	}
	if n := revealNote(revealingCatalog, core.Grant{Target: "vault.item.get", Scope: "prod/db"}); n != "" {
		t.Errorf("a grant naming its record is told it reveals every record: %q", n)
	}
	if n := revealNote(revealingCatalog, core.Grant{Target: "vault.item.show"}); n != "" {
		t.Errorf("a masked sibling is told it reveals: %q", n)
	}
}

// "vault" does not read like a grant to read every secret in it, so a plugin-wide
// grant counts the reveals it carries, and leaves out the ones an agent never
// reaches.
func TestAPluginWideGrantCountsTheRevealsItCarries(t *testing.T) {
	got := revealNote(revealingCatalog, core.Grant{Target: "vault"})
	if !strings.Contains(got, "vault covers 2 capabilities that reveal stored values (vault.item.env, vault.item.get)") {
		t.Errorf("the note is %q", got)
	}
	if strings.Contains(got, "vault.item.edit") {
		t.Errorf("a human-only capability is counted: %q", got)
	}
}

// Said by the command an operator types, on a dry run too, where finding it out
// is what the dry run is for.
func TestGrantAllowSaysSoOnTheReceipt(t *testing.T) {
	setup(t)
	v, err := runAllow(context.Background(),
		plugin.NewRequest(map[string]any{"target": "vault.item.get", "ttl": "1h", "agent": "test"}, true, true).
			WithSurface(plugin.SurfaceCLI), revealingCatalog, func(string) (string, bool) { return "", true })
	if err != nil {
		t.Fatal(err)
	}
	if body := v.(view.Text).Body; !strings.Contains(body, "reveals the stored value itself") {
		t.Errorf("the receipt of an unscoped reveal grant says %q", body)
	}
}
