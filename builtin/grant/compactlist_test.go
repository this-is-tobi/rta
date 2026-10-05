package grant

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/builtin/internal/compact"
	"github.com/this-is-tobi/rta/internal/agentlog"
	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/view"
)

func names(t view.Table) []string {
	out := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = c.Name
	}
	return out
}

func issueRows(t *testing.T, grants ...core.Grant) {
	t.Helper()
	now := time.Now()
	for i := range grants {
		grants[i].Issued, grants[i].Expires = now, now.Add(time.Hour)
		grants[i].From = core.FromTerminal
	}
	if verr := core.Save(grants); verr != nil {
		t.Fatal(verr)
	}
}

// The roster is the screen somebody opens to see what they have allowed and
// for how long. Eight columns at eighty cells are a card of seven lines each.
func TestOnATerminalTheRosterIsOneShortLinePerGrant(t *testing.T) {
	defer compact.Pretend(true)()
	setup(t)
	issueRows(t,
		core.Grant{Target: "kv.get", Scope: "db-password", Agent: "claude", MaxUses: 3, Note: "debugging"},
		core.Grant{Target: "net.dns", Agent: "claude"})
	tbl := listed(t, run(t, listH, nil))
	if got, want := names(tbl), []string{"Capability", "Record", "Agent", "Expires In", "Budget Left"}; !slices.Equal(got, want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
	if tbl.Rows[0][4] != "3 of 3 uses" || tbl.Rows[1][4] != "unlimited" {
		t.Errorf("budget cells = %q, %q", tbl.Rows[0][4], tbl.Rows[1][4])
	}
	for i, row := range tbl.Rows {
		if w := len(strings.Join(row, "  ")); w > 60 {
			t.Errorf("row %d is %d cells wide: %q", i, w, row)
		}
	}
}

// Short is not the same as hiding the finding: a grant on a connection, one
// issued with nobody at the terminal, one whose plugin was replaced are the
// rows somebody most needs to see, and their columns stay.
func TestTheCompactRosterKeepsTheColumnsThatAreTheFinding(t *testing.T) {
	defer compact.Pretend(true)()
	setup(t)
	issueRows(t,
		core.Grant{Target: "kv.get", Agent: "claude", Profile: "staging"},
		core.Grant{Target: "net.dns", Agent: "claude"})
	if got := names(listed(t, run(t, listH, nil))); !slices.Contains(got, "Profile") {
		t.Errorf("a grant on a connection and no Profile column: %v", got)
	}
	issueRows(t, core.Grant{Target: "kv.get", Agent: "claude", From: core.FromCommand})
	grants, _ := core.Load()
	grants[0].From = core.FromCommand
	if verr := core.Save(grants); verr != nil {
		t.Fatal(verr)
	}
	if got := names(listed(t, run(t, listH, nil))); !slices.Contains(got, "Origin") {
		t.Errorf("a grant issued with nobody there and no Origin column: %v", got)
	}
}

// A script is promised every field, and the time left is a reading of the
// clock that is out of date by the time it is parsed: the instant is what an
// alert is written against.
func TestThePipedRosterCarriesEveryFieldAndTheInstantItExpires(t *testing.T) {
	defer compact.Pretend(false)()
	setup(t)
	issueRows(t, core.Grant{Target: "kv.get", Scope: "db-password", Agent: "claude", Note: "debugging"})
	tbl := listed(t, run(t, listH, nil))
	want := []string{"Capability", "Profile", "Agent", "Record", "Expires In", "Expires At", "Budget Left", "Note"}
	if got := names(tbl); !slices.Equal(got, want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
	at := tbl.Rows[0][5]
	when, err := time.Parse(time.RFC3339, at)
	if err != nil {
		t.Fatalf("Expires At = %q, not RFC 3339: %v", at, err)
	}
	if left := time.Until(when); left < 59*time.Minute || left > 61*time.Minute {
		t.Errorf("Expires At = %s is %v from now, want the hour the grant was issued for", at, left)
	}
}

func TestTheRosterFiltersByAgent(t *testing.T) {
	setup(t)
	issueRows(t,
		core.Grant{Target: "kv.get", Agent: "claude"},
		core.Grant{Target: "kv.env", Agent: "cursor"})
	tbl := listed(t, run(t, listH, map[string]any{"agent": "cursor"}))
	if tbl.Total != 1 || tbl.Rows[0][0] != "kv.env" {
		t.Fatalf("--agent cursor: %+v", tbl.Rows)
	}
	none := listed(t, run(t, listH, map[string]any{"agent": "cursr"}))
	if len(none.Rows) != 0 || !strings.Contains(none.Empty, "No standing grant was issued for the agent cursr") ||
		!strings.Contains(none.Empty, "did you mean cursor?") {
		t.Errorf("a mistyped agent reads as an agent with nothing: %q", none.Empty)
	}
}

// A lock on a name nobody used freezes nobody while the screen says locked,
// and a grant for it authorizes nothing while the roster lists it. The record
// counts as having seen a name: the agent an incident is about called an hour
// ago and holds nothing now.
func TestAnUnknownAgentIsNotedBesideTheNearestNameThisMachineKnows(t *testing.T) {
	setup(t)
	if got := UnknownAgentNote("claudee"); got != "" && !strings.Contains(got, "test") {
		t.Errorf("a machine that knows only test: %q", got)
	}
	if err := agentlog.Append(agentlog.Entry{Cap: "sys.cpu", Agent: "claude", Outcome: agentlog.Ran, Auth: agentlog.Open}); err != nil {
		t.Fatal(err)
	}
	if got := UnknownAgentNote("claude"); got != "" {
		t.Errorf("claude is in the record and was noted: %q", got)
	}
	got := UnknownAgentNote("claudee")
	for _, want := range []string{`no agent named "claudee"`, "appears in the record", "claude, test", "did you mean claude?"} {
		if !strings.Contains(got, want) {
			t.Errorf("note = %q, missing %q", got, want)
		}
	}
	if got := UnknownAgentNote("zzzzzz"); strings.Contains(got, "did you mean") {
		t.Errorf("a name near nothing is offered a guess: %q", got)
	}
}

func TestNearestNameIsATypoNotAChance(t *testing.T) {
	known := []string{"claude", "cursor", "codex"}
	for typed, want := range map[string]string{
		"claudee": "claude", "cluade": "claude", "CURSOR": "cursor", "cursr": "cursor", "codx": "codex",
		"gemini": "", "c": "", "ab": "",
	} {
		if got := nearestName(typed, known); got != want {
			t.Errorf("nearestName(%q) = %q, want %q", typed, got, want)
		}
	}
}
