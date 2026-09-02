package kv

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// usedByConfig writes a config whose profiles read two of the store's
// entries, into the file setup already isolated.
func usedByConfig(t *testing.T) {
	t.Helper()
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{
			"pg@aaaa": {Secrets: map[string]string{"password": "kv:db-pass"}},
		}},
		"prod": {Plugins: map[string]config.Connection{
			"pg@aaaa": {Secrets: map[string]string{"password": "kv:db-pass"}},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
}

func listTable(t *testing.T, r plugin.Request) view.Table {
	t.Helper()
	v, err := runList(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("runList returned %T, want a table", v)
	}
	return tbl
}

// A listing at the terminal says which environments depend on each entry —
// the join between the store and the config file that names its entries.
func TestListNamesTheProfilesUsingAnEntry(t *testing.T) {
	setup(t)
	usedByConfig(t)
	for _, kv := range []map[string]any{
		{"key": "db-pass", "value": "s3cret"},
		{"key": "scratch", "value": "note"},
	} {
		if _, err := runSet(context.Background(), req(kv, false)); err != nil {
			t.Fatal(err)
		}
	}

	tbl := listTable(t, req(nil, false))
	col := -1
	for i, c := range tbl.Columns {
		if c.Name == "Used by" {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("no Used by column in %v", tbl.Columns)
	}
	rows := map[string]string{}
	for _, r := range tbl.Rows {
		rows[r[0]] = r[col]
	}
	if rows["db-pass"] != "prod, staging" {
		t.Errorf("db-pass used by %q, want %q", rows["db-pass"], "prod, staging")
	}
	if rows["scratch"] != "" {
		t.Errorf("scratch used by %q, want nothing", rows["scratch"])
	}
}

// Over MCP the column does not exist: an agent listing the store learns
// names and kinds, and which entry authenticates which environment is
// reconnaissance it has no call for.
func TestListWithholdsProfileUsersOverMCP(t *testing.T) {
	setup(t)
	usedByConfig(t)
	if _, err := runSet(context.Background(), req(map[string]any{"key": "db-pass", "value": "s3cret"}, false)); err != nil {
		t.Fatal(err)
	}

	tbl := listTable(t, req(nil, false).WithSurface(plugin.SurfaceMCP))
	for _, c := range tbl.Columns {
		if c.Name == "Used by" {
			t.Fatalf("the MCP listing carries the Used by column: %v", tbl.Columns)
		}
	}
	for _, r := range tbl.Rows {
		for _, cell := range r {
			if strings.Contains(cell, "staging") || strings.Contains(cell, "prod") {
				t.Errorf("an MCP row names a profile: %v", r)
			}
		}
	}
}

// A store nothing references keeps its old shape: no column of blanks naming
// a concept the operator never used.
func TestListOmitsTheColumnWhenNothingIsReferenced(t *testing.T) {
	setup(t)
	if _, err := runSet(context.Background(), req(map[string]any{"key": "scratch", "value": "note"}, false)); err != nil {
		t.Fatal(err)
	}
	for _, c := range listTable(t, req(nil, false)).Columns {
		if c.Name == "Used by" {
			t.Fatalf("Used by column present with no profile referencing anything")
		}
	}
}

// kv.show makes the same join for one entry.
func TestShowNamesTheProfilesUsingAnEntry(t *testing.T) {
	setup(t)
	usedByConfig(t)
	if _, err := runSet(context.Background(), req(map[string]any{"key": "db-pass", "value": "s3cret"}, false)); err != nil {
		t.Fatal(err)
	}
	v, err := runShow(context.Background(), req(map[string]any{"key": "db-pass"}, false))
	if err != nil {
		t.Fatal(err)
	}
	pairs := v.(view.KeyValue).Pairs
	found := ""
	for _, p := range pairs {
		if p.Key == "used by" {
			found = p.Value
		}
	}
	if found != "prod, staging" {
		t.Errorf("show's used by = %q, want %q", found, "prod, staging")
	}

	mcp, err := runShow(context.Background(), req(map[string]any{"key": "db-pass"}, false).WithSurface(plugin.SurfaceMCP))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range mcp.(view.KeyValue).Pairs {
		if p.Key == "used by" {
			t.Errorf("MCP show carries the used by pair: %q", p.Value)
		}
	}
}
