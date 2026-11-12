package yamlguard

import (
	"reflect"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

type written struct {
	Note   string            `yaml:"note"`
	Notes  []string          `yaml:"notes"`
	ByName map[string]string `yaml:"by_name"`
	Inner  struct {
		Title string `yaml:"title"`
	} `yaml:"inner"`
}

// **A string rta writes reads back as itself.** `rta profile set staging --note
// "? why this exists"` wrote `note: ? why this exists`, which opens a complex
// key, and config.yaml stopped parsing; a tab in a plain scalar came back gone.
// Every string here goes through a struct field, a list and a map, where the
// config, the dashboard and the manifests keep theirs.
func TestAWrittenStringReadsBackAsItself(t *testing.T) {
	for _, s := range []string{
		"? why this exists", "?", "? ", "col\tcol", "line one\nline two", "trailing\n",
		"- not a list", ": not a key", "# not a comment", "yes", "null", "~", "0755", "1.0", "",
		"it's", `"quoted"`, "{flow}", "[flow]", "*alias", "&anchor", "!tag", "|", ">", "%", "@at", "`tick",
		"café · 日本語", "key: value", "<<",
	} {
		// A multi-line string beside it, at depth: in a document that needs
		// the quoting pass, that is the one the pass has to place right.
		in := written{Note: s, Notes: []string{s, "plain", "multi\nline"}, ByName: map[string]string{"k": s, "m": "multi\nline"}}
		in.Inner.Title = s
		out, err := Marshal(in)
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		var back written
		if err := yaml.Unmarshal(out, &back); err != nil {
			t.Errorf("%q does not read back: %v\n%s", s, err, out)
			continue
		}
		if !reflect.DeepEqual(back, in) {
			t.Errorf("%q came back as\n%#v\nfrom\n%s", s, back, out)
		}
	}
}

// What needs no quoting is written exactly as the encoder writes it, so a file
// rta rewrites does not change on every line for the sake of two strings.
func TestAPlainStringIsWrittenAsTheEncoderWritesIt(t *testing.T) {
	in := written{Note: "shared staging", Notes: []string{"a", "multi\nline"}, ByName: map[string]string{"k": "v"}}
	in.Inner.Title = "title"
	ours, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := yaml.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(ours) != string(theirs) {
		t.Errorf("a document with nothing to quote changed:\n%s\nwant\n%s", ours, theirs)
	}
	if !strings.Contains(string(ours), "note: shared staging") {
		t.Errorf("a plain value was quoted:\n%s", ours)
	}
}
