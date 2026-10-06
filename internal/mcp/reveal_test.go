package mcp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

const revealedValue = "the-stored-value-9f3a1c"

// revealRegistry is a store with a masked read and the capability that is its
// reveal: the same records, one answering with the value and one without.
func revealRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	key := plugin.Field{Name: "key", Type: plugin.String, Required: true, Positional: true, Help: "which record"}
	err := reg.Register(plugin.Plugin{
		Name: "vault", Summary: "a store",
		Capabilities: []plugin.Capability{
			{
				ID: "vault.item.show", Summary: "what a record is, without its value",
				Safety: plugin.Write, NeedsGrant: true, Scope: "key", Idempotent: true,
				Inputs: []plugin.Field{key},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.KeyValue{Pairs: []view.Pair{{Key: "value", Value: view.Mask}}, Redacted: []string{"value"}}, nil
				},
			},
			{
				ID: "vault.item.get", Summary: "the value of a record",
				Safety: plugin.Write, NeedsGrant: true, Scope: "key", Idempotent: true, Reveals: true,
				Inputs: []plugin.Field{key},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: revealedValue}, nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func allowRecord(t *testing.T, target, scope string, uses int) {
	t.Helper()
	grants, verr := grant.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if verr := grant.Save(append(grants, grant.Grant{
		Target: target, Scope: scope,
		Issued: time.Now(), Expires: time.Now().Add(time.Hour), MaxUses: uses,
	})); verr != nil {
		t.Fatal(verr)
	}
}

// The reveal is not a new gate: it is the grant that already exists, and every
// control of it holds. A call refused without a grant, a grant on the masked
// sibling, a grant on another record and a spent one-time grant each refuse it;
// the one call that ran says it revealed, in the row that names the capability,
// the record and how it was authorized.
func TestARevealIsHeldToTheGrantThatAlreadyExists(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	s := connectWith(t, revealRegistry(t), Options{})
	get := func() string {
		res := callTool(t, s, "vault_item_get", map[string]any{"key": "prod/db"})
		text := res.Content[0].(*sdk.TextContent).Text
		if res.IsError {
			return "refused: " + text
		}
		return text
	}

	if got := get(); !strings.Contains(got, "core.grant.required") {
		t.Fatalf("a reveal with no grant: %s", got)
	}
	allowRecord(t, "vault.item.show", "prod/db", 0)
	if got := get(); !strings.Contains(got, "core.grant.required") {
		t.Fatalf("a grant on the masked sibling covered the reveal: %s", got)
	}
	allowRecord(t, "vault.item.get", "prod/other", 0)
	if got := get(); !strings.Contains(got, "core.grant.required") {
		t.Fatalf("a grant on record A covered record B: %s", got)
	}
	allowRecord(t, "vault.item.get", "prod/db", 1)
	if got := get(); !strings.Contains(got, revealedValue) {
		t.Fatalf("the granted reveal did not answer: %s", got)
	}
	if got := get(); !strings.Contains(got, "core.grant.required") {
		t.Fatalf("a one-time grant covered a second reveal: %s", got)
	}

	entries, err := agentlog.Read(0)
	if err != nil {
		t.Fatal(err)
	}
	var ran []agentlog.Entry
	for _, e := range entries {
		if e.Revealed && e.Outcome != agentlog.Ran {
			t.Errorf("a %s call is recorded as having revealed a value: %+v", e.Outcome, e)
		}
		if e.Outcome == agentlog.Ran {
			ran = append(ran, e)
		}
	}
	if len(ran) != 1 {
		t.Fatalf("%d calls ran, want the one granted reveal: %+v", len(ran), entries)
	}
	got := ran[0]
	if got.Cap != "vault.item.get" || !got.Revealed || got.Auth != agentlog.Standing ||
		len(got.Records) != 1 || got.Records[0] != "prod/db" {
		t.Errorf("the row of the reveal says %+v, want the capability, the record, a standing grant and revealed", got)
	}
}

// What the row says is that a value went out, and the value is in the answer
// and nowhere else: the record is read by people and by the next agent that
// greps it.
func TestTheRecordOfARevealNeverHoldsTheValue(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	s := connectWith(t, revealRegistry(t), Options{})
	allowRecord(t, "vault.item.get", "prod/db", 0)
	if res := callTool(t, s, "vault_item_get", map[string]any{"key": "prod/db"}); res.IsError {
		t.Fatalf("the granted reveal was refused: %+v", res.Content)
	}
	files, err := agentlog.Segments()
	if err != nil || len(files) == 0 {
		t.Fatalf("no record to read: %v %v", files, err)
	}
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), revealedValue) {
			t.Errorf("%s holds the revealed value", path)
		}
		if !strings.Contains(string(b), `"revealed":true`) {
			t.Errorf("%s does not say a value was revealed", path)
		}
	}
}

// A value the operator's size cap held back did not go to the agent, and the
// row must not say it did.
func TestAWithheldRevealIsNotRecordedAsRevealed(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	s := connectWith(t, revealRegistry(t), Options{MaxResult: 5})
	allowRecord(t, "vault.item.get", "prod/db", 0)
	res := callTool(t, s, "vault_item_get", map[string]any{"key": "prod/db"})
	if !res.IsError {
		t.Fatalf("the answer came through a 5-byte cap: %+v", res.Content)
	}
	entries, err := agentlog.Read(1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("record: %v %v", entries, err)
	}
	if entries[0].Outcome != agentlog.Ran || entries[0].Revealed {
		t.Errorf("a withheld answer is recorded as %+v, want ran and not revealed", entries[0])
	}
}
