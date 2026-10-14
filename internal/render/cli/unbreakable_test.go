package cli

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// replicationTable is the shape of a replication listing: prose-free columns
// whose values are positions and file names, each one a single token that means
// nothing once it is cut. 16/B374D848 cut after its slash, or a WAL file name
// cut at the 14th of 24 hex digits, is a different position or a name that does
// not exist, and nothing on the screen says where the cut was.
func replicationTable() view.Table {
	return view.Table{
		Columns: []view.Column{
			{Name: "Standby"}, {Name: "State"},
			{Name: "Sent LSN"}, {Name: "Write LSN"}, {Name: "Flush LSN"}, {Name: "Replay LSN"},
			{Name: "WAL file"},
		},
		Rows: [][]string{
			{"pg-replica-1", "streaming", "FFFFFFFF/FFFFFFFF", "16/B374D848", "16/B374D848", "16/B374D7C0", "00000001000000160000002D"},
			{"pg-replica-2", "catchup", "16/B374D848", "16/B3740000", "16/B3740000", "16/B3700000", "00000001000000160000002C"},
		},
	}
}

// A grid squeezed past what its narrowest unbreakable cell needs drew that cell
// over two lines; the table was still "wide enough" by the text-column floor,
// which counts cells and knows nothing of which of them cannot be broken.
// Whatever the width, a cell with no space in it is on one line whole — as a
// grid when there is room, and as records, which give a value the whole line,
// when there is not.
func TestAnUnbreakableCellIsNeverSplit(t *testing.T) {
	tokens := []string{"FFFFFFFF/FFFFFFFF", "16/B374D848", "16/B374D7C0", "00000001000000160000002D", "00000001000000160000002C"}
	for _, fill := range []bool{false, true} {
		for width := 50; width <= 200; width++ {
			out, _ := renderWidth(t, replicationTable(), Options{Width: width, Fill: fill})
			for _, tok := range tokens {
				if !strings.Contains(out, tok) {
					t.Fatalf("fill=%v width=%d: %q was split:\n%s", fill, width, tok, out)
				}
			}
		}
	}
}

// The protection is for a value that is one token and reasonably short. A
// column of long paths is not given the room of its longest one, or every
// table with a path in it would become records at any width short of the
// path's own.
func TestAVeryLongTokenDoesNotTurnATableIntoRecords(t *testing.T) {
	long := "/var/lib/postgresql/17/main/pg_wal/archive_status/000000010000001600000002D.done.partial.tmp.0001"
	tbl := view.Table{
		Columns: []view.Column{{Name: "Name"}, {Name: "Kind"}, {Name: "Path"}},
		Rows:    [][]string{{"wal", "directory", long}},
	}
	out, _ := renderWidth(t, tbl, Options{Width: 100, Fill: true})
	if !isGrid(out) {
		t.Fatalf("a long path turned the table into records at 100 cells:\n%s", out)
	}
}
