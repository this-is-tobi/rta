package cli

import (
	"bytes"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/pkg/view"
)

// Alignment is measured in cells, not in bytes.
//
// Every one of these is the same mistake in a different renderer: len(s)
// counts bytes, fmt's %-*s pads by runes, and a terminal draws cells. The
// three agree only for ASCII, so a column stays straight until somebody's
// data has an accent in it.

// column returns the display column each line's marker starts at.
func columns(out, marker string) []int {
	var cols []int
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		i := strings.Index(line, marker)
		if i < 0 {
			continue
		}
		cols = append(cols, lipgloss.Width(line[:i]))
	}
	return cols
}

func allSame(t *testing.T, what string, cols []int, out string) {
	t.Helper()
	if len(cols) == 0 {
		t.Fatalf("%s: nothing matched, so nothing is under test:\n%s", what, out)
	}
	for _, c := range cols[1:] {
		if c != cols[0] {
			t.Errorf("%s start at columns %v — they do not line up:\n%s", what, cols, out)
			return
		}
	}
}

// A KeyValue's values form a column whatever the keys are made of.
func TestKeyValueValuesLineUp(t *testing.T) {
	v := view.KeyValue{Pairs: []view.Pair{
		{Key: "hostname", Value: "· one"},
		{Key: "café", Value: "· two"},
		{Key: "日本語", Value: "· three"},
		{Key: "plain", Value: "· four"},
	}}
	var buf bytes.Buffer
	if err := Render(&buf, v, Options{Format: Pretty, NoColor: true}); err != nil {
		t.Fatal(err)
	}
	allSame(t, "values", columns(buf.String(), "·"), buf.String())
}

// A bar chart's bars share a baseline, which is the only thing a bar chart is
// for.
func TestBarsShareABaseline(t *testing.T) {
	v := view.Chart{Kind: view.ChartBar, Unit: "%", Max: 100, Series: []view.Series{
		{Name: "café", Points: []float64{50}},
		{Name: "日本", Points: []float64{25}},
		{Name: "abcde", Points: []float64{75}},
	}}
	var buf bytes.Buffer
	if err := Render(&buf, v, Options{Format: Pretty, NoColor: true, Width: 60}); err != nil {
		t.Fatal(err)
	}
	allSame(t, "bars", columns(buf.String(), "█"), buf.String())
}

// A cell with no column is not drawn at all, in any format.
//
// view.Redact now truncates a row longer than Columns itself. Before that
// fix, pretty and markdown each dropped the extra cell on their own, while
// csv, json and yaml wrote it verbatim — so a table whose rows outran their
// headers leaked exactly the value redaction exists to hide through three
// of the five output formats, and only there. All five have to agree, and
// the only way that holds is deciding it once, in view.Redact, rather than
// separately in each renderer — which is what this checks across all of
// them rather than pretty alone.
func TestACellWithNoColumnIsNotDrawn(t *testing.T) {
	v := view.Table{
		Columns:  []view.Column{{Name: "Name"}, {Name: "Token"}},
		Redacted: []string{"Token"},
		Rows:     [][]string{{"one", "s3cret", "LEAKED-BY-BEING-EXTRA"}},
	}
	for _, format := range []Format{Pretty, Markdown, CSV, JSON, YAML} {
		t.Run(string(format), func(t *testing.T) {
			var buf bytes.Buffer
			if err := Render(&buf, v, Options{Format: format, NoColor: true}); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(buf.String(), "LEAKED-BY-BEING-EXTRA") {
				t.Errorf("a cell with no column reached the output:\n%s", buf.String())
			}
			if strings.Contains(buf.String(), "s3cret") {
				t.Errorf("the named column was not masked:\n%s", buf.String())
			}
		})
	}
}

// A right-aligned column's heading sits over its own digits.
func TestANumericHeadingIsRightAligned(t *testing.T) {
	v := view.Table{
		Columns: []view.Column{{Name: "Name"}, {Name: "Bytes", Kind: view.KindNumber}},
		Rows:    [][]string{{"one", "1"}, {"two", "1000000"}},
	}
	var buf bytes.Buffer
	if err := Render(&buf, v, Options{Format: Pretty, NoColor: true}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(buf.String(), "\n")
	var header, digits string
	for _, l := range lines {
		if strings.Contains(l, "BYTES") {
			header = l
		}
		if strings.Contains(l, "1000000") {
			digits = l
		}
	}
	if header == "" || digits == "" {
		t.Fatalf("could not find the rows under test:\n%s", buf.String())
	}
	// The last cell of each ends at the same column when both are right-aligned.
	endOf := func(line, want string) int {
		i := strings.Index(line, want)
		return lipgloss.Width(line[:i]) + lipgloss.Width(want)
	}
	if a, b := endOf(header, "BYTES"), endOf(digits, "1000000"); a != b {
		t.Errorf("heading ends at %d, digits end at %d — the heading floats left of its own column:\n%s",
			a, b, buf.String())
	}
}

// A markdown table's pipes line up, so the raw source reads in a pull request
// — which is the reason markdown.go pads it at all.
func TestMarkdownPipesLineUp(t *testing.T) {
	v := view.Table{
		Columns: []view.Column{{Name: "Name"}, {Name: "N"}},
		Rows:    [][]string{{"日本語テスト", "1"}, {"ab", "22222"}},
	}
	var buf bytes.Buffer
	if err := Render(&buf, v, Options{Format: Markdown}); err != nil {
		t.Fatal(err)
	}
	var widths []int
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if strings.HasPrefix(line, "|") {
			widths = append(widths, lipgloss.Width(line))
		}
	}
	if len(widths) < 3 {
		t.Fatalf("not a table:\n%s", buf.String())
	}
	for _, w := range widths[1:] {
		if w != widths[0] {
			t.Errorf("row widths %v — the source does not line up:\n%s", widths, buf.String())
			return
		}
	}
}

// A wrapped error message hangs under its own badge, with colour on.
//
// Both badges carry Padding(0, 1) when there is colour and nothing when there
// is not, so a hardcoded indent was right in a pipe and two cells short on a
// terminal — the half nobody diffs.
func TestAWrappedErrorHangsUnderItsBadge(t *testing.T) {
	e := view.Errorf("core.profile.unknown",
		"no profile named %q, and this message is long enough that it has to wrap somewhere", "nope").
		WithHint("a hint that is also long enough to need a second line of its own to sit on")
	var buf bytes.Buffer
	if err := RenderError(&buf, e, Options{Format: Pretty, Width: 60}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("nothing wrapped, so nothing is under test:\n%s", buf.String())
	}
	for i, line := range lines {
		plain := ansi.Strip(line)
		if strings.HasPrefix(plain, " ERROR") || strings.HasPrefix(plain, " HINT") {
			continue // a badge line
		}
		lead := lipgloss.Width(plain) - lipgloss.Width(strings.TrimLeft(plain, " "))
		want := 8 // " ERROR " plus the space after it
		if strings.Contains(plain, "hint") || i > 1 {
			// The HINT badge is one cell narrower.
			if lead != want && lead != want-1 {
				t.Errorf("continuation line %d hangs at %d cells, want %d or %d: %q",
					i, lead, want-1, want, plain)
			}
			continue
		}
		if lead != want {
			t.Errorf("continuation line %d hangs at %d cells, want %d: %q", i, lead, want, plain)
		}
	}
}
