package wire

import (
	"slices"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
	rtav1 "github.com/this-is-tobi/rta/proto/rta/v1"
)

// A plugin's table crosses with its values, row for row, and a row without
// any crosses without any.
func TestATablesValuesCrossTheWire(t *testing.T) {
	in := view.Table{Columns: []view.Column{
		{Name: "Name"}, {Name: "Size", Kind: view.KindBytes}, {Name: "Seen", Kind: view.KindTimestamp},
	}}
	in.Add(view.Plain("a"), view.Bytes(2048), view.Time("today", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)))
	in.Add(view.Plain("b"), view.Plain("?"), view.Plain("never"))
	out, ok := ViewFromProto(ViewToProto(in)).(view.Table)
	if !ok {
		t.Fatal("the table did not come back a table")
	}
	if len(out.Values) != 2 || !slices.Equal(out.Values[0], []string{"", "2048", "2026-11-01T00:00:00Z"}) || out.Values[1] != nil {
		t.Errorf("values after the wire: %q", out.Values)
	}
}

// What a host keeps of a plugin's values: a row's only when there is one for
// every cell, and each only when it is one its column takes. A value that is
// not a number would otherwise be written into JSON unquoted.
func TestAPluginsMalformedValuesAreDropped(t *testing.T) {
	pb := &rtav1.View{Kind: &rtav1.View_Table{Table: &rtav1.Table{
		Columns: []*rtav1.Column{
			{Name: "Name"},
			{Name: "Size", Kind: rtav1.ColumnKind_COLUMN_KIND_BYTES},
			{Name: "State", Kind: rtav1.ColumnKind_COLUMN_KIND_STATUS},
		},
		Rows: []*rtav1.Row{
			{Cells: []string{"a", "2 KiB", "ok"}, Values: []string{"text has none", "2048}], \"x\": [1", "ok"}},
			{Cells: []string{"b", "1 KiB", "ok"}, Values: []string{"", "1024"}},
			{Cells: []string{"c", "4 KiB", "WARN"}, Values: []string{"", "4096", "WARN"}},
		},
	}}}
	out := ViewFromProto(pb).(view.Table)
	if len(out.Values) != 3 {
		t.Fatalf("values %q", out.Values)
	}
	if !slices.Equal(out.Values[0], []string{"", "", "ok"}) {
		t.Errorf("row 0 kept %q, want the text column's and the malformed size dropped", out.Values[0])
	}
	if out.Values[1] != nil {
		t.Errorf("row 1, with values for two of three cells, kept %q", out.Values[1])
	}
	if !slices.Equal(out.Values[2], []string{"", "4096", ""}) {
		t.Errorf("row 2 kept %q, want the status that is not a word dropped", out.Values[2])
	}
}
