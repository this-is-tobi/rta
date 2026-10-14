package cli

import (
	"encoding/json"
	"strings"
	"testing"

	yaml "github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rta/pkg/view"
)

func pageWithNumbers() view.KeyValue {
	return view.KeyValue{
		Pairs:   []view.Pair{{Key: "keys", Value: "16 keys, 11 folders, 2.8 KiB of values"}},
		Numbers: map[string]float64{"keys": 16, "folders": 11, "value_bytes": 2891},
	}
}

// A page's pairs are sentences for a person, and a script reading `-o json` had
// to take "16 keys, 11 folders, 2.8 KiB of values" apart to learn how many keys
// there were. The numbers it was made from are beside it, as numbers, in the two
// formats a program reads.
func TestAPagesNumbersAreNumbersInJSONAndYAML(t *testing.T) {
	var j struct {
		Numbers map[string]any `json:"numbers"`
	}
	if err := json.Unmarshal([]byte(render(t, pageWithNumbers(), JSON)), &j); err != nil {
		t.Fatal(err)
	}
	if n, ok := j.Numbers["keys"].(float64); !ok || n != 16 {
		t.Errorf("json numbers.keys = %#v, want the number 16", j.Numbers["keys"])
	}
	if n, ok := j.Numbers["value_bytes"].(float64); !ok || n != 2891 {
		t.Errorf("json numbers.value_bytes = %#v, want the number 2891", j.Numbers["value_bytes"])
	}

	var y struct {
		Numbers map[string]any `yaml:"numbers"`
	}
	out := render(t, pageWithNumbers(), YAML)
	if err := yaml.Unmarshal([]byte(out), &y); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	switch n := y.Numbers["folders"].(type) {
	case uint64:
		if n != 11 {
			t.Errorf("yaml numbers.folders = %v", n)
		}
	case int64:
		if n != 11 {
			t.Errorf("yaml numbers.folders = %v", n)
		}
	case float64:
		if n != 11 {
			t.Errorf("yaml numbers.folders = %v", n)
		}
	default:
		t.Errorf("yaml numbers.folders = %#v, want a number and not a string:\n%s", y.Numbers["folders"], out)
	}
}

// They are a second way to read the page and never a second page: what a person
// reads, in any other format, is the same as before.
func TestAPagesNumbersAreNotInTheOtherFormats(t *testing.T) {
	for _, f := range []Format{Pretty, CSV, Markdown} {
		out := render(t, pageWithNumbers(), f)
		if strings.Contains(out, "value_bytes") || strings.Contains(out, "2891") {
			t.Errorf("%s carries the machine numbers:\n%s", f, out)
		}
		if !strings.Contains(out, "16 keys, 11 folders") {
			t.Errorf("%s lost the sentence:\n%s", f, out)
		}
	}
}

// A page that states no numbers has no `numbers` in its output: nothing that
// parsed before reads differently.
func TestAPageWithNoNumbersHasNoNumbersKey(t *testing.T) {
	page := view.KeyValue{Pairs: []view.Pair{{Key: "k", Value: "v"}}}
	for _, f := range []Format{JSON, YAML} {
		if out := render(t, page, f); strings.Contains(out, "numbers") {
			t.Errorf("%s has a numbers key for a page with none:\n%s", f, out)
		}
	}
}
