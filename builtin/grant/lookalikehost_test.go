package grant

import (
	"slices"
	"strings"
	"testing"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A grant on github.com written with a Cyrillic letter for its i said "may
// call http.get on any record under" that host in the receipt, the roster and
// the revoke, and drew it as the one it imitates, although the name resolves as
// xn--gthub-n2e.com. Each of them draws the record through textclean.Record,
// which says what the host resolves as, so the one change is every surface's.
// The letter is planted by code point: a source file may not hold a lookalike.
func TestAGrantOnALookalikeHostSaysWhatTheHostResolvesAs(t *testing.T) {
	setup(t)
	record := "https://g" + string(rune(0x456)) + "thub.com/"
	const note = "(xn--gthub-n2e.com)"

	body := run(t, allowH, map[string]any{"target": "kv.get", "scope": record, "agent": "probe"}).(view.Text).Body
	if want := "probe may call kv.get on any record under " + record + " " + note + " for"; !strings.Contains(body, want) {
		t.Errorf("the allow receipt says %q, want it to say %q", body, want)
	}

	tbl := listed(t, run(t, listH, nil))
	col := slices.Index(names(tbl), "Record")
	if col < 0 || len(tbl.Rows) != 1 {
		t.Fatalf("the roster has no Record column or not one row: %v %v", names(tbl), tbl.Rows)
	}
	cell := tbl.Rows[0][col]
	if want := record + " " + note + " (all)"; cell != want {
		t.Errorf("the roster's Record cell is %q, want %q", cell, want)
	}
	// The cell a row action seeds a revoke from reads back as the record.
	if back, ok := core.RecordOfRoster(cell); !ok || back != record {
		t.Errorf("RecordOfRoster(%q) = %q, %v, want the record %q back", cell, back, ok, record)
	}

	gone := revokeAt(t, map[string]any{"all": true}, false)
	if want := "probe may call kv.get on any record under " + record + " " + note; !strings.Contains(gone, want) {
		t.Errorf("the revoke receipt says %q, want it to say %q", gone, want)
	}
}
