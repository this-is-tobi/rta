package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// **The grading table.** Every capability this plugin declares, with the
// answer to the only three questions that decide what an agent can do with
// it: does it reach MCP at all, does a person have to allow the call, and
// does the grant name one record or the whole capability.
//
// It is a completeness guard first and a set of assertions second — a
// capability added without an entry here fails the test rather than
// inheriting whatever grading its neighbour happened to have. That is the
// failure this file exists for: pg.table.dump and pg.dump differ by one
// property that is invisible at the call site and decides whether a
// database leaves the building, and the way that gets broken is not by
// arguing about it, it is by copying the declaration above.
//
// The vault plugin's grant-accounting test is the same shape for the same
// reason.
type grading struct {
	// mcp is the weakest tier that exposes this capability at all:
	// "read" for anything Safety Read, "write" for --allow-write pg, and
	// "never" for one that refuses the surface outright.
	mcp string
	// consent is what a call over MCP costs: "" for none, "grant" for a
	// grant the person issues, naming the record in scope when there is one.
	consent string
	scope   string
	// bound says who decides how many rows come back.
	//
	//   ""        — returns no rows.
	//   "caller"  — the caller sizes the result, so the capability must
	//               declare a limit with a default and a ceiling.
	//   "fixed"   — rta wrote the SQL and the shape is one row per thing
	//               that exists, so maxRows is the ceiling and there is
	//               nothing for anybody to tune.
	//
	// The distinction is the one pg.query got wrong: it is the capability
	// whose result set the *caller* sizes, and it was the one with no
	// declared bound.
	bound string
}

// **The line between the two tiers, in one sentence:** `--allow-read pg`
// describes the database and returns no value stored in it; `--allow-write
// pg` returns values.
//
// From the user, and it is what forced this file to be rewritten once:
// *"almost every table could store sensitive data in a db, not only user
// table"*. Taken seriously, there is no table a capability may read by
// default, because there is no table known to be safe — orders carry
// addresses, events carry payloads, application logs carry tokens. So the
// tier cannot be "reads that feel small"; it has to be "reads that return
// nothing anybody stored", and three capabilities moved when that was
// applied honestly.
var expected = map[string]grading{
	// The read tier. Server identity, what databases exist, what tables
	// exist, what shape they have, and whether anything is stuck. Not one
	// row of anybody's data.
	"pg.status":     {mcp: "read"},
	"pg.table.list": {mcp: "read", bound: "caller"},
	"pg.overview":   {mcp: "read"},

	// Rows, of whatever the caller asked for. It was Read on the reasoning
	// that a READ ONLY transaction mutates nothing — true, and the wrong
	// axis, the one the safety model opens by rejecting.
	"pg.query": {mcp: "write", bound: "caller"},

	// **A running query is a place a value hides.** `select * from patients
	// where mrn = '...'` is a row of somebody's data wearing a WHERE clause,
	// and this returns eighty characters of every statement on the server.
	// pg.overview embeds the same rows without that column, which is what
	// keeps the glanceable form in the read tier.
	"pg.activity": {mcp: "write", bound: "caller"},

	// One row per database on the server. rta wrote the query, nobody
	// chooses the shape, and maxRows is the ceiling against a catalogue
	// nobody expected to be this big.
	"pg.database.list": {mcp: "read", bound: "fixed"},

	// Read and ungated, and the argument is that refusing it would be
	// theatre: anyone holding pg.query can already select from
	// information_schema.columns. It carries no values by construction —
	// schema_test.go is what holds that claim up.
	"pg.schema.dump": {mcp: "read"},

	// The one that hands over rows in bulk. Write so it needs
	// --allow-write pg, NeedsGrant so a person allows the call, and scoped
	// on the table so the grant names one relation rather than the database.
	"pg.table.dump": {mcp: "write", consent: "grant", scope: "table", bound: "caller"},

	// No blast radius a grant could name, so it does not reach the surface
	// at all. NeedsGrant deliberately unset: a grant that can never be
	// exercised would be an entry in `grant list` that means nothing.
	"pg.dump": {mcp: "never"},
}

func TestEveryCapabilityIsGradedDeliberately(t *testing.T) {
	caps := Plugin().Capabilities
	seen := map[string]bool{}

	for _, c := range caps {
		want, ok := expected[c.ID]
		if !ok {
			t.Errorf("%s: not accounted for in the grading table — decide what an agent may "+
				"do with it and write the entry, rather than inheriting the grading of "+
				"whichever declaration it was copied from", c.ID)
			continue
		}
		seen[c.ID] = true

		switch want.mcp {
		case "read":
			if c.Safety != plugin.Read {
				t.Errorf("%s: safety = %s, want read", c.ID, c.Safety)
			}
		case "write", "never":
			if c.Safety == plugin.Read {
				t.Errorf("%s: safety = read, so --allow-read alone exposes it; want write", c.ID)
			}
		}

		if wantGrant := want.consent == "grant"; c.NeedsGrant != wantGrant {
			t.Errorf("%s: NeedsGrant = %v, want %v", c.ID, c.NeedsGrant, wantGrant)
		}
		if c.Scope != want.scope {
			t.Errorf("%s: scope = %q, want %q", c.ID, c.Scope, want.scope)
		}
		switch want.bound {
		case "caller":
			assertDeclaresALimit(t, c)
		case "fixed":
			if declaresALimit(c) {
				t.Errorf("%s: declares a limit but is graded as fixed-shape — either the "+
					"caller sizes this result or it does not", c.ID)
			}
		}
	}

	for id := range expected {
		if !seen[id] {
			t.Errorf("the grading table names %q, which this plugin no longer declares", id)
		}
	}
}

func declaresALimit(c plugin.Capability) bool {
	for _, f := range c.Inputs {
		if f.Name == "limit" {
			return true
		}
	}
	return false
}

// assertDeclaresALimit is the rule the row bound was added for: a capability
// whose result set the caller sizes declares a limit with a default and a
// ceiling. Without one, `select * from users` streamed every row into a
// slice in the plugin, through the host, and at a model's context — which is
// the defect pg.query shipped with.
func assertDeclaresALimit(t *testing.T, c plugin.Capability) {
	t.Helper()
	for _, f := range c.Inputs {
		if f.Name != "limit" {
			continue
		}
		if f.Type != plugin.Int {
			t.Errorf("%s: limit is %s, want Int", c.ID, f.Type)
		}
		if f.Default == nil {
			t.Errorf("%s: limit has no default, so a caller that omits it is unbounded", c.ID)
		}
		if f.Max == 0 {
			t.Errorf("%s: limit has no maximum, so a caller can ask for an unbounded read", c.ID)
		}
		return
	}
	t.Errorf("%s returns rows and declares no row bound", c.ID)
}

// A capability that reaches MCP and hands over bulk rows must be gated by
// something a person issues. Stated as a property over the declarations
// rather than only as table entries, so it also holds for whatever is added
// next.
func TestNothingHandsOverBulkRowsWithoutAPersonSayingSo(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		g := expected[c.ID]
		if g.mcp == "never" || g.consent == "grant" {
			continue
		}
		if strings.Contains(c.ID, ".dump") && c.ID != "pg.schema.dump" {
			t.Errorf("%s reads in bulk, reaches MCP, and needs no grant", c.ID)
		}
	}
}

// The Scope input must not also be fillable from config.
//
// Sharp, and invisible without knowing the enforcement order: the grant gate
// reads the caller's raw argument map, and a Scope input the caller omitted
// is checked as the empty scope — then plugin.Resolve fills it from config
// and the handler runs against that value. The call would be authorized as
// one thing and performed on another. Fail-closed today, since no narrow
// grant covers the empty scope, but the two strings must simply never be
// able to differ.
func TestAScopedInputIsNotAlsoFilledFromConfig(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.Scope == "" {
			continue
		}
		for _, f := range c.Inputs {
			if f.Name != c.Scope {
				continue
			}
			if f.Config != "" {
				t.Errorf("%s: scope input %q also has Config %q — an omitted argument would be "+
					"checked as the empty scope and then run against the configured value",
					c.ID, f.Name, f.Config)
			}
			if f.Default != nil {
				t.Errorf("%s: scope input %q has a default, so an omitted argument is "+
					"authorized as one record and could run against another", c.ID, f.Name)
			}
		}
	}
}

// **The summary form never asks the server for the query text.**
//
// pg.overview stays in the read tier because the activity rows it embeds
// carry state, duration and what each session is waiting on — no literal
// anybody stored. That holds only if the column is left out of the SQL
// rather than trimmed off a table that already contains it: trimming is the
// protection that survives until somebody logs the intermediate.
func TestTheOverviewFormNeverSelectsTheQueryText(t *testing.T) {
	summary, col := activitySQL(false)
	if strings.Contains(summary, "coalesce(query") {
		t.Errorf("the summary form selects the query text:\n%s", summary)
	}
	if col.Name == "Query" {
		t.Errorf("the summary form's last column is %q", col.Name)
	}

	// And the detailed form does, or pg.activity would return nothing worth
	// the write classification it now carries.
	detail, col := activitySQL(true)
	if !strings.Contains(detail, "coalesce(query") {
		t.Errorf("pg.activity no longer returns the query text:\n%s", detail)
	}
	if col.Name != "Query" {
		t.Errorf("detail form's last column = %q, want Query", col.Name)
	}
}

// The tier rule, asserted as a property rather than only as table entries:
// nothing in the read tier may hand back a value stored in the database.
// Written as the list of capabilities allowed to be Read, so adding one to
// that tier is a deliberate edit here and not a default.
func TestOnlyDescribingCapabilitiesAreInTheReadTier(t *testing.T) {
	describes := []string{"pg.status", "pg.database.list", "pg.table.list",
		"pg.schema.dump", "pg.overview"}
	for _, c := range Plugin().Capabilities {
		if c.Safety != plugin.Read {
			continue
		}
		if !slices.Contains(describes, c.ID) {
			t.Errorf("%s is Read, so --allow-read alone exposes it — the read tier is for "+
				"capabilities that describe the database and return no value stored in it", c.ID)
		}
	}
}

// Local is what keeps a caller from choosing which file on this host gets
// written (a destination is a destination). pg.dump refuses MCP anyway;
// both hold, because they protect against different mistakes — the refusal
// is this capability's and can be edited away, Local is the contract's.
func TestTheBackupDestinationIsNeverCallerChosen(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		for _, f := range c.Inputs {
			if f.Type == plugin.Path && !f.Local {
				t.Errorf("%s: path input %q is not Local, so an MCP caller can name the file "+
					"this host writes", c.ID, f.Name)
			}
		}
	}
}

// Connection inputs stay Local, which is the property that keeps an agent
// from pointing the operator's credential at a database it chose. Asserted
// here rather than trusted, because a capability added later gets these
// fields from cap() and a change to connFields would move all of them at
// once.
func TestTheConnectionIsNeverCallerChosen(t *testing.T) {
	must := []string{"host", "port", "user", "database", "sslmode", "password"}
	for _, c := range Plugin().Capabilities {
		for _, f := range c.Inputs {
			if slices.Contains(must, f.Name) && !f.Local {
				t.Errorf("%s: %q is not Local", c.ID, f.Name)
			}
		}
	}
}
