package view

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

// disk is a table as sys disk would fill it: a name, a size, a share and a
// status, the last row with no values at all.
func disk() Table {
	t := Table{Columns: []Column{
		{Name: "Mount"}, {Name: "Size", Kind: KindBytes}, {Name: "Use%", Kind: KindUsage}, {Name: "Status", Kind: KindStatus},
	}}
	t.Add(Plain("/"), Bytes(int64(994662584320)), Number("72%", 72.4), Status("WARN >80%", "warn"))
	t.Add(Plain("/boot"), Bytes(int64(1073741824)), Number("9%", 9), Status("ok", "ok"))
	t.Add(Plain("total"), Plain(""), Plain(""), Plain(""))
	return t
}

// The cells are what a person reads and the values what a program reads, row
// for row: Add keeps the two in step, a row with no value adds an empty one,
// and a table of Plain cells carries none at all.
func TestAddKeepsTheValuesBesideTheirRows(t *testing.T) {
	d := disk()
	if !slices.Equal(d.Rows[0], []string{"/", "926.4 GiB", "72%", "WARN >80%"}) {
		t.Errorf("row 0 reads %q", d.Rows[0])
	}
	if !slices.Equal(d.Values[0], []string{"", "994662584320", "72.4", "warn"}) {
		t.Errorf("row 0's values are %q", d.Values[0])
	}
	if len(d.Values) != len(d.Rows) || d.Values[2] != nil {
		t.Errorf("values %q for %d rows, want one per row and none on the last", d.Values, len(d.Rows))
	}

	var plain Table
	plain.Add(Plain("a"), Plain("b"))
	if plain.Values != nil {
		t.Errorf("a table of plain cells carries values %q", plain.Values)
	}
	// A value arriving after rows without any puts nothing on the wrong row.
	plain.Add(Plain("c"), Count(3))
	if len(plain.Values) != 2 || plain.Values[0] != nil || !slices.Equal(plain.Values[1], []string{"", "3"}) {
		t.Errorf("a value after a row with none: %q", plain.Values)
	}
}

// Each helper spells its value as ValueOK reads it, and has none where a
// value would be a lie or would break the document: a number that is not
// finite, the zero time, a status that is not a word.
func TestTheCellHelpersSpellTheirValues(t *testing.T) {
	when := time.Date(2026, 10, 31, 14, 2, 0, 0, time.FixedZone("CET", 3600))
	for _, c := range []struct {
		kind ColumnKind
		cell Cell
		want string
	}{
		{KindBytes, Bytes(uint64(1392640)), "1392640"},
		{KindBytes, Bytes(-1), "-1"},
		{KindNumber, Count(42), "42"},
		{KindPercent, Number("47.6", 47.6), "47.6"},
		{KindNumber, Number("∞", math.Inf(1)), ""},
		{KindDuration, Duration("1h30m", 90*time.Minute), "5400"},
		{KindDuration, Duration("250ms", 250*time.Millisecond), "0.25"},
		{KindTimestamp, Time("Oct 31", when), "2026-10-31T13:02:00Z"},
		{KindTimestamp, Time("never", time.Time{}), ""},
		{KindStatus, Status("WARN soon · Oct 31", "warn"), "warn"},
		{KindStatus, Status("did not answer", "did not answer"), "did not answer"},
		{KindStatus, Status("WARN", "WARN"), ""},
	} {
		if c.cell.Value != c.want {
			t.Errorf("%q: value %q, want %q", c.cell.Text, c.cell.Value, c.want)
		}
		if c.want != "" && !ValueOK(c.kind, c.cell.Value) {
			t.Errorf("%q: value %q is not one a %s column takes", c.cell.Text, c.cell.Value, c.kind)
		}
	}
	for kind, bad := range map[ColumnKind][]string{
		KindBytes:     {"926.4 GiB", "0x10", " 12", "1,024", "NaN", "Infinity", "12."},
		KindUsage:     {"72%"},
		KindTimestamp: {"Oct 31", "2026-10-31"},
		KindStatus:    {"WARN", "warn!", " warn"},
		KindText:      {"anything"},
	} {
		for _, v := range bad {
			if ValueOK(kind, v) {
				t.Errorf("%q is taken as a %q column's value", v, kind)
			}
		}
	}
}

// -o json and MCP carry the values beside the rows in their JSON types: a size
// as a number, a status as a string, null where a cell has none. A table
// without any encodes as it always did, with no "values" at all.
func TestTheEnvelopeWritesValuesInTheirTypes(t *testing.T) {
	raw, err := Marshal(Envelope{View: disk()})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Values [][]any `json:"values"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Values) != 3 {
		t.Fatalf("values = %v in %s", got.Values, raw)
	}
	if n, ok := got.Values[0][1].(json.Number); !ok || n.String() != "994662584320" {
		t.Errorf("a size reads %#v, want the number", got.Values[0][1])
	}
	if s, ok := got.Values[0][3].(string); !ok || s != "warn" {
		t.Errorf("a status reads %#v, want the string", got.Values[0][3])
	}
	if got.Values[0][0] != nil || got.Values[2][1] != nil {
		t.Errorf("cells with no value read %#v and %#v, want null", got.Values[0][0], got.Values[2][1])
	}

	var plain Table
	plain.Columns = []Column{{Name: "Name"}}
	plain.Add(Plain("a"))
	raw, err = Marshal(Envelope{View: plain})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "values") {
		t.Errorf("a table with no values encodes them: %s", raw)
	}

	// YAML reads the same document: a size is an integer there, not a string.
	m, err := ToMap(disk())
	if err != nil {
		t.Fatal(err)
	}
	if v := m["values"].([]any)[0].([]any)[1]; v != int64(994662584320) {
		t.Errorf("the size reads %#v through ToMap, want an int64", v)
	}
}

// A value that is not one its column takes is written as null, whoever wrote
// it: a hand-filled table, or a cleaning pass that changed one.
func TestAMalformedValueIsWrittenAsNone(t *testing.T) {
	d := disk()
	d.Values[0][1] = "926.4 GiB"
	raw, err := Marshal(Envelope{View: d})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatalf("encoded %s", raw)
	}
	var got struct {
		Values [][]any `json:"values"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Values[0][1] != nil {
		t.Errorf("a malformed size reads %#v, want null", got.Values[0][1])
	}
}

// A status, a count or a time its producer gave no value is valued as its
// text, where the text is one, so a plugin that fills nothing still hands a
// program its grades, its counts and its times. Nothing else is read back from
// a cell's text, a value the producer gave wins, and a masked cell's text is no
// value.
func TestAStatusACountOrATimeWithNoValueIsValuedAsItsText(t *testing.T) {
	pods := Table{Columns: []Column{
		{Name: "Name"}, {Name: "Status", Kind: KindStatus}, {Name: "Restarts", Kind: KindNumber}, {Name: "Size", Kind: KindBytes},
		{Name: "Started", Kind: KindTimestamp},
	}}
	pods.Rows = [][]string{
		{"web", "Running", "3", "12", "2026-03-04T06:06:07+01:00"},
		{"db", "WARN lagging · 40s", "1,024", "1.2 GiB", "Mar 4 06:06"},
		{"job", "—", "-", "", "0001-01-01T00:00:00Z"},
		{"api", "n/a", "007", "0", "2026-03-04"},
	}
	pods.Add(Plain("cron"), Status("did not answer", "did not answer"), Count(5), Bytes(2048),
		Time("yesterday", time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)))
	raw, err := Marshal(Envelope{View: pods})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Values [][]any `json:"values"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := [][]any{
		{nil, "running", json.Number("3"), nil, "2026-03-04T05:06:07Z"},
		{nil, "warn", nil, nil, nil},
		{nil, nil, nil, nil, nil},
		{nil, nil, nil, nil, nil},
		{nil, "did not answer", json.Number("5"), json.Number("2048"), "2026-03-03T00:00:00Z"},
	}
	if len(got.Values) != len(want) {
		t.Fatalf("values = %v in %s", got.Values, raw)
	}
	for i := range want {
		if !slices.Equal(got.Values[i], want[i]) {
			t.Errorf("row %d (%q) is valued %v, want %v", i, pods.Rows[i], got.Values[i], want[i])
		}
	}

	pods.Redacted = []string{"Status", "Restarts", "Started"}
	masked := typedValues(Redact(pods).(Table))
	for i, row := range masked {
		if row[1] != nil || row[2] != nil || row[4] != nil {
			t.Errorf("row %d's masked cells are valued %v, %v and %v", i, row[1], row[2], row[4])
		}
	}
}

// A masked cell's value goes with it, and values are cut to the columns and
// rows the cells are: a secret's size is the secret's to keep.
func TestRedactDropsAMaskedCellsValue(t *testing.T) {
	d := disk()
	d.Redacted = []string{"Size"}
	d.Values[1] = append(d.Values[1], "past the last column")
	r := Redact(d).(Table)
	if r.Rows[0][1] != Mask || r.Values[0][1] != "" {
		t.Errorf("the masked size reads %q with value %q", r.Rows[0][1], r.Values[0][1])
	}
	if r.Values[0][2] != "72.4" {
		t.Errorf("an unmasked value beside it reads %q", r.Values[0][2])
	}
	if len(r.Values[1]) != len(r.Rows[1]) {
		t.Errorf("a row's values were not cut to its cells: %q", r.Values[1])
	}
	if d.Values[0][1] != "994662584320" {
		t.Errorf("Redact changed the caller's values: %q", d.Values[0])
	}
}

// The values are strings a model reads beside the cells, so the walk that
// cleans every string visits them too.
func TestMapStringsVisitsTheValues(t *testing.T) {
	d := disk()
	d.Values[0][3] = "warn\u200b"
	got, _ := MapStrings(d, func(s string) string { return strings.ReplaceAll(s, "\u200b", "") }).(Table)
	if got.Values[0][3] != "warn" {
		t.Errorf("the value reads %q after the walk", got.Values[0][3])
	}
}
