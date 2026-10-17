package tui

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A tile has no room for the columns of a table that was written for a page,
// and what it gives up first used to be decided by the shape of the data alone:
// plain text before times before verdicts. A table that knows which of its
// columns matter says so with a priority, and that outranks the shape.
func TestDeclaredPriorityBeatsTheShapeOfAColumnWhenATileGivesOneUp(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{
			{Name: "pod"},
			{Name: "notes"},
			{Name: "health", Kind: view.KindStatus, Priority: 1},
		},
		Rows: [][]string{
			{"api-0", "canary", "ok"},
			{"api-1", "stable", "WARN"},
		},
	}
	var narrow string
	for inner := 40; inner >= 12; inner-- {
		lines, ok := glanceTable(tbl, inner)
		if !ok {
			continue
		}
		joined := plain(strings.Join(lines, "\n"))
		if !strings.Contains(joined, "HEALTH") {
			narrow = joined
			break
		}
	}
	if narrow == "" {
		t.Fatal("the tile never gave a column up")
	}
	if !strings.Contains(narrow, "NOTES") {
		t.Errorf("the tile gave up notes, which the table never offered, before health, which it did:\n%s", narrow)
	}
}

// A column that may be given up never takes the tile's spare room for itself:
// it is the first thing the tile no longer has room for, so it cannot be what
// the remaining width is spent on.
func TestAnOptionalColumnOfProseIsNotTheFlexColumn(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "pod"}, {Name: "message", Priority: 2}, {Name: "state"}},
		Rows:    [][]string{{"api-0", strings.Repeat("a long sentence about the pod ", 3), "up"}},
	}
	lines := glanced(t, tbl, 30)
	if joined := strings.Join(lines, "\n"); strings.Contains(joined, "MESSAGE") && !strings.Contains(joined, "STATE") {
		t.Errorf("the optional prose column squeezed out a column that is never given up:\n%s", joined)
	}
}
