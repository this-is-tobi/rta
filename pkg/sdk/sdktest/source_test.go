package sdktest

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk/spelling"
	"github.com/this-is-tobi/rta/pkg/view"
)

func sourceDemo() plugin.Plugin {
	return plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{
		{ID: "demo.key.list", Summary: "list keys", Safety: plugin.Read,
			Inputs: []plugin.Field{{Name: "limit", Type: plugin.String}, {Name: "jobs", Type: plugin.String},
				{Name: "host", Type: plugin.String}},
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.Text{Body: "ok"}, nil
			}},
		{ID: "demo.key.get", Summary: "get a key", Safety: plugin.Read,
			Inputs: []plugin.Field{{Name: "key", Type: plugin.String, Positional: true}},
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.Text{Body: "ok"}, nil
			}},
	}}
}

// The two scans WithSource adds to the speller, held to what they exist to
// catch and what they must let through: a test that passes because its
// scanner sees nothing guards nothing. The speller's own rules are
// pkg/sdk/spelling's to hold, and are not repeated here.
func TestTheSourceScansCatchWhatTheyExistFor(t *testing.T) {
	demo := sourceDemo()

	src := "package main\n\nfunc hints(sf plugin.Surface, host, key string) []string {\n" +
		"\treturn []string{\n" +
		"\t\t\"a new database is worse. `createdb --host=\" + host + \" app` makes it\",\n" +
		"\t\t\"raise --limit to see more\",\n" +
		"\t\t\"remove it first: rta demo key rm \" + key + \" --host \" + host,\n" +
		"\t\tplugin.AskOperator(\"grant allow demo.key.list --limit 5\"),\n" +
		"\t\t\"--host=\" + host,\n" +
		"\t\t\"raise --\" + key + \" to see more\",\n" +
		"\t\tfmt.Sprintf(\"raise --%s to see more\", key),\n" +
		"\t\t\"`rta explain \" + key + \"` lists every input\",\n" +
		"\t\t\"`rta net dns \" + host + \"` shows what DNS returns\",\n" +
		"\t}\n}\n"
	f, err := parser.ParseFile(token.NewFileSet(), "hints.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range spelledForATerminal(spelling.ForPlugin(demo), f) {
		got = append(got, h.text)
	}
	// One of rta's own commands is let through, since a handler words it in
	// the CLI's branch; a built-in's command line in a span is not, though
	// the speller was given none of the built-ins.
	op := spelling.Operand
	want := []string{"raise --limit to see more", "remove it first: rta demo key rm " + op + " --host " + op,
		"raise --" + op + " to see more", "raise --%s to see more", "`rta net dns " + op + "` shows what DNS returns"}
	if !slices.Equal(got, want) {
		t.Errorf("held in source: %q, want %q", got, want)
	}

	calls := "package main\n\nfunc calls(sf plugin.Surface, k string) []string {\n" +
		"\treturn []string{\n" +
		"\t\tsf.Call(\"demo.key.get\", plugin.Arg{Name: \"key\", Value: k}),\n" +
		"\t\tsf.Call(\"demo.key.list\", plugin.Arg{Name: \"limit\", Value: 5, Positional: true}),\n" +
		"\t\tsf.CapabilityWith(\"demo.key.list\", \"depth\"),\n" +
		"\t\tsf.CapabilityWith(\"demo.key.get\", \"key\"),\n" +
		"\t\tsf.CapabilityName(\"demo.key.rm\"),\n" +
		"\t\tsf.Call(\"demo.key.get\", plugin.Arg{Name: \"key\", Value: k, Positional: true}),\n" +
		"\t\tsf.CapabilityWith(\"demo.key.list\", \"limit\", \"detail\"),\n" +
		"\t\tsf.Call(\"net.dns\", plugin.Arg{Name: \"name\", Value: k}),\n" +
		"\t\tsf.CapabilityWith(\"demo.key.list\", \"server\"),\n" +
		"\t\tsf.Call(\"demo.key.list\", plugin.Arg{Name: \"output\", Value: \"json\"}),\n" +
		"\t\tsf.Call(\"demo.key.list\", plugin.Arg{Name: \"profile\", Value: \"prod\"}),\n" +
		"\t}\n}\n"
	fset := token.NewFileSet()
	f, err = parser.ParseFile(fset, "calls.go", calls, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var wrong []int
	for _, c := range namedCalls(f) {
		if len(callProblems(demo, c)) > 0 {
			wrong = append(wrong, fset.Position(c.pos).Line)
		}
	}
	// --output and --profile are the host's, given to every capability's
	// command that takes them; --server is a switch of rta's own commands,
	// which no capability's is given, so a call handing one over names an
	// input the command refuses.
	if want := []int{5, 6, 7, 8, 9, 13}; !slices.Equal(wrong, want) {
		t.Errorf("calls held on lines %v, want %v", wrong, want)
	}
}

// AskOperator is known by the name its file imports pkg/plugin under: an
// author who renames the import hands the operator the same command, and it
// is let through the same way.
func TestAskOperatorIsKnownUnderTheNameItIsImportedAs(t *testing.T) {
	for _, imp := range []string{`sdk "github.com/this-is-tobi/rta/pkg/plugin"`, `. "github.com/this-is-tobi/rta/pkg/plugin"`} {
		call := "sdk.AskOperator"
		if strings.HasPrefix(imp, ".") {
			call = "AskOperator"
		}
		src := "package main\n\nimport " + imp + "\n\nvar hint = " + call + "(\"grant allow demo.key.list --limit 5\")\n"
		f, err := parser.ParseFile(token.NewFileSet(), "ask.go", src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		if held := spelledForATerminal(spelling.ForPlugin(sourceDemo()), f); len(held) > 0 {
			t.Errorf("with %s, the operator's command was held: %q", imp, held[0].text)
		}
	}
}

func writeSource(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// WithSource has the suite read the plugin's source beside its declaration,
// naming the file and line of each sentence and call it holds, and not its
// tests, whose literals are fixtures. Without the option the source is not
// read: rta runs the suite over its built-ins from the package that builds
// its command line, whose source is the CLI's own.
func TestWithSourceHoldsThePluginsOwnSource(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "hints.go", "package main\n\n"+
		"var hint = \"raise --limit to see more\"\n\n"+
		"func next(sf plugin.Surface) string { return sf.CapabilityName(\"demo.key.rm\") }\n")
	writeSource(t, dir, "hints_test.go", "package main\n\nvar fixture = \"raise --jobs to go faster\"\n")

	rec := &recorder{}
	cfg := noConfig()
	WithSource(dir)(&cfg)
	checkAll(rec, sourceDemo(), cfg, t.TempDir(), nil)
	got := rec.errText()
	for _, want := range []string{
		filepath.Join(dir, "hints.go") + ":3:12 spells what only a terminal can act on",
		filepath.Join(dir, "hints.go") + ":5:46: CapabilityName names demo.key.rm, which this plugin does not declare",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the suite did not report %q:\n%s", want, got)
		}
	}
	if len(rec.errs) != 2 {
		t.Errorf("want the two problems in hints.go and nothing from its test, got:\n%s", got)
	}

	rec = &recorder{}
	checkAll(rec, sourceDemo(), noConfig(), t.TempDir(), nil)
	if len(rec.errs) > 0 {
		t.Errorf("the source was read without WithSource:\n%s", rec.errText())
	}
}

// A directory with no source in it is refused rather than read as clean: a
// WithSource naming the wrong place would otherwise pass for good.
func TestWithSourceRefusesADirectoryWithNothingToRead(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "only_test.go", "package main\n")
	for _, where := range []string{dir, filepath.Join(dir, "absent")} {
		rec := &recorder{}
		checkSource(rec, sourceDemo(), where)
		if !strings.Contains(rec.errText(), "holds no Go source outside its tests") {
			t.Errorf("%s was read as clean source: %q", where, rec.errText())
		}
	}
	writeSource(t, dir, "broken.go", "package main\n\nfunc {\n")
	rec := &recorder{}
	checkSource(rec, sourceDemo(), dir)
	if !strings.Contains(rec.errText(), "reading the source in") {
		t.Errorf("a file that does not parse went unreported: %q", rec.errText())
	}
}

// Sentences hands a plugin's own test what WithSource reads: each literal
// and each sum of them as one sentence, where it starts, and nothing from
// its tests or from what AskOperator is given. A directory it cannot read a
// sentence from is an error rather than an empty answer, which a test would
// take for clean source.
func TestSentencesReadsThePluginsOwnSource(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "words.go", "package main\n\n"+
		"var server = \"MySQL enforces it\"\n\n"+
		"func hint(name string) string { return \"the \" + name + \" table\" }\n\n"+
		"var ask = plugin.AskOperator(\"doctor\")\n")
	writeSource(t, dir, "words_test.go", "package main\n\nvar fixture = \"a fixture\"\n")

	got, err := Sentences(dir)
	if err != nil {
		t.Fatal(err)
	}
	var read []string
	for _, s := range got {
		read = append(read, fmt.Sprintf("%s:%d %s", filepath.Base(s.Pos.Filename), s.Pos.Line, s.Text))
	}
	want := []string{"words.go:3 MySQL enforces it", "words.go:5 the " + spelling.Operand + " table"}
	if !slices.Equal(read, want) {
		t.Errorf("Sentences read %q, want %q", read, want)
	}

	empty := t.TempDir()
	writeSource(t, empty, "only_test.go", "package main\n")
	if _, err := Sentences(empty); err == nil || !strings.Contains(err.Error(), "holds no Go source") {
		t.Errorf("a directory with no source read as clean: %v", err)
	}
	writeSource(t, empty, "broken.go", "package main\n\nfunc {\n")
	if _, err := Sentences(empty); err == nil {
		t.Error("a file that does not parse read as clean")
	}
}
