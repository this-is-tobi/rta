package mcp

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A table cut to fit a tool result is cut with its values, from the end it
// keeps: every value left must stand beside the row it was written for.
func TestACutTableKeepsEachValueBesideItsRow(t *testing.T) {
	for _, tail := range []bool{false, true} {
		tbl := view.Table{Columns: []view.Column{{Name: "N", Kind: view.KindNumber}}, Tail: tail}
		for i := range 400 {
			tbl.Add(view.Count(i))
		}
		cut, ok := shrinkTable(tbl, 2000, func(int) string { return "" })
		if !ok {
			t.Fatalf("tail=%v: nothing fit", tail)
		}
		out := cut.(view.Table)
		if len(out.Rows) == len(tbl.Rows) || len(out.Values) != len(out.Rows) {
			t.Fatalf("tail=%v: %d rows and %d values kept of %d", tail, len(out.Rows), len(out.Values), len(tbl.Rows))
		}
		for i, row := range out.Rows {
			if out.Values[i][0] != row[0] {
				t.Fatalf("tail=%v: row %d reads %q beside the value %q", tail, i, row[0], out.Values[i][0])
			}
		}
		if tail && out.Rows[len(out.Rows)-1][0] != strconv.Itoa(399) {
			t.Errorf("a tail table kept %q last, want its newest row", out.Rows[len(out.Rows)-1][0])
		}
	}
}

// A model is sent a table's cells and not their values, which are the same
// cells again: the ones its producer gave and the ones its text holds, on a
// table answered alone and on one a page holds.
func TestAModelIsSentATablesCellsAndNotTheirValues(t *testing.T) {
	tbl := view.Table{Columns: []view.Column{{Name: "Used", Kind: view.KindBytes}, {Name: "State", Kind: view.KindStatus}}}
	tbl.Add(view.Bytes(994662584320), view.Plain("WARN soon"))
	if raw, _ := view.Marshal(view.Envelope{View: tbl}); !strings.Contains(string(raw), `"values"`) {
		t.Fatalf("-o json carries no values for the table, so this proves nothing: %s", raw)
	}
	page := view.Sections{Items: []view.Section{{ID: "disks", Title: "disks", View: tbl}}}
	for _, v := range []view.View{tbl, page} {
		text, isErr := sent(t, v)
		var got map[string]any
		if err := json.Unmarshal([]byte(text), &got); isErr || err != nil {
			t.Fatalf("a %s answer, error %v %v: %s", view.TypeOf(v), isErr, err, text)
		}
		answer := got
		if items, ok := got["items"].([]any); ok && len(items) == 1 {
			section, _ := items[0].(map[string]any)
			answer, _ = section["view"].(map[string]any)
		}
		if _, has := answer["values"]; has || answer["rows"] == nil {
			t.Errorf("a %s answer, want a table's rows without their values: %s", view.TypeOf(v), text)
		}
	}
}

// What a result may weigh is spent on the rows a model reads: a table of sizes
// that fits once its values are left out is sent whole, where counting them
// too cut it at 2813 of its 4000 rows.
func TestATableIsCutForItsCellsAlone(t *testing.T) {
	tbl := view.Table{Columns: []view.Column{{Name: "Size", Kind: view.KindBytes}}}
	for i := range 4000 {
		tbl.Add(view.Bytes(994662584320 + i))
	}
	if raw, _ := view.Marshal(view.Envelope{View: tbl}); len(raw) <= maxResultBytes {
		t.Fatalf("the table is %d bytes with its values, want it over the %d a result may weigh", len(raw), maxResultBytes)
	}
	text, isErr := sent(t, tbl)
	var got struct {
		Rows     [][]string
		Warnings []struct{ Code string }
	}
	if err := json.Unmarshal([]byte(text), &got); isErr || err != nil {
		t.Fatalf("error %v %v: %.200s", isErr, err, text)
	}
	if len(got.Rows) != 4000 || len(got.Warnings) != 0 {
		t.Errorf("%d of 4000 rows sent, warnings %+v: want the whole table", len(got.Rows), got.Warnings)
	}
}
