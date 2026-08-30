package cli

import (
	"encoding/json"
	"strings"
	"testing"

	yaml "github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// **Every json and yaml output has to parse, whatever is in the data.**
//
// The two machine-readable formats are a contract with a pipe: somebody runs
// `rta … -o json | jq`, and a view that happens to contain a quote, a
// newline, a control character or a string YAML would rather read as a
// number must not be what breaks it. Nothing in a view is written by rta —
// it is an HTTP body, a DNS record, a certificate subject, a filename, a
// task somebody typed — so "whatever is in the data" is the only useful
// scope.
//
// Round-tripped rather than merely parsed, because parsing is the weaker
// half. YAML is full of strings that come back as something else: `no` is a
// boolean, `0755` is an octal int, `1.0` is a float, `null` is nothing, and
// a consumer reading `.rows[0][4]` gets a type it did not ask for. An output
// that parses into the wrong value is worse than one that fails to parse,
// because nothing anywhere reports it.

// hostile is one string per way a serializer can be wrong about it.
var hostile = []struct{ name, value string }{
	{"quote", `he said "hello"`},
	{"backslash", `C:\Users\tobi\path`},
	{"both", `"\" and more`},
	{"newline", "line one\nline two"},
	{"tab", "col\tcol"},
	{"carriage return", "over\rwritten"},
	{"ansi escape", "\x1b]52;c;cGF5bG9hZA==\x07"},
	{"nul", "before\x00after"},
	{"unicode", "café · naïve · 日本語 · 🔐"},
	// The YAML retyping family. Each of these is a string that a bare scalar
	// would come back from as some other type entirely.
	{"yaml no", "no"},
	{"yaml yes", "yes"},
	{"yaml true", "true"},
	{"yaml off", "off"},
	{"yaml null", "null"},
	{"yaml tilde", "~"},
	{"octal-looking", "0755"},
	{"float-looking", "1.0"},
	{"int-looking", "42"},
	{"sexagesimal-looking", "12:30:45"},
	{"leading dash", "- not a list item"},
	{"leading hash", "# not a comment"},
	{"leading star", "*not-an-alias"},
	{"leading amp", "&not-an-anchor"},
	{"leading bang", "!not-a-tag"},
	{"colon space", "key: value"},
	{"block scalar", "|\n  not a block"},
	{"empty", ""},
	{"only spaces", "   "},
	{"very long", strings.Repeat("x", 5000)},
}

// decoders is the pair a caller actually reaches for.
var decoders = map[Format]func([]byte, any) error{
	JSON: json.Unmarshal,
	YAML: yaml.Unmarshal,
}

// A table cell is where most of a capability's data ends up, so it is where
// this is checked one string at a time.
func TestEveryTableCellSurvivesJSONAndYAML(t *testing.T) {
	for _, h := range hostile {
		t.Run(h.name, func(t *testing.T) {
			v := view.Table{
				Columns: []view.Column{{Name: "Key"}, {Name: "Value"}},
				Rows:    [][]string{{"subject", h.value}},
				Total:   1,
			}
			for _, f := range []Format{JSON, YAML} {
				out := render(t, v, f)
				var got struct {
					Rows [][]string `json:"rows" yaml:"rows"`
				}
				if err := decoders[f]([]byte(out), &got); err != nil {
					t.Fatalf("%s does not parse: %v\n%s", f, err, out)
				}
				if len(got.Rows) != 1 || len(got.Rows[0]) != 2 {
					t.Fatalf("%s lost the row shape: %v", f, got.Rows)
				}
				// Every format but json is cleaned of terminal control
				// sequences before it is written — see sanitize.go, which
				// records what an unescaped OSC 52 in an HTTP body cost a
				// reader's clipboard. So the comparison is against the
				// cleaned string, and what is asserted is that nothing
				// *else* changed on the way out and back.
				want := h.value
				if f != JSON {
					want = cleanedFor(t, h.value)
				}
				if got.Rows[0][1] != want {
					t.Errorf("%s changed the value.\n got: %q\nwant: %q", f, got.Rows[0][1], want)
				}
			}
		})
	}
}

// The same guarantee for the other two shapes a capability commonly returns.
func TestKeyValueAndTextSurviveJSONAndYAML(t *testing.T) {
	for _, h := range hostile {
		t.Run(h.name, func(t *testing.T) {
			for _, f := range []Format{JSON, YAML} {
				kv := view.KeyValue{Pairs: []view.Pair{{Key: "subject", Value: h.value}}}
				var gotKV struct {
					Pairs []struct {
						Key   string `json:"key" yaml:"key"`
						Value string `json:"value" yaml:"value"`
					} `json:"pairs" yaml:"pairs"`
				}
				out := render(t, kv, f)
				if err := decoders[f]([]byte(out), &gotKV); err != nil {
					t.Fatalf("KeyValue %s does not parse: %v\n%s", f, err, out)
				}
				if len(gotKV.Pairs) != 1 {
					t.Fatalf("KeyValue %s lost the pair: %s", f, out)
				}

				txt := view.Text{Body: h.value}
				var gotText struct {
					Body string `json:"body" yaml:"body"`
				}
				out = render(t, txt, f)
				if err := decoders[f]([]byte(out), &gotText); err != nil {
					t.Fatalf("Text %s does not parse: %v\n%s", f, err, out)
				}
			}
		})
	}
}

// A tree and a sectioned page nest, and nesting is where an encoder that is
// almost right stops being right at all.
func TestNestedViewsSurviveJSONAndYAML(t *testing.T) {
	nasty := "no\n- item: \"quoted\"\t#comment"
	tree := view.Tree{Roots: []view.Node{{
		Label: nasty, Detail: nasty,
		Children: []view.Node{{Label: nasty, Detail: "0755"}},
	}}}
	for _, f := range []Format{JSON, YAML} {
		out := render(t, tree, f)
		var m map[string]any
		if err := decoders[f]([]byte(out), &m); err != nil {
			t.Fatalf("Tree %s does not parse: %v\n%s", f, err, out)
		}
	}
}

// cleanedFor is what a non-json format is expected to write for value: the
// same string with terminal control sequences removed, which is the one
// transformation sanitize is allowed to make.
func cleanedFor(t *testing.T, value string) string {
	t.Helper()
	v := sanitize(view.Table{
		Columns: []view.Column{{Name: "Value"}},
		Rows:    [][]string{{value}},
	})
	return v.(view.Table).Rows[0][0]
}

// The specific case that started this, kept as its own test because the
// sweep above would pass again the moment somebody "simplified" yamlSafe
// away: a tab in a value, through the format a script parses.
//
// It is the worst shape a bug in a machine-readable output can take. It does
// not fail, it does not warn, it does not corrupt the syntax — goccy/go-yaml
// writes `v: col<TAB>col` as a plain scalar, which cannot carry a tab, so
// every parser folds it away and hands the consumer a value that is almost
// the one rta had.
func TestATabSurvivesYAMLBecausePlainScalarsCannotCarryOne(t *testing.T) {
	v := view.Table{
		Columns: []view.Column{{Name: "Key"}, {Name: "Value"}},
		Rows:    [][]string{{"record", "v=spf1\tinclude:_spf.example.com\t-all"}},
	}
	out := render(t, v, YAML)
	if strings.Contains(out, "value: v=spf1\tinclude") {
		t.Errorf("the tab was written into a plain scalar:\n%s", out)
	}
	var got struct {
		Rows [][]string `yaml:"rows"`
	}
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("does not parse: %v\n%s", err, out)
	}
	if want := "v=spf1\tinclude:_spf.example.com\t-all"; got.Rows[0][1] != want {
		t.Errorf("the tabs did not survive.\n got: %q\nwant: %q", got.Rows[0][1], want)
	}
}

// …and nothing that does not need quoting gets it, or every yaml output rta
// produces becomes a wall of escapes for no reason.
func TestOnlyTheValuesThatNeedQuotingGetIt(t *testing.T) {
	v := view.Table{
		Columns: []view.Column{{Name: "Key"}, {Name: "Value"}},
		Rows:    [][]string{{"plain", "an ordinary sentence"}},
	}
	if out := render(t, v, YAML); !strings.Contains(out, "- an ordinary sentence") {
		t.Errorf("an ordinary string was quoted unnecessarily:\n%s", out)
	}
}

// **The two machine-readable formats must agree about types, not only about
// values.**
//
// view.ToMap round-trips the envelope through encoding/json to get a generic
// map, and encoding/json decodes every number into a float64. Through json
// that is invisible — Go writes a whole float64 as `5` — and through yaml it
// is not: the same table's row count came out as `total: 5.0`. A consumer
// cannot work around two formats disagreeing about a field's type, because
// each of them looks right on its own.
func TestNumbersHaveTheSameTypeInJSONAndYAML(t *testing.T) {
	v := view.Table{
		Columns: []view.Column{{Name: "Key"}},
		Rows:    [][]string{{"a"}, {"b"}, {"c"}},
		Total:   3,
	}
	var fromJSON, fromYAML map[string]any
	if err := json.Unmarshal([]byte(render(t, v, JSON)), &fromJSON); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte(render(t, v, YAML)), &fromYAML); err != nil {
		t.Fatal(err)
	}
	// yaml.Unmarshal into any gives uint64/int for an integer and float64 for
	// a float, so the assertion is on the rendered text: `3`, never `3.0`.
	if out := render(t, v, YAML); strings.Contains(out, "total: 3.0") {
		t.Errorf("an integer count was written as a float:\n%s", out)
	}
	if !strings.Contains(render(t, v, YAML), "total: 3") {
		t.Errorf("the total is missing from the yaml:\n%s", render(t, v, YAML))
	}
	if fromJSON["total"] != float64(3) {
		t.Errorf("json total = %#v", fromJSON["total"])
	}
}

// A genuine fraction stays one — the fix puts the type back, it does not
// round the number off.
func TestAFractionStaysAFraction(t *testing.T) {
	v := view.Chart{
		Kind:   view.ChartBar,
		Series: []view.Series{{Name: "load", Points: []float64{1.5, 2, 0.25}}},
	}
	out := render(t, v, YAML)
	for _, want := range []string{"1.5", "0.25"} {
		if !strings.Contains(out, want) {
			t.Errorf("the fractional value %s was flattened:\n%s", want, out)
		}
	}
	// …and a whole number beside it is still whole.
	if strings.Contains(out, "2.0") {
		t.Errorf("a whole value was written as a float:\n%s", out)
	}
}
