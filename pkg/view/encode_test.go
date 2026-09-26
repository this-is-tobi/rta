package view

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// A git author, a URL's query and a comparison come out as written, at every
// depth. encoding/json re-escapes a MarshalJSON method's output unless the
// encoder calling it was told not to, so a nested section is where a partial
// fix would show.
func TestMarshalLeavesHTMLCharactersAlone(t *testing.T) {
	v := Sections{Items: []Section{{ID: "log", Title: "a <b> & c", View: Table{
		Columns: []Column{{Name: "Author"}},
		Rows:    [][]string{{"Ada <ada@example.com>"}, {"https://x.example/?a=1&b=2"}},
	}}}}
	data, err := Marshal(Envelope{View: v})
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{`"a <b> & c"`, `"Ada <ada@example.com>"`, `"https://x.example/?a=1&b=2"`} {
		if !strings.Contains(got, want) {
			t.Errorf("encoded = %s\nwant it to hold %s", got, want)
		}
	}
	if strings.Contains(got, `\u00`) {
		t.Errorf("encoded = %s, still escaped", got)
	}
	// Still one JSON value, and still the same one a parser reads back.
	var back map[string]any
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("not JSON any more: %v", err)
	}
	indented, err := MarshalIndent(Envelope{View: Text{Body: "x&y"}}, "", "  ")
	if err != nil || string(indented) != "{\n  \"body\": \"x&y\",\n  \"type\": \"text\"\n}" {
		t.Errorf("indented = %q, %v", indented, err)
	}
}

// What a terminal acts on and encoding/json writes raw comes out escaped, at
// every depth and indented or not — DEL, the C1 controls and the characters
// that reorder text — so `-o json` on a terminal is drawn as it is stored; a
// parser reads back the same string. The marks either side of the reorder
// characters in their block, and everything else, are left as they are.
func TestMarshalEscapesWhatATerminalActsOn(t *testing.T) {
	for _, r := range []rune{0x7f, 0x85, 0x9b, 0x9d, 0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2067, 0x2068, 0x2069} {
		name := "invoice" + string(r) + "fdp.exe"
		v := Sections{Items: []Section{{Title: name, View: Table{
			Columns: []Column{{Name: "name"}}, Rows: [][]string{{name}},
		}}}}
		for _, indent := range []string{"", "  "} {
			data, err := MarshalIndent(Envelope{View: v}, "", indent)
			if err != nil {
				t.Fatal(err)
			}
			if strings.ContainsRune(string(data), r) {
				t.Errorf("U+%04X: encoded = %s, still holds the character", r, data)
			}
			if want := fmt.Sprintf(`\u%04x`, r); strings.Count(string(data), want) != 2 {
				t.Errorf("U+%04X: encoded = %s, want %s twice", r, data, want)
			}
			var back struct {
				Items []struct {
					Title string `json:"title"`
				} `json:"items"`
			}
			if err := json.Unmarshal(data, &back); err != nil || len(back.Items) != 1 || back.Items[0].Title != name {
				t.Errorf("U+%04X: read back as %+v (%v), want the title unchanged", r, back, err)
			}
		}
	}
	plain := "abc" + string(rune(0x200f)) + string(rune(0x2065)) + string(rune(0x206a)) + "é"
	data, err := Marshal(Text{Body: plain})
	if err != nil || !strings.Contains(string(data), plain) {
		t.Errorf("encoded = %s (%v), want %q as it is", data, err, plain)
	}
}

// A section's id must survive encoding.
//
// Title is prose meant for a person and free to change; ID is the stable
// handle a script or an agent addresses a section by, which is why
// plugin.Page assigns one and the CLI renderer's comments say the id exists
// so a script can name a section. Section.MarshalJSON dropped it, so on every
// surface that encodes — -o json, -o yaml, and both halves of an MCP result —
// no machine consumer could use the identifier the contract promises.
func TestASectionKeepsItsIDThroughJSON(t *testing.T) {
	v := Sections{Items: []Section{
		{ID: "summary", Title: "summary", View: Text{Body: "x"}},
		{Title: "untitled section", View: Text{Body: "y"}},
	}}
	data, err := json.Marshal(Envelope{View: v})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Items []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %d\n%s", len(got.Items), data)
	}
	if got.Items[0].ID != "summary" {
		t.Errorf("id = %q, want summary:\n%s", got.Items[0].ID, data)
	}
	// A section built by hand need not have one, and `"id":""` is not an
	// identifier — so it is omitted rather than emitted blank.
	if strings.Contains(string(data), `"id":""`) {
		t.Errorf("an empty id was emitted:\n%s", data)
	}
}

// A redacted name must survive cleaning, or the mask comes off.
//
// Redacted lists key and column names and is matched against Pair.Key and
// Column.Name, so the two sides have to move together. MapStrings moved one
// of them: a key containing a zero-width space, marked Redacted, went through
// internal/mcp.viewResult — which maps with textclean.Model and then calls
// Redact — and arrived at the model as plaintext, because the cleaned key no
// longer matched the uncleaned entry in Redacted.
//
// Reproduced exactly this way before the fix. It is the worst kind of leak
// available here: every individual component behaved as documented.
func TestRedactionSurvivesCleaning(t *testing.T) {
	const key = "pass\u200bword" // zero-width space
	strip := func(s string) string { return strings.ReplaceAll(s, "\u200b", "") }

	kv := KeyValue{
		Pairs:    []Pair{{Key: key, Value: "hunter2"}},
		Redacted: []string{key},
	}
	got := Redact(MapStrings(kv, strip)).(KeyValue)
	if got.Pairs[0].Value != Mask {
		t.Errorf("KeyValue: value is %q, want it masked (key became %q, Redacted holds %q)",
			got.Pairs[0].Value, got.Pairs[0].Key, got.Redacted)
	}

	tbl := Table{
		Columns:  []Column{{Name: "user"}, {Name: key}},
		Rows:     [][]string{{"tobi", "hunter2"}},
		Redacted: []string{key},
	}
	gotT := Redact(MapStrings(tbl, strip)).(Table)
	if gotT.Rows[0][1] != Mask {
		t.Errorf("Table: cell is %q, want it masked (column became %q, Redacted holds %q)",
			gotT.Rows[0][1], gotT.Columns[1].Name, gotT.Redacted)
	}
	// The untouched column is not masked by accident.
	if gotT.Rows[0][0] != "tobi" {
		t.Errorf("Table: an unredacted cell was masked: %q", gotT.Rows[0][0])
	}
}

// A collection the JSON names is an array when it is empty, never null.
//
// A tree with no roots — what a plugin's empty tree is by the time it has
// crossed the wire, where proto3 cannot tell empty from absent — encoded as
// "roots": null, and `jq '.roots[]'` failed on it with "Cannot iterate over
// null" where an empty array yields nothing and exits 0. A table's rows were
// spared only because Redact copies them into a new slice.
func TestAnEmptyCollectionIsAnArray(t *testing.T) {
	for _, c := range []struct {
		v    View
		want string
	}{
		{KeyValue{}, `"pairs":[]`},
		{Table{}, `"columns":[]`},
		{Table{}, `"rows":[]`},
		{Tree{}, `"roots":[]`},
		{Chart{Kind: ChartBar}, `"series":[]`},
		{Chart{Kind: ChartLine, Series: []Series{{Name: "cpu"}}}, `"points":[]`},
		{Sections{}, `"items":[]`},
		{Sections{Items: []Section{{Title: "t", View: Tree{}}}}, `"roots":[]`},
	} {
		data, err := Marshal(Envelope{View: c.v})
		if err != nil || !strings.Contains(string(data), c.want) || strings.Contains(string(data), "null") {
			t.Errorf("%s = %s (%v), want %s", TypeOf(c.v), data, err, c.want)
		}
	}
}
