package grant

import (
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// **A grant an agent issued itself must not look like one you issued.**
//
// An agent that can run shell commands can run `rta grant allow` — measured,
// and the seal does nothing about it, because the seal stops a process
// forging a line and not one asking rta to write a real one. What was missing
// was any way to tell the two apart afterwards: `grant list` showed what was
// in force and nothing about where it came from, so a grant nobody remembers
// issuing was indistinguishable from one they typed.
//
// Detection and not prevention. A shell can allocate a pty and claim to be a
// terminal — which the test suite for this feature did itself, with `script`
// — and it is worth having anyway, for the reason the record's hash chain is:
// against something running as you, making the ordinary case visible is the
// most that is honest, and the ordinary case is the one that happens.
func TestOriginRecordsWhetherAnybodyWasThere(t *testing.T) {
	for _, tc := range []struct {
		name        string
		surface     plugin.Surface
		interactive bool
		want        string
	}{
		{"a form", plugin.SurfaceTUI, false, core.FromForm},
		{"a terminal", plugin.SurfaceCLI, true, core.FromTerminal},
		{"a command with nobody there", plugin.SurfaceCLI, false, core.FromCommand},
		// A TUI is a person whatever the file descriptor says: it cannot run
		// without one.
		{"a form with no tty behind it", plugin.SurfaceTUI, true, core.FromForm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := core.Origin(tc.surface, tc.interactive); got != tc.want {
				t.Errorf("Origin(%v, %v) = %q, want %q", tc.surface, tc.interactive, got, tc.want)
			}
		})
	}
}

// The column appears only when it has something to say, which is the same
// rule the Agent column follows and a sharper version of its reason: its
// arrival *is* the finding.
func TestTheOriginColumnAppearsOnlyWhenAGrantWasUnwatched(t *testing.T) {
	now := time.Now()
	watched := []core.Grant{
		{Target: "kv.get", Issued: now, Expires: now.Add(time.Hour), From: core.FromTerminal},
		{Target: "todo.rm", Issued: now, Expires: now.Add(time.Hour), From: core.FromForm},
	}
	if names := columnNames(listTable(t, watched)); contains(names, "Origin") {
		t.Errorf("the column appeared with nothing to report: %v", names)
	}

	mixed := append(watched, core.Grant{
		Target: "pg.query", Issued: now, Expires: now.Add(time.Hour), From: core.FromCommand,
	})
	tbl := listTable(t, mixed)
	if names := columnNames(tbl); !contains(names, "Origin") {
		t.Fatalf("a grant issued unwatched produced no column: %v", names)
	}
	var sawCommand, sawTerminal bool
	for _, row := range tbl.Rows {
		joined := strings.Join(row, " ")
		if strings.Contains(joined, "command") {
			sawCommand = true
		}
		if strings.Contains(joined, "terminal") {
			sawTerminal = true
		}
	}
	if !sawCommand || !sawTerminal {
		t.Errorf("once the column exists every row has to fill it: %v", tbl.Rows)
	}
}

// A grant sealed before this field existed says "unknown", never "command".
// Unknown and unattended are different facts, and conflating them would
// accuse an old grant of something it did not do.
func TestAGrantFromBeforeThisFieldIsNotAccused(t *testing.T) {
	now := time.Now()
	tbl := listTable(t, []core.Grant{
		{Target: "kv.get", Issued: now, Expires: now.Add(time.Hour)},
		{Target: "pg.query", Issued: now, Expires: now.Add(time.Hour), From: core.FromCommand},
	})
	var old string
	for _, row := range tbl.Rows {
		if strings.HasPrefix(row[0], "kv.get") {
			old = strings.Join(row, " ")
		}
	}
	if strings.Contains(old, "command") {
		t.Errorf("a grant with no recorded origin was reported as unattended: %q", old)
	}
	if !strings.Contains(old, "—") {
		t.Errorf("a grant with no recorded origin says nothing at all: %q", old)
	}
}

// listTable saves grants and renders the list the way `rta grant list` does,
// so what is asserted on is the table an operator actually reads.
func listTable(t *testing.T, grants []core.Grant) view.Table {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if verr := core.Save(grants); verr != nil {
		t.Fatal(verr)
	}
	v, verr := heldTable()
	if verr != nil {
		t.Fatal(verr)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("want a Table, got %s", view.TypeOf(v))
	}
	return tbl
}

func columnNames(t view.Table) []string {
	out := make([]string, 0, len(t.Columns))
	for _, c := range t.Columns {
		out = append(out, c.Name)
	}
	return out
}

func contains(all []string, want string) bool {
	for _, s := range all {
		if s == want {
			return true
		}
	}
	return false
}
