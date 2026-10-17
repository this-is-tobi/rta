package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A column that says how willingly it is given up lets a narrow terminal show
// the columns that matter instead of falling back to a block per row, while
// everything a machine reads keeps every column.

func podTable() view.Table {
	return view.Table{
		Columns: []view.Column{
			{Name: "pod"}, {Name: "ready"}, {Name: "status", Kind: view.KindStatus}, {Name: "age", Kind: view.KindDuration},
			{Name: "node", Priority: 2}, {Name: "ip", Priority: 3}, {Name: "qos", Priority: 4},
		},
		Rows: [][]string{
			{"api-6d9f8-abcde", "1/1", "Running", "3d", "worker-node-1", "10.42.0.17", "Burstable"},
			{"db-0", "1/1", "Running", "9d", "worker-node-2", "10.42.1.4", "Guaranteed"},
		},
		Total: 2,
	}
}

func prettyAt(t *testing.T, v view.View, width int) string {
	t.Helper()
	out, _ := renderWidth(t, v, Options{Width: width})
	return out
}

func TestNothingIsGivenUpWhileTheTableFits(t *testing.T) {
	out := prettyAt(t, podTable(), 160)
	for _, name := range []string{"NODE", "IP", "QOS"} {
		if !strings.Contains(out, name) {
			t.Errorf("%s was dropped on a terminal wide enough for it:\n%s", name, out)
		}
	}
	if strings.Contains(out, "hidden") {
		t.Errorf("a footer names nothing hidden:\n%s", out)
	}
}

func TestTheHighestPriorityColumnsGoFirstAndTheFooterNamesThem(t *testing.T) {
	naturalWidth := 0
	for _, line := range strings.Split(prettyAt(t, podTable(), 200), "\n") {
		naturalWidth = max(naturalWidth, len([]rune(line)))
	}
	// Squeeze the terminal a cell at a time and record what is given up and when.
	var order []string
	for width := naturalWidth; width >= 20; width-- {
		out := prettyAt(t, podTable(), width)
		for _, line := range strings.Split(out, "\n") {
			_, names, found := strings.Cut(line, "hidden to fit: ")
			if !found {
				continue
			}
			for _, n := range strings.Split(names, ", ") {
				if len(order) == 0 || !contains(order, n) {
					order = append(order, n)
				}
			}
		}
	}
	// Priority 4, 3 then 2: qos, ip, node, in that order, and never age or the name.
	if len(order) < 3 || order[0] != "qos" || order[1] != "ip" || order[2] != "node" {
		t.Errorf("columns were given up in the order %v, want qos, ip, node (the highest priority number first)", order)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestWidthingTheTerminalOnlyEverShowsMoreOfTheSameRanking(t *testing.T) {
	shown := func(out string) []string {
		var names []string
		for _, c := range podTable().Columns {
			if strings.Contains(out, strings.ToUpper(c.Name)) {
				names = append(names, c.Name)
			}
		}
		return names
	}
	prev := 0
	for _, width := range []int{40, 50, 60, 70, 80, 90, 100, 120, 160} {
		got := len(shown(prettyAt(t, podTable(), width)))
		if got < prev {
			t.Errorf("at %d cells %d columns are shown, fewer than the %d at a narrower width", width, got, prev)
		}
		prev = got
	}
}

func TestAPipeAndEveryMachineFormatCarryEveryColumn(t *testing.T) {
	if out, _ := renderWidth(t, podTable(), Options{Width: 0}); !strings.Contains(out, "QOS") {
		t.Errorf("a width of 0, which is a pipe, dropped a column:\n%s", out)
	}
	for _, f := range []Format{JSON, YAML, CSV, Markdown} {
		var buf bytes.Buffer
		if err := Render(&buf, podTable(), Options{Format: f, NoColor: true, Width: 30}); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"qos", "ip", "node"} {
			if !strings.Contains(strings.ToLower(buf.String()), want) {
				t.Errorf("%s output lost the %s column at a narrow width:\n%s", f, want, buf.String())
			}
		}
	}
}

func TestAnOptionalColumnWithNothingInItGoesFirstAndIsNotNamed(t *testing.T) {
	tbl := podTable()
	for i := range tbl.Rows {
		tbl.Rows[i][5] = "" // ip says nothing in any row
	}
	wide := prettyAt(t, tbl, 160)
	needed := 0
	for _, line := range strings.Split(wide, "\n") {
		needed = max(needed, len([]rune(line)))
	}
	out := prettyAt(t, tbl, needed-1)
	if strings.Contains(out, "IP") && !strings.Contains(out, "hidden") {
		t.Errorf("the empty column was kept while the table was squeezed:\n%s", out)
	}
	if strings.Contains(out, "ip") {
		t.Errorf("an empty column that cost nothing was named in the footer:\n%s", out)
	}
	if !strings.Contains(out, "QOS") {
		t.Errorf("a column that held something was given up before the empty one:\n%s", out)
	}
}

func TestATableWithNoPrioritiesIsDrawnExactlyAsItAlwaysWas(t *testing.T) {
	plain := podTable()
	for i := range plain.Columns {
		plain.Columns[i].Priority = 0
	}
	for _, width := range []int{30, 60, 100, 160} {
		withZero := prettyAt(t, plain, width)
		if strings.Contains(withZero, "hidden to fit") {
			t.Errorf("width %d: a table with no priorities named hidden columns:\n%s", width, withZero)
		}
	}
}

// The first column is the row's name, so a priority on it is not honoured: a
// table whose rows cannot be told apart is worse than a wide one.
func TestTheFirstColumnIsKeptWhateverItsPriority(t *testing.T) {
	tbl := podTable()
	tbl.Columns[0].Priority = 9
	if out := prettyAt(t, tbl, 36); !strings.Contains(out, "api-6d9f8") && !strings.Contains(out, "POD") {
		t.Errorf("the name column was given up:\n%s", out)
	}
}

func TestARedactedColumnIsStillMaskedAfterOthersAreGivenUp(t *testing.T) {
	tbl := podTable()
	tbl.Columns[1].Name = "token"
	tbl.Redacted = []string{"token"}
	for i := range tbl.Rows {
		tbl.Rows[i][1] = "s3cr3t"
	}
	out := prettyAt(t, tbl, 50)
	if strings.Contains(out, "s3cr3t") {
		t.Errorf("a redacted cell was shown after columns were dropped:\n%s", out)
	}
}
