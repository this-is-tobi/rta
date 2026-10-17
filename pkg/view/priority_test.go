package view

import (
	"slices"
	"testing"
)

func TestDropOrderIsTheHighestPriorityFirstAndTheRightmostAmongEquals(t *testing.T) {
	tbl := Table{Columns: []Column{
		{Name: "pod"}, {Name: "ready"}, {Name: "node", Priority: 2}, {Name: "ip", Priority: 3},
		{Name: "owner", Priority: 3}, {Name: "age", Priority: 1}, {Name: "qos", Priority: 4},
	}}
	if got, want := tbl.DropOrder(), []int{6, 4, 3, 2, 5}; !slices.Equal(got, want) {
		t.Errorf("DropOrder = %v, want %v", got, want)
	}
}

func TestTheFirstColumnAndAnyColumnWithNoPriorityAreNeverGivenUp(t *testing.T) {
	tbl := Table{Columns: []Column{{Name: "pod", Priority: 9}, {Name: "ready"}, {Name: "x", Priority: -1}, {Name: "y"}}}
	if got := tbl.DropOrder(); len(got) != 0 {
		t.Errorf("DropOrder = %v, want nothing: column 0 is the row's name and zero or less is never given up", got)
	}
	if got := (Table{}).DropOrder(); len(got) != 0 {
		t.Errorf("an empty table has a drop order %v", got)
	}
}

// MapStrings rebuilds every column it maps, and a field it forgot to copy is a
// hint that silently does nothing on the terminal paths that clean first.
func TestPriorityIsKeptWhenAViewsStringsAreMapped(t *testing.T) {
	in := Table{Columns: []Column{{Name: "pod"}, {Name: "node", Kind: KindText, Priority: 3}}}
	out := MapStrings(in, func(s string) string { return s + "!" }).(Table)
	if out.Columns[1].Priority != 3 || out.Columns[1].Name != "node!" {
		t.Errorf("MapStrings made %#v", out.Columns[1])
	}
}
