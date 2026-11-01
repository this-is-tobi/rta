package sdktest

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A value the host would drop fails the suite: the host drops it without a
// word, and the author learns of it nowhere else. One spelled by the cell
// helpers passes.
func TestAValueTheHostWouldDropFails(t *testing.T) {
	tbl := view.Table{Columns: []view.Column{{Name: "Name"}, {Name: "Size", Kind: view.KindBytes}}}
	tbl.Add(view.Plain("a"), view.Bytes(2048))
	rec := &recorder{}
	checkValues(rec, ok(), tbl)
	if len(rec.errs) != 0 {
		t.Fatalf("a table filled through Add failed:\n%s", rec.errText())
	}

	for name, c := range map[string]struct {
		values [][]string
		want   string
	}{
		"a size spelled for a person": {[][]string{{"", "2 KiB"}}, `column "Size" (kind "bytes") has the value "2 KiB"`},
		"a value on a text column":    {[][]string{{"a", "2048"}}, `column "Name" (kind "") has the value "a"`},
		"a short row":                 {[][]string{{"2048"}}, "row 0 has 1 values for 2 cells"},
		"more rows of values":         {[][]string{{"", "1"}, {"", "2"}}, "values for 2 rows and has 1"},
	} {
		bad := tbl
		bad.Values = c.values
		rec := &recorder{}
		checkValues(rec, ok(), bad)
		if !strings.Contains(rec.errText(), c.want) {
			t.Errorf("%s: want %q, got:\n%s", name, c.want, rec.errText())
		}
	}
}
