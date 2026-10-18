package view

import (
	"slices"
	"strings"
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

// How willingly a screen gives a column up is the screen's concern, the way
// Empty is: a program reading the table, json, yaml, csv or an agent over MCP,
// has no width to fit and was handed `"priority":2` beside the column's name
// and kind all the same. The field stays on the contract and on the plugin
// wire, where a renderer reads it; it is not part of any encoding.
func TestAColumnsPriorityIsNotEncoded(t *testing.T) {
	tbl := Table{
		Columns: []Column{{Name: "pod"}, {Name: "node", Kind: KindStatus, Priority: 2}},
		Rows:    [][]string{{"a", "n1"}},
	}
	raw, err := Marshal(Envelope{View: tbl})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "priority") {
		t.Errorf("the encoding carries a column's priority: %s", raw)
	}
	if !strings.Contains(string(raw), `"kind":"status"`) {
		t.Errorf("the encoding lost the column's kind, which an agent reads: %s", raw)
	}
	m, err := ToMap(tbl)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range m["columns"].([]any) {
		if _, ok := c.(map[string]any)["priority"]; ok {
			t.Errorf("ToMap carries a column's priority: %v", c)
		}
	}
}
