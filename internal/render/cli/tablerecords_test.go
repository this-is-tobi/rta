package cli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

func disks() view.Table {
	t := view.Table{
		Columns: []view.Column{
			{Name: "Mount"}, {Name: "Size", Kind: view.KindBytes}, {Name: "Use%", Kind: view.KindUsage}, {Name: "Status", Kind: view.KindStatus},
		},
		Total: 3,
	}
	t.Add(view.Plain("/"), view.Bytes(int64(994662584320)), view.Number("72%", 72.4), view.Status("WARN >80%", "warn"))
	t.Add(view.Plain("/boot"), view.Bytes(int64(1073741824)), view.Number("9%", 9), view.Plain("ok"))
	t.Add(view.Plain("total"), view.Plain(""), view.Plain(""), view.Plain(""))
	return t
}

// number reads a figure however the decoder under it typed it: json's float64,
// and the uint64, int64 or float64 yaml gives a whole number or a fraction.
func number(t *testing.T, v any) float64 {
	t.Helper()
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case uint64:
		return float64(n)
	}
	t.Fatalf("%#v is not a number", v)
	return 0
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// decodeRecords reads the "records" of an output back in the format it was
// written in, as a consumer does.
func decodeRecords(t *testing.T, out string, f Format) []map[string]any {
	t.Helper()
	var got struct {
		Records []map[string]any `json:"records" yaml:"records"`
	}
	if err := decoders[f]([]byte(out), &got); err != nil {
		t.Fatalf("%s does not parse: %v\n%s", f, err, out)
	}
	return got.Records
}

// A table read by a script is a row to a record, whichever of the two formats it
// asked for: the same keys, a figure that is a figure in both, and the text a
// person reads in a status's cell and not the word it grades by.
func TestATableCarriesRecordsInJSONAndYAML(t *testing.T) {
	var records [2][]map[string]any
	for i, f := range []Format{JSON, YAML} {
		records[i] = decodeRecords(t, render(t, disks(), f), f)
		if len(records[i]) != 3 {
			t.Fatalf("%s has %d records, want one to a row", f, len(records[i]))
		}
	}
	for row := range 3 {
		if a, b := keysOf(records[0][row]), keysOf(records[1][row]); !slices.Equal(a, b) {
			t.Errorf("row %d: json keys %q, yaml keys %q", row, a, b)
		}
	}
	for i, f := range []Format{JSON, YAML} {
		first, last := records[i][0], records[i][2]
		if first["Mount"] != "/" || first["Status"] != "WARN >80%" {
			t.Errorf("%s: the first record reads %v", f, first)
		}
		if n := number(t, first["Size"]); n != 994662584320 {
			t.Errorf("%s: Size is %v, want the bytes as a number", f, first["Size"])
		}
		if n := number(t, first["Use%"]); n != 72.4 {
			t.Errorf("%s: Use%% is %v, want 72.4 as a number", f, first["Use%"])
		}
		if last["Size"] != nil || last["Use%"] != nil || last["Status"] != "" {
			t.Errorf("%s: a row with no figures reads %v, want null in the numeric cells and the empty text in the status", f, last)
		}
	}
}

// The rows, the values and the table's own fields are what they were: records
// are a third way to read the table, never a different table.
func TestRecordsLeaveTheRestOfTheTableAsItWas(t *testing.T) {
	for _, f := range []Format{JSON, YAML} {
		var got struct {
			Rows    [][]string `json:"rows" yaml:"rows"`
			Values  [][]any    `json:"values" yaml:"values"`
			Columns []struct {
				Name string `json:"name" yaml:"name"`
			} `json:"columns" yaml:"columns"`
			Total int `json:"total" yaml:"total"`
		}
		out := render(t, disks(), f)
		if err := decoders[f]([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Rows) != 3 || len(got.Values) != 3 || len(got.Columns) != 4 || got.Total != 3 {
			t.Errorf("%s: %d rows, %d values, %d columns, total %d", f, len(got.Rows), len(got.Values), len(got.Columns), got.Total)
		}
		if got.Rows[0][1] != "926.4 GiB" {
			t.Errorf("%s: the cell reads %q, want the text a person reads", f, got.Rows[0][1])
		}
	}
}

// Only the two formats a script reads a table in are given them. The cells a
// person reads, csv's and a page's flattening of the same encoding, stay what
// they were.
func TestRecordsAreInJSONAndYAMLAndNoOtherFormat(t *testing.T) {
	page := view.Sections{Items: []view.Section{{ID: "disks", Title: "Disks", View: disks()}}}
	for _, v := range []view.View{disks(), page} {
		for _, f := range []Format{Pretty, CSV, Markdown} {
			if out := render(t, v, f); strings.Contains(out, "records") {
				t.Errorf("a %s in %s mentions records:\n%s", view.TypeOf(v), f, out)
			}
		}
		for _, f := range []Format{JSON, YAML} {
			if out := render(t, v, f); !strings.Contains(out, "records") {
				t.Errorf("a %s in %s has no records:\n%s", view.TypeOf(v), f, out)
			}
		}
	}
}

// A page's tables are read by the same key at any depth, in both formats.
func TestATableInAPageHasRecordsInJSONAndYAML(t *testing.T) {
	inner := view.Table{Columns: []view.Column{{Name: "N", Kind: view.KindNumber}}}
	inner.Add(view.Count(7))
	page := view.Sections{Items: []view.Section{
		{ID: "disks", Title: "Disks", View: disks()},
		{ID: "deeper", Title: "Deeper", View: view.Sections{Items: []view.Section{{ID: "inner", Title: "Inner", View: inner}}}},
	}}
	for _, f := range []Format{JSON, YAML} {
		type section struct {
			View struct {
				Records []map[string]any `json:"records" yaml:"records"`
				Items   []struct {
					View struct {
						Records []map[string]any `json:"records" yaml:"records"`
					} `json:"view" yaml:"view"`
				} `json:"items" yaml:"items"`
			} `json:"view" yaml:"view"`
		}
		var got struct {
			Records []map[string]any `json:"records" yaml:"records"`
			Items   []section        `json:"items" yaml:"items"`
		}
		out := render(t, page, f)
		if err := decoders[f]([]byte(out), &got); err != nil {
			t.Fatalf("%s does not parse: %v\n%s", f, err, out)
		}
		if got.Records != nil {
			t.Errorf("%s: the page has records of its own: %v", f, got.Records)
		}
		if r := got.Items[0].View.Records; len(r) != 3 || r[0]["Mount"] != "/" {
			t.Errorf("%s: the table in a section has records %v", f, r)
		}
		deep := got.Items[1].View.Items[0].View.Records
		if len(deep) != 1 || number(t, deep[0]["N"]) != 7 {
			t.Errorf("%s: the table a level down has records %v\n%s", f, deep, out)
		}
	}
}

// A masked column is masked in the record too, in every format: the secret is
// in none of them, the record holds the mask a person sees where the column is
// text, and null where it was a number, since Redact took the figure with the
// cell.
func TestARedactedColumnsSecretIsInNoFormatAndNoRecord(t *testing.T) {
	tbl := view.Table{
		Columns:  []view.Column{{Name: "Name"}, {Name: "Token"}, {Name: "Size", Kind: view.KindBytes}},
		Redacted: []string{"Token", "Size"},
	}
	tbl.Add(view.Plain("api"), view.Plain("hunter2-secret"), view.Bytes(int64(48151623420)))
	page := view.Sections{Items: []view.Section{{ID: "keys", Title: "Keys", View: tbl}}}

	for _, v := range []view.View{tbl, page} {
		for _, f := range []Format{Pretty, JSON, YAML, CSV, Markdown} {
			out := render(t, v, f)
			for _, secret := range []string{"hunter2-secret", "48151623420", "44.8 GiB"} {
				if strings.Contains(out, secret) {
					t.Errorf("a %s in %s shows %q:\n%s", view.TypeOf(v), f, secret, out)
				}
			}
		}
	}
	for _, f := range []Format{JSON, YAML} {
		got := decodeRecords(t, render(t, tbl, f), f)
		if len(got) != 1 || got[0]["Name"] != "api" || got[0]["Token"] != view.Mask || got[0]["Size"] != nil {
			t.Errorf("%s: the record reads %v, want the name, the mask and null", f, got)
		}
	}
}

// A column is named by whoever wrote the plugin, and a SQL alias or a header
// from a file is a string like any other. The key is the name, byte for byte, in
// json; in yaml the name has been through the cleaning every string is, and is
// what it comes back as. Neither loses a field or fails to parse for what is in
// the name.
func TestARecordIsKeyedByAnyColumnName(t *testing.T) {
	for _, h := range hostile {
		t.Run(h.name, func(t *testing.T) {
			tbl := view.Table{Columns: []view.Column{{Name: h.value}, {Name: "Other"}}, Rows: [][]string{{"first", "second"}}}
			for _, f := range []Format{JSON, YAML} {
				out := render(t, tbl, f)
				got := decodeRecords(t, out, f)
				if len(got) != 1 {
					t.Fatalf("%s: %d records in\n%s", f, len(got), out)
				}
				name := h.value
				if f != JSON {
					name = sanitize(tbl).(view.Table).Columns[0].Name
				}
				if got[0][name] != "first" || got[0]["Other"] != "second" || len(got[0]) != 2 {
					t.Errorf("%s: the record reads %q, want the name %q to hold %q\n%s", f, got[0], name, "first", out)
				}
			}
		})
	}
}

// Whatever the names are, the keys are unique: a repeat is told apart, in both
// formats, and no cell is written over another.
func TestRepeatedColumnNamesAreToldApartInBothFormats(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "id"}, {Name: "id"}, {Name: "id_2"}},
		Rows:    [][]string{{"a", "b", "c"}},
	}
	for _, f := range []Format{JSON, YAML} {
		got := decodeRecords(t, render(t, tbl, f), f)
		if len(got) != 1 || got[0]["id"] != "a" || got[0]["id_3"] != "b" || got[0]["id_2"] != "c" || len(got[0]) != 3 {
			t.Errorf("%s: the record reads %v", f, got)
		}
	}
}

// -o json is a document, and a record's field round-trips exactly in it.
func TestARecordsFieldsAreTheCellsExactly(t *testing.T) {
	tbl := view.Table{Columns: []view.Column{{Name: "Key"}, {Name: "Value"}}}
	for _, h := range hostile {
		tbl.Rows = append(tbl.Rows, []string{h.name, h.value})
	}
	var got struct {
		Records []struct{ Key, Value string }
	}
	if err := json.Unmarshal([]byte(render(t, tbl, JSON)), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != len(hostile) {
		t.Fatalf("%d records for %d rows", len(got.Records), len(hostile))
	}
	for i, h := range hostile {
		if got.Records[i].Key != h.name || got.Records[i].Value != h.value {
			t.Errorf("%s: the record reads %q, want %q", h.name, got.Records[i], h.value)
		}
	}
}
