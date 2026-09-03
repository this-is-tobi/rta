package view

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEnvelopeJSON(t *testing.T) {
	tests := []struct {
		name     string
		view     View
		wantType string
		wantKeys []string
	}{
		{"text", Text{Body: "hello"}, "text", []string{"body"}},
		{"keyvalue", KeyValue{Pairs: []Pair{{Key: "os", Value: "darwin"}}}, "keyvalue", []string{"pairs"}},
		{"table", Table{Columns: []Column{{Name: "PID", Kind: KindNumber}}, Rows: [][]string{{"1"}}, Total: 1}, "table", []string{"columns", "rows"}},
		{"tree", Tree{Roots: []Node{{Label: "root"}}}, "tree", []string{"roots"}},
		{"error", &Error{Code: "x.y", Message: "boom", Hint: "run rta doctor"}, "error", []string{"code", "message", "hint"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(Envelope{View: tt.view})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if m["type"] != tt.wantType {
				t.Errorf("type = %v, want %v", m["type"], tt.wantType)
			}
			for _, k := range tt.wantKeys {
				if _, ok := m[k]; !ok {
					t.Errorf("missing key %q in %s", k, raw)
				}
			}
		})
	}
}

func TestErrorContract(t *testing.T) {
	e := Errorf("pg.conn.refused", "connection refused to %s", "db:5432").WithHint("is the tunnel up?")
	if e.Code != "pg.conn.refused" || e.Hint != "is the tunnel up?" {
		t.Fatalf("unexpected error: %+v", e)
	}
	var err error = e
	if err.Error() != "connection refused to db:5432" {
		t.Errorf("Error() = %q", err.Error())
	}
	if e.Refusal {
		t.Error("Errorf must not mark a refusal — only the author's explicit choice does")
	}
}

func TestRefusefMarksAPolicyRefusal(t *testing.T) {
	e := Refusef("keys.human", "%s is for the person at the terminal", "keys.backup").
		WithHint("ask the operator")
	if !e.Refusal {
		t.Fatalf("Refusef must mark the error a refusal: %+v", e)
	}
	// WithHint copies; a copy that dropped the flag would silently turn the
	// gate's no back into "the work broke" in the ledger.
	if e.Code != "keys.human" || e.Hint != "ask the operator" {
		t.Fatalf("unexpected error: %+v", e)
	}
	if e.Message != "keys.backup is for the person at the terminal" {
		t.Fatalf("unexpected message: %q", e.Message)
	}
}

func TestAsError(t *testing.T) {
	if AsError(nil, "x") != nil {
		t.Error("nil should stay nil")
	}
	coded := &Error{Code: "a.b", Message: "m"}
	if got := AsError(coded, "fallback"); got.Code != "a.b" {
		t.Errorf("coded error must keep its code, got %q", got.Code)
	}
	if got := AsError(errors.New("plain"), "sys.internal"); got.Code != "sys.internal" || got.Message != "plain" {
		t.Errorf("foreign error not wrapped: %+v", got)
	}
}

func TestRedaction(t *testing.T) {
	kv := KeyValue{Pairs: []Pair{{Key: "password", Value: "hunter2"}}, Redacted: []string{"password"}}
	if !kv.IsRedacted("password") || kv.IsRedacted("user") {
		t.Error("IsRedacted misbehaves")
	}
}

// TestSectionsEncodeNested: a composite keeps every child discriminated, so
// agents can switch on "type" at any depth.
func TestSectionsEncodeNested(t *testing.T) {
	v := Sections{Items: []Section{
		{Title: "cpu", View: Chart{Kind: ChartBar, Max: 100, Series: []Series{{Name: "core0", Points: []float64{5}}}}},
		{Title: "storage", View: Table{Columns: []Column{{Name: "Mount"}}, Rows: [][]string{{"/"}}}},
	}}
	raw, err := json.Marshal(Envelope{View: v})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Type  string `json:"type"`
		Items []struct {
			Title string         `json:"title"`
			View  map[string]any `json:"view"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "sections" {
		t.Errorf("type = %q, want sections", got.Type)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %d", len(got.Items))
	}
	if got.Items[0].Title != "cpu" || got.Items[0].View["type"] != "chart" {
		t.Errorf("first section = %+v", got.Items[0])
	}
	if got.Items[1].View["type"] != "table" {
		t.Errorf("second section type = %v", got.Items[1].View["type"])
	}
}

func TestSectionsTypeOf(t *testing.T) {
	if got := TypeOf(Sections{}); got != "sections" {
		t.Errorf("TypeOf(Sections) = %q", got)
	}
}

func TestRedactMasksOnlyMarkedKeys(t *testing.T) {
	kv := KeyValue{
		Pairs:    []Pair{{Key: "user", Value: "tobi"}, {Key: "password", Value: "hunter2"}},
		Redacted: []string{"password"},
	}
	got := Redact(kv).(KeyValue)
	if got.Pairs[0].Value != "tobi" {
		t.Errorf("non-redacted field changed: %q", got.Pairs[0].Value)
	}
	if got.Pairs[1].Value != Mask {
		t.Errorf("redacted field not masked: %q", got.Pairs[1].Value)
	}
	// The original is untouched: callers that hold onto the source value
	// (e.g. a capability re-checking its own output) don't get corrupted data.
	if kv.Pairs[1].Value != "hunter2" {
		t.Errorf("Redact mutated its input: %q", kv.Pairs[1].Value)
	}
}

// TestRedactRecursesIntoSections: composing a view must never strip its
// protection. A detail page is assembled from the very views individual
// capabilities return, so a field marked Redacted by its producer has to
// stay masked once some page embeds it — at any depth.
func TestRedactRecursesIntoSections(t *testing.T) {
	secret := KeyValue{
		Pairs:    []Pair{{Key: "user", Value: "tobi"}, {Key: "token", Value: "hunter2"}},
		Redacted: []string{"token"},
	}
	page := Sections{Items: []Section{
		{Title: "plain", View: Text{Body: "nothing to hide"}},
		{Title: "credentials", View: secret},
		{Title: "nested", View: Sections{Items: []Section{{Title: "deeper", View: secret}}}},
	}}

	got := Redact(page).(Sections)
	if got.Items[1].View.(KeyValue).Pairs[1].Value != Mask {
		t.Errorf("section field not masked: %+v", got.Items[1].View)
	}
	inner := got.Items[2].View.(Sections).Items[0].View.(KeyValue)
	if inner.Pairs[1].Value != Mask {
		t.Errorf("nested section field not masked: %+v", inner)
	}
	if inner.Pairs[0].Value != "tobi" {
		t.Errorf("nested non-redacted field changed: %q", inner.Pairs[0].Value)
	}
	// And the source page is left alone, at every depth.
	if page.Items[1].View.(KeyValue).Pairs[1].Value != "hunter2" {
		t.Error("Redact mutated the composite it was given")
	}
	if secret.Pairs[1].Value != "hunter2" {
		t.Error("Redact mutated a view shared between sections")
	}
}

// A section carrying no view at all must not panic the enforcement point.
func TestRedactToleratesEmptySections(t *testing.T) {
	got := Redact(Sections{Items: []Section{{Title: "empty"}}}).(Sections)
	if len(got.Items) != 1 || got.Items[0].View != nil {
		t.Errorf("empty section mangled: %+v", got)
	}
}

func TestRedactPassesThroughOtherViews(t *testing.T) {
	tbl := Table{Columns: []Column{{Name: "x"}}, Rows: [][]string{{"y"}}}
	if got := Redact(tbl); got.(Table).Rows[0][0] != "y" {
		t.Errorf("Redact altered a table with no Redacted columns: %v", got)
	}
	if got := Redact(KeyValue{Pairs: []Pair{{Key: "a", Value: "b"}}}); got.(KeyValue).Pairs[0].Value != "b" {
		t.Error("Redact touched a KeyValue with no Redacted keys")
	}
	tree := Tree{Roots: []Node{{Label: "root"}}}
	if got := Redact(tree); got.(Tree).Roots[0].Label != "root" {
		t.Errorf("Redact altered a Tree: %v", got)
	}
	if got := Redact(Text{Body: "plain"}); got.(Text).Body != "plain" {
		t.Errorf("Redact altered a Text: %v", got)
	}
}

// A table is the other shape with a stable name to hang a secret on, and the
// one the catalogue reaches for whenever there is more than one of something —
// which is exactly when a list of credentials shows up. Before this, only
// KeyValue could declare a secret, so a capability returning a table of them
// had no way to say so and no error telling it that.
func TestRedactMasksOnlyMarkedColumns(t *testing.T) {
	tbl := Table{
		Columns:  []Column{{Name: "key"}, {Name: "value"}, {Name: "updated"}},
		Rows:     [][]string{{"api-token", "hunter2", "2m ago"}, {"db-password", "s3cret", "1d ago"}},
		Redacted: []string{"value"},
	}
	got := Redact(tbl).(Table)
	for i, row := range got.Rows {
		if row[1] != Mask {
			t.Errorf("row %d: secret column not masked: %q", i, row[1])
		}
	}
	if got.Rows[0][0] != "api-token" || got.Rows[0][2] != "2m ago" {
		t.Errorf("a non-redacted column changed: %v", got.Rows[0])
	}
	// The source is untouched, the same promise KeyValue already makes.
	if tbl.Rows[0][1] != "hunter2" {
		t.Errorf("Redact mutated its input: %q", tbl.Rows[0][1])
	}
}

// Columns are named rather than indexed precisely so that this cannot happen:
// insert a column, and an index-based declaration silently starts protecting
// its neighbour while the secret goes out in the clear.
func TestRedactFollowsAColumnWhenTheOrderChanges(t *testing.T) {
	reordered := Table{
		Columns:  []Column{{Name: "value"}, {Name: "key"}},
		Rows:     [][]string{{"hunter2", "api-token"}},
		Redacted: []string{"value"},
	}
	got := Redact(reordered).(Table)
	if got.Rows[0][0] != Mask {
		t.Errorf("the secret column moved and lost its masking: %v", got.Rows[0])
	}
	if got.Rows[0][1] != "api-token" {
		t.Errorf("masking followed the position instead of the name: %v", got.Rows[0])
	}
}

// Rows are [][]string with nothing enforcing their width, so a ragged one is
// reachable from any producer that builds rows in a loop. The enforcement
// point is the last place that may panic.
func TestRedactToleratesRaggedRows(t *testing.T) {
	tbl := Table{
		Columns:  []Column{{Name: "key"}, {Name: "value"}},
		Rows:     [][]string{{"lonely"}, {"a", "hunter2"}, {"a", "hunter2", "extra"}, {}},
		Redacted: []string{"value"},
	}
	got := Redact(tbl).(Table)
	if len(got.Rows[0]) != 1 || got.Rows[0][0] != "lonely" {
		t.Errorf("short row mangled: %v", got.Rows[0])
	}
	if got.Rows[1][1] != Mask {
		t.Errorf("well-formed row not masked: %v", got.Rows[1])
	}
	// A cell past the last column has no name, so nothing can declare it
	// secret — it is passed through rather than guessed at.
	if got.Rows[2][1] != Mask || got.Rows[2][2] != "extra" {
		t.Errorf("over-long row mishandled: %v", got.Rows[2])
	}
	if len(got.Rows[3]) != 0 {
		t.Errorf("empty row mangled: %v", got.Rows[3])
	}
}

// The recursion promise, for the shape a detail page is most likely to embed.
func TestRedactRecursesIntoSectionedTables(t *testing.T) {
	secret := Table{
		Columns:  []Column{{Name: "key"}, {Name: "value"}},
		Rows:     [][]string{{"api-token", "hunter2"}},
		Redacted: []string{"value"},
	}
	page := Sections{Items: []Section{{Title: "secrets", View: secret}}}
	got := Redact(page).(Sections).Items[0].View.(Table)
	if got.Rows[0][1] != Mask {
		t.Errorf("embedded table not masked: %v", got.Rows[0])
	}
	if secret.Rows[0][1] != "hunter2" {
		t.Error("Redact mutated a table shared between sections")
	}
}

// Found independently by four of six review lenses, and the reason it matters
// is where it lands: Envelope.MarshalJSON is every `-o json`, every `-o yaml`
// and every MCP tools/call. On the MCP path it panics inside the server's
// per-call goroutine, which no caller-side recover can contain — so one
// plugin returning a titled section it could not fill takes down the server
// for every other tool the agent had open.
//
// Four other call sites already treat a nil view as legal; this one panicked.
func TestNilViewEncodesInsteadOfPanicking(t *testing.T) {
	cases := map[string]View{
		"a bare nil view":            nil,
		"a section with no view":     Sections{Items: []Section{{Title: "gap"}}},
		"a nil view inside a nested": Sections{Items: []Section{{Title: "outer", View: Sections{Items: []Section{{Title: "gap"}}}}}},
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(Envelope{View: v})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !json.Valid(raw) {
				t.Fatalf("invalid JSON: %s", raw)
			}
			if !strings.Contains(string(raw), `"type"`) {
				t.Errorf("no discriminator: %s", raw)
			}
			// ToMap is the YAML and MCP path, and shares the same encoder.
			if _, err := ToMap(v); err != nil {
				t.Errorf("ToMap: %v", err)
			}
		})
	}
}
