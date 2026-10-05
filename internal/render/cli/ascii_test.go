package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// A session over an old ssh, a CI log or a legacy console under LC_ALL=C drew
// box-drawing characters as mojibake, with no way to say "plain". The locale
// decides, and the terminal types that cannot draw a rounded corner at all.
func TestASCIIOnlyFollowsTheLocaleAndTheTerminal(t *testing.T) {
	for _, c := range []struct {
		name string
		vars map[string]string
		want bool
	}{
		{"nothing set says nothing", map[string]string{}, false},
		{"a UTF-8 locale", map[string]string{"LANG": "en_US.UTF-8"}, false},
		{"the spelling without the dash", map[string]string{"LANG": "en_US.utf8"}, false},
		{"C.UTF-8, which containers set", map[string]string{"LANG": "C.UTF-8"}, false},
		{"a modifier after the charset", map[string]string{"LANG": "de_DE.UTF-8@euro"}, false},
		{"macOS Terminal's bare charset", map[string]string{"LC_CTYPE": "UTF-8"}, false},
		{"the C locale", map[string]string{"LANG": "C"}, true},
		{"POSIX", map[string]string{"LC_ALL": "POSIX"}, true},
		{"Latin-1", map[string]string{"LANG": "en_US.ISO-8859-1"}, true},
		{"a locale that names no charset", map[string]string{"LANG": "en_US"}, true},
		{"LC_ALL beats LANG", map[string]string{"LC_ALL": "C", "LANG": "en_US.UTF-8"}, true},
		{"LC_ALL beats LANG the other way", map[string]string{"LC_ALL": "en_US.UTF-8", "LANG": "C"}, false},
		{"LC_CTYPE beats LANG", map[string]string{"LC_CTYPE": "C", "LANG": "en_US.UTF-8"}, true},
		{"an empty LC_ALL is unset", map[string]string{"LC_ALL": "", "LANG": "C"}, true},
		{"the Linux console", map[string]string{"TERM": "linux", "LANG": "en_US.UTF-8"}, true},
		{"a terminal that cannot move the cursor", map[string]string{"TERM": "dumb"}, true},
		{"an ordinary terminal", map[string]string{"TERM": "xterm-256color", "LANG": "en_US.UTF-8"}, false},
	} {
		if got := ASCIIOnly(env(c.vars)); got != c.want {
			t.Errorf("%s: ASCIIOnly = %v, want %v", c.name, got, c.want)
		}
	}
}

func nonASCII(s string) string {
	for _, r := range s {
		if r > 127 {
			return string(r)
		}
	}
	return ""
}

func renderASCII(t *testing.T, v view.View, width int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, v, Options{Format: Pretty, NoColor: true, ASCII: true, Width: width}); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// What the renderer draws with itself — borders, rules, bars, tree branches,
// markers and the separators it puts between parts — is ASCII when asked, for
// every shape a view takes. The text it is handed is the capability's and is
// never rewritten.
func TestASCIIDrawsEveryShapeInASCII(t *testing.T) {
	table := view.Table{
		Columns: []view.Column{{Name: "Name"}, {Name: "Size", Kind: view.KindBytes}},
		Rows:    [][]string{{"alpha", "10 MB"}, {"beta", "2 GB"}},
		Total:   40,
		Page:    &view.Cursor{Next: "next-page"},
	}
	views := map[string]view.View{
		"a table, natural width": table,
		"a table, constrained":   table,
		"key and value":          view.KeyValue{Pairs: []view.Pair{{Key: "host", Value: "poire"}}},
		"a tree": view.Tree{Roots: []view.Node{{Label: "public", Children: []view.Node{
			{Label: "users", Detail: "12 rows", Children: []view.Node{{Label: "id"}}}, {Label: "orders"},
		}}}},
		"a line chart": view.Chart{Kind: view.ChartLine, Unit: "%", Max: 100,
			Series: []view.Series{{Name: "cpu", Points: []float64{10, 40, 30, 80, 60}}, {Name: "mem", Points: []float64{50, 52, 51, 55, 54}}}},
		"a bar chart": view.Chart{Kind: view.ChartBar, Unit: "%", Max: 100,
			Series: []view.Series{{Name: "core0", Points: []float64{50}}, {Name: "core1", Points: []float64{100}}}},
		"sections": view.Sections{
			Items:    []view.Section{{Title: "identity", View: view.KeyValue{Pairs: []view.Pair{{Key: "host", Value: "poire"}}}}},
			Warnings: []view.Error{*view.Errorf("x.partial", "one section could not be read").WithHint("try again")},
		},
	}
	for name, v := range views {
		for _, width := range []int{0, 60} {
			if out := renderASCII(t, v, width); nonASCII(out) != "" {
				t.Errorf("%s at width %d drew %q:\n%s", name, width, nonASCII(out), out)
			}
		}
	}

	var buf bytes.Buffer
	if err := RenderError(&buf, view.Errorf("pg.conn.refused", "connection refused").WithHint("run rta doctor"),
		Options{Format: Pretty, NoColor: true, ASCII: true}); err != nil {
		t.Fatal(err)
	}
	if nonASCII(buf.String()) != "" {
		t.Errorf("an error drew %q:\n%s", nonASCII(buf.String()), buf.String())
	}
}

func TestASCIITablesAreDrawnWithPlusDashAndPipe(t *testing.T) {
	out := renderASCII(t, sampleTable, 0)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if !strings.HasPrefix(lines[0], "+-") || !strings.HasSuffix(lines[0], "-+") || !strings.HasPrefix(lines[1], "| NAME") {
		t.Errorf("not an ASCII grid:\n%s", out)
	}
	for _, line := range lines {
		if len(line) != len(lines[0]) {
			t.Errorf("a row is not as wide as the top rule (%d, want %d): %q", len(line), len(lines[0]), line)
		}
	}
}

// A grid that did not fit is told from one that did by its right edge; the
// ASCII border has a different edge, and a check written for the rounded one
// found every ASCII grid cut off and redrew it as records.
func TestAnASCIITableThatFitsIsStillAGrid(t *testing.T) {
	out := renderASCII(t, sampleTable, 80)
	if !strings.HasPrefix(out, "+-") {
		t.Errorf("a grid that fits was redrawn:\n%s", out)
	}
}

func TestASCIIBarsAndBranchesAndMarkers(t *testing.T) {
	tree := renderASCII(t, view.Tree{Roots: []view.Node{{Label: "public", Children: []view.Node{
		{Label: "users", Children: []view.Node{{Label: "id"}}}, {Label: "orders"},
	}}}}, 0)
	for _, want := range []string{"|-- users", "|   `-- id", "`-- orders"} {
		if !strings.Contains(tree, want) {
			t.Errorf("tree missing %q:\n%s", want, tree)
		}
	}

	bars := renderASCII(t, view.Chart{Kind: view.ChartBar, Max: 100,
		Series: []view.Series{{Name: "a", Points: []float64{50}}, {Name: "b", Points: []float64{100}}}}, 0)
	lines := strings.Split(strings.TrimSpace(bars), "\n")
	if strings.Count(lines[1], "#") != strings.Count(lines[0], "#")*2 || !strings.Contains(lines[0], ".") {
		t.Errorf("ASCII bars wrong:\n%s", bars)
	}
}

// The default is the box-drawing it always was, so nothing changes for a
// terminal that can draw it.
func TestWithoutTheOptionTheDrawingIsUnchanged(t *testing.T) {
	out := render(t, sampleTable, Pretty)
	if !strings.HasPrefix(out, "╭") {
		t.Errorf("the default grid changed:\n%s", out)
	}
}
