package tui

import (
	"context"
	"maps"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// grantRowActions is x and n on grant.list, as the real registry declares
// them.
func grantRowActions(t *testing.T, m Model) []capAction {
	t.Helper()
	var out []capAction
	for _, a := range capActions(m.reg, "grant.list") {
		if a.src == srcRow {
			out = append(out, a)
		}
	}
	if len(out) != 2 {
		t.Fatalf("grant.list offers %d row actions, want renew and revoke", len(out))
	}
	return out
}

// The roster's readings are the roster's: a table another capability drew
// with a Record or Profile column of its own is seeded from its cells as
// they are, a dash read as no value and "any" as a record named any.
func TestOnlyTheRosterIsReadAsTheRoster(t *testing.T) {
	rm := plugin.Capability{ID: "demo.rm", Summary: "rm", Safety: plugin.Destructive,
		Inputs: []plugin.Field{
			{Name: "id", Type: plugin.String, Positional: true, Required: true},
			{Name: "record", Type: plugin.String},
			{Name: "profile", Type: plugin.String},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	tbl := view.Table{
		Columns: []view.Column{{Name: "Id"}, {Name: "Record"}, {Name: "Profile"}},
		Rows: [][]string{
			{"1", "any", "staging (changed)"},
			{"2", "prod/ (all)", "—"},
		},
	}
	want := []map[string]any{
		{"id": "1", "record": "any", "profile": "staging (changed)"},
		{"id": "2", "record": "prod/ (all)"},
	}
	for i := range tbl.Rows {
		m := Model{row: i}
		base, ok := m.actionSeed(capAction{key: "x", label: "remove", cap: rm, src: srcRow, from: "demo.list"}, tbl)
		if !ok || !maps.Equal(base, want[i]) {
			t.Errorf("row %d seeded %v, want its cells as they are: %v", i, base, want[i])
		}
	}
}

// x and n on a grant row seed the record the grant holds, not the cell
// drawing it. The Record column says "any" for a grant naming no record,
// "prod/ (all)" for a folder, and quotes a padded record and one named like
// "any"; seeded as drawn, the revoke or renew they opened named a record no
// grant holds — or, for "any", a key literally named any — and took back or
// extended another grant, or none.
func TestARowActionOnAGrantSeedsTheRecordItHolds(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	m := Model{reg: realRegistry(t)}
	padded := "db" + string(rune(0xa0))
	now := time.Now()
	for _, record := range []string{"", "any", "prod/", padded, "prod/db"} {
		if verr := grant.Issue(grant.Grant{Target: "kv.get", Scope: record, Agent: "claude",
			Issued: now, Expires: now.Add(time.Hour)}, true); verr != nil {
			t.Fatal(verr)
		}
	}
	list, _ := m.reg.Capability("grant.list")
	v, err := list.Run(context.Background(), plugin.NewRequest(nil, false, true).WithSurface(plugin.SurfaceTUI))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("grant.list answered %s, want the roster table", view.TypeOf(v))
	}
	// The cell as drawn, and the record a command acting on its row names.
	want := map[string]string{
		"any":                        "",
		strconv.Quote("any"):         "any",
		"prod/ (all)":                "prod/",
		strconv.QuoteToASCII(padded): padded,
		"prod/db":                    "prod/db",
	}
	record := -1
	for i, c := range tbl.Columns {
		if c.Name == "Record" {
			record = i
		}
	}
	if record < 0 || len(tbl.Rows) != len(want) {
		t.Fatalf("roster = %v / %q, want a Record column and five rows", tbl.Columns, tbl.Rows)
	}
	for _, a := range grantRowActions(t, m) {
		for i, row := range tbl.Rows {
			expect, known := want[row[record]]
			if !known {
				t.Fatalf("the roster draws a record as %q, which this test does not know", row[record])
			}
			m.row = i
			base, ok := m.actionSeed(a, tbl)
			if !ok {
				t.Fatalf("%s on %q seeded nothing", a.cap.ID, row[record])
			}
			got, seeded := base["scope"]
			switch {
			case expect == "" && seeded:
				t.Errorf("%s on %q seeded scope %q, want none: the grant names no record", a.cap.ID, row[record], got)
			case expect != "" && got != expect:
				t.Errorf("%s on %q seeded scope %q, want %q", a.cap.ID, row[record], got, expect)
			}
			if base["target"] != "kv.get" || base["agent"] != "claude" {
				t.Errorf("%s on %q seeded %v, want the row's target and agent", a.cap.ID, row[record], base)
			}
		}
	}
}

// A connection repointed since issue is marked on the row, and the mark is
// not part of its name: "staging (changed)" seeded names no connection, so
// the revoke or renew it opened reached no grant.
func TestARowActionOnAChangedConnectionSeedsTheProfile(t *testing.T) {
	m := Model{reg: realRegistry(t)}
	tbl := view.Table{
		Columns: []view.Column{{Name: "Capability"}, {Name: "Profile"}, {Name: "Agent"}, {Name: "Record"}},
		Rows: [][]string{
			{"kv.get", "staging (changed)", "claude", "any"},
			{"kv.get", "—", "claude", "db"},
		},
	}
	for _, a := range grantRowActions(t, m) {
		m.row = 0
		if base, _ := m.actionSeed(a, tbl); base["profile"] != "staging" {
			t.Errorf("%s on a changed connection seeded profile %v, want staging", a.cap.ID, base["profile"])
		}
		m.row = 1
		if base, _ := m.actionSeed(a, tbl); base["profile"] != nil || base["scope"] != "db" {
			t.Errorf("%s on the base connection seeded %v, want the record and no profile", a.cap.ID, base)
		}
	}
}
