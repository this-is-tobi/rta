package wire

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/this-is-tobi/rta/pkg/view"
	rtav1 "github.com/this-is-tobi/rta/proto/rta/v1"
)

// A column's priority crosses the wire both ways, and an absent one is zero:
// the tables written before the field existed are never given up for width.
func TestAColumnsPriorityCrossesTheWireAndAbsentMeansNeverDropped(t *testing.T) {
	in := view.Table{Columns: []view.Column{{Name: "pod"}, {Name: "node", Priority: 3}}}
	out, ok := ViewFromProto(ViewToProto(in)).(view.Table)
	if !ok || out.Columns[0].Priority != 0 || out.Columns[1].Priority != 3 {
		t.Fatalf("round trip made %#v", out.Columns)
	}
	old := &rtav1.Column{Name: "x", Kind: rtav1.ColumnKind_COLUMN_KIND_UNSPECIFIED}
	if got := ViewFromProto(&rtav1.View{Kind: &rtav1.View_Table{Table: &rtav1.Table{Columns: []*rtav1.Column{old}}}}).(view.Table); got.Columns[0].Priority != 0 {
		t.Errorf("a column without the field decoded to priority %d", got.Columns[0].Priority)
	}
}

// A negative priority has no meaning on the wire, so it is sent as never given
// up rather than wrapped into a huge number that is given up first.
func TestANegativePriorityIsSentAsZero(t *testing.T) {
	got := ViewFromProto(ViewToProto(view.Table{Columns: []view.Column{{Name: "a"}, {Name: "b", Priority: -4}}})).(view.Table)
	if got.Columns[1].Priority != 0 {
		t.Errorf("a negative priority crossed as %d", got.Columns[1].Priority)
	}
}

// Skew in both directions. A host built before the field reads a column that
// carries it as a plain column, and a plugin built before it sends none.
func TestAColumnWithAFieldThisBuildDoesNotKnowStillDecodes(t *testing.T) {
	raw, err := proto.Marshal(&rtav1.Column{Name: "n", Priority: 2})
	if err != nil {
		t.Fatal(err)
	}
	raw = protowire.AppendTag(raw, 99, protowire.VarintType)
	raw = protowire.AppendVarint(raw, 7)
	var c rtav1.Column
	if err := proto.Unmarshal(raw, &c); err != nil {
		t.Fatalf("an unknown field broke decoding: %v", err)
	}
	if c.GetName() != "n" || c.GetPriority() != 2 {
		t.Errorf("decoded %v", &c)
	}
}
