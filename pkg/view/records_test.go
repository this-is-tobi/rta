package view

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// withRecords is v as the envelope that asks for records encodes it, decoded
// the way a consumer reads it, numbers as written.
func withRecords(t *testing.T, v View) map[string]any {
	t.Helper()
	raw, err := Marshal(Envelope{View: v, Records: true})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := decodeNumbers(raw, &m); err != nil {
		t.Fatalf("%v in %s", err, raw)
	}
	return m
}

func recordsOf(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	list, ok := m["records"].([]any)
	if !ok {
		t.Fatalf("records = %#v, want an array", m["records"])
	}
	out := make([]map[string]any, len(list))
	for i, r := range list {
		if out[i], ok = r.(map[string]any); !ok {
			t.Fatalf("record %d = %#v, want an object", i, r)
		}
	}
	return out
}

// A record is a row read by column name: a number where the column has one, the
// text a person reads where it does not, and null for a numeric cell that holds
// nothing — which is why `select(.["Use%"] > 90)` never meets the words "n/a".
func TestARecordIsARowKeyedByColumnName(t *testing.T) {
	got := recordsOf(t, withRecords(t, disk()))
	want := []map[string]any{
		{"Mount": "/", "Size": json.Number("994662584320"), "Use%": json.Number("72.4"), "Status": "WARN >80%"},
		{"Mount": "/boot", "Size": json.Number("1073741824"), "Use%": json.Number("9"), "Status": "ok"},
		{"Mount": "total", "Size": nil, "Use%": nil, "Status": ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("records\n got: %#v\nwant: %#v", got, want)
	}
}

// Nothing that does not ask for records is given them: the MCP bridge, the
// csv of a page and the TUI's copy all encode the envelope with the flag off,
// and the bytes they write are the bytes they wrote before the field existed.
func TestNoRecordsUnlessTheEnvelopeAsksForThem(t *testing.T) {
	page := Sections{Items: []Section{{ID: "disks", Title: "Disks", View: disk()}}}
	for _, v := range []View{disk(), page} {
		raw, err := Marshal(Envelope{View: v})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "records") {
			t.Errorf("a %s encodes records without being asked: %s", TypeOf(v), raw)
		}
		m, err := ToMap(v)
		if err != nil {
			t.Fatal(err)
		}
		if raw, _ := Marshal(m); strings.Contains(string(raw), "records") {
			t.Errorf("a %s through ToMap holds records: %s", TypeOf(v), raw)
		}
	}
}

// A page is a table's most common home, and the table can sit under sections
// that sit under sections: every one is given its records, the views that are
// no table are not, and a section with no view at all is skipped rather than
// taken down with a nil map.
func TestEveryTableInAPageHasItsRecords(t *testing.T) {
	inner := Table{Columns: []Column{{Name: "N", Kind: KindNumber}}}
	inner.Add(Count(7))
	page := Sections{Items: []Section{
		{ID: "disks", Title: "Disks", View: disk()},
		{ID: "deeper", Title: "Deeper", View: Sections{Items: []Section{{ID: "inner", Title: "Inner", View: inner}}}},
		{ID: "nothing", Title: "Nothing"},
		{ID: "prose", Title: "Prose", View: Text{Body: "hello"}},
	}}
	m := withRecords(t, page)
	if _, has := m["records"]; has {
		t.Errorf("a page has records of its own: %v", m["records"])
	}
	items := m["items"].([]any)
	view := func(i int) map[string]any { return items[i].(map[string]any)["view"].(map[string]any) }

	if got := recordsOf(t, view(0)); len(got) != 3 || got[0]["Mount"] != "/" || got[0]["Size"] != json.Number("994662584320") {
		t.Errorf("the table in a section has records %#v", got)
	}
	nested := view(1)["items"].([]any)[0].(map[string]any)["view"].(map[string]any)
	if got := recordsOf(t, nested); len(got) != 1 || got[0]["N"] != json.Number("7") {
		t.Errorf("the table two sections down has records %#v", got)
	}
	for _, i := range []int{2, 3} {
		if _, has := view(i)["records"]; has {
			t.Errorf("section %d is no table and has records: %v", i, view(i))
		}
	}
	if view(2)["type"] != "unknown" {
		t.Errorf("a section with no view encodes as %v", view(2))
	}
}

// An empty table is still a table: `.records[]` yields nothing and exits 0, as
// `.rows[]` and `.values[]` do, where null would fail on the day a list is empty.
func TestAnEmptyTableHasAnEmptyArrayOfRecords(t *testing.T) {
	for name, tbl := range map[string]Table{
		"of a kind": {Columns: []Column{{Name: "ID"}, {Name: "Due", Kind: KindStatus}}},
		"of text":   {Columns: []Column{{Name: "Name"}}},
		"no column": {},
	} {
		raw, err := Marshal(Envelope{View: tbl, Records: true})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"records":[]`) {
			t.Errorf("an empty table %s encodes as %s, want \"records\":[]", name, raw)
		}
	}
}

// The key is the column's name as columns[].name spells it. A name that
// repeats keeps its first holder, and each later one takes the first suffix
// nobody else already is called by, so no cell is written over another and no
// name changes for a column that was never in a clash. A blank name is the key
// "", once.
func TestRecordKeys(t *testing.T) {
	for _, c := range []struct {
		names []string
		want  []string
	}{
		{[]string{"Name", "Size"}, []string{"Name", "Size"}},
		{[]string{"CPU %", "°C", "?column?", "a.id"}, []string{"CPU %", "°C", "?column?", "a.id"}},
		{[]string{"id", "id", "id_2", "", ""}, []string{"id", "id_3", "id_2", "", "_2"}},
		{[]string{"a", "a", "a"}, []string{"a", "a_2", "a_3"}},
		{[]string{"a", "a_2", "a", "a_2", "a"}, []string{"a", "a_2", "a_3", "a_2_2", "a_4"}},
		{[]string{"", "", ""}, []string{"", "_2", "_3"}},
		{nil, []string{}},
	} {
		got := recordKeys(c.names)
		if !slices.Equal(got, c.want) {
			t.Errorf("recordKeys(%q) = %q, want %q", c.names, got, c.want)
		}
		seen := map[string]bool{}
		for _, k := range got {
			if seen[k] {
				t.Errorf("recordKeys(%q) = %q repeats %q", c.names, got, k)
			}
			seen[k] = true
		}
	}

	tbl := Table{Columns: []Column{{Name: "id"}, {Name: "id"}, {Name: ""}}, Rows: [][]string{{"1", "2", "3"}}}
	want := map[string]any{"id": "1", "id_2": "2", "": "3"}
	if got := recordsOf(t, withRecords(t, tbl)); !reflect.DeepEqual(got, []map[string]any{want}) {
		t.Errorf("a table with a repeated and a blank name has records %#v, want %#v", got, want)
	}
}

// A row that does not reach every column leaves the rest null, as it does in
// `values`; a cell past the last column has no name to be keyed by, so it is
// nowhere, and nothing here panics on either.
func TestARowOfTheWrongLengthStillHasARecord(t *testing.T) {
	tbl := Table{
		Columns: []Column{{Name: "A"}, {Name: "B", Kind: KindNumber}, {Name: "C"}},
		Rows:    [][]string{{"short"}, {"x", "1", "y", "past the last column"}, nil},
	}
	want := []map[string]any{
		{"A": "short", "B": nil, "C": nil},
		{"A": "x", "B": json.Number("1"), "C": "y"},
		{"A": nil, "B": nil, "C": nil},
	}
	if got := recordsOf(t, withRecords(t, tbl)); !reflect.DeepEqual(got, want) {
		t.Errorf("records\n got: %#v\nwant: %#v", got, want)
	}
}

// A timestamp is its instant where it has one, and null where it has not: its
// text is for a person (`Oct 31`, `never`), and a column read by a program is
// all of one kind of thing.
func TestATimestampIsItsInstantOrNull(t *testing.T) {
	tbl := Table{Columns: []Column{{Name: "Name"}, {Name: "Seen", Kind: KindTimestamp}}}
	tbl.Add(Plain("a"), Time("Oct 31", time.Date(2026, 10, 31, 14, 2, 0, 0, time.UTC)))
	tbl.Add(Plain("b"), Time("never", time.Time{}))
	want := []map[string]any{
		{"Name": "a", "Seen": "2026-10-31T14:02:00Z"},
		{"Name": "b", "Seen": nil},
	}
	if got := recordsOf(t, withRecords(t, tbl)); !reflect.DeepEqual(got, want) {
		t.Errorf("records\n got: %#v\nwant: %#v", got, want)
	}
}

// A number keeps every digit it was given, through json and through the map
// yaml and csv are written from — the same promise the table's values make.
func TestARecordKeepsEveryDigitOfANumber(t *testing.T) {
	tbl := Table{
		Columns: []Column{{Name: "N", Kind: KindNumber}},
		Rows:    [][]string{{"2^64 - 1"}, {"12.3 quadrillion"}},
		Values:  [][]string{{"18446744073709551615"}, {"12345678901234567.89"}},
	}
	raw, err := Marshal(Envelope{View: tbl, Records: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `{"N":18446744073709551615}`) || !strings.Contains(string(raw), `{"N":12345678901234567.89}`) {
		t.Errorf("the records lost digits: %s", raw)
	}

	m, err := Envelope{View: tbl, Records: true}.ToMap()
	if err != nil {
		t.Fatal(err)
	}
	records := m["records"].([]any)
	if got := records[0].(map[string]any)["N"]; got != uint64(math.MaxUint64) {
		t.Errorf("the first record reads %#v through ToMap, want a uint64", got)
	}
	if got := records[1].(map[string]any)["N"]; got != json.Number("12345678901234567.89") {
		t.Errorf("the second record reads %#v through ToMap, want its own text", got)
	}
}

// A record is built from the table Redact left, so a masked cell is still
// masked and its value is still gone: the secret is in no record, whatever the
// column's kind. A text column's record holds the mask a person sees; a numeric
// one has no value left and is null, which says nothing about the secret.
func TestARecordHoldsNothingRedactRemoved(t *testing.T) {
	tbl := Table{
		Columns:  []Column{{Name: "Name"}, {Name: "Token"}, {Name: "Size", Kind: KindBytes}},
		Redacted: []string{"Token", "Size"},
	}
	tbl.Add(Plain("api"), Plain("hunter2-secret"), Bytes(int64(48151623420)))

	raw, err := Marshal(Envelope{View: Redact(tbl), Records: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hunter2-secret", "48151623420", "44.8 GiB"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("%q survives redaction in %s", secret, raw)
		}
	}
	want := map[string]any{"Name": "api", "Token": Mask, "Size": nil}
	var m map[string]any
	if err := decodeNumbers(raw, &m); err != nil {
		t.Fatal(err)
	}
	if got := recordsOf(t, m); !reflect.DeepEqual(got, []map[string]any{want}) {
		t.Errorf("records %#v, want %#v", got, want)
	}
}

// The helper reads an encoded map, and a map is only as well-formed as whatever
// built it. Nothing in it may panic: this encoder has taken a process down
// before on a view that was nil.
func TestAddRecordsToleratesAMapThatIsNotAView(t *testing.T) {
	for _, m := range []map[string]any{
		nil,
		{},
		{"type": 3},
		{"type": "table"},
		{"type": "table", "columns": "x", "rows": 7, "values": "y"},
		{"type": "table", "columns": []any{3, nil, map[string]any{"name": 4}}, "rows": []any{"x", []any{1, nil}, nil}},
		{"type": "table", "columns": []any{map[string]any{"name": "N", "kind": "number"}}, "rows": []any{[]any{"1"}}, "values": []any{3}},
		{"type": "table", "columns": []any{map[string]any{"name": "N", "kind": "number"}}, "rows": []any{[]any{"1"}}, "values": [][]any{nil}},
		{"type": "sections"},
		{"type": "sections", "items": "x"},
		{"type": "sections", "items": []any{nil, 3, map[string]any{}, map[string]any{"view": 4}, map[string]any{"view": nil}, map[string]any{"view": map[string]any{}}}},
	} {
		addRecords(m)
	}
}
