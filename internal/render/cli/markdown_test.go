package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func md(t *testing.T, v view.View) string {
	t.Helper()
	return render(t, v, Markdown)
}

// A markdown table whose rows disagree with its header is not a table — the
// renderer silently reassigns values to the wrong columns. Counting the cell
// separators is the cheapest way to catch that, and it catches the escaping
// bugs too, since an unescaped pipe shows up here as an extra column.
func checkGrid(t *testing.T, out string) {
	t.Helper()
	var cols, seen int
	for i, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasPrefix(line, "|") {
			cols = 0 // a blank line or prose ends the current table
			continue
		}
		n := strings.Count(line, "|") - strings.Count(line, "\\|")
		if cols == 0 {
			cols = n
			seen++
			continue
		}
		if n != cols {
			t.Errorf("line %d has %d cell borders, the table's header had %d:\n%s",
				i+1, n, cols, out)
		}
	}
	if seen == 0 {
		t.Errorf("expected at least one table:\n%s", out)
	}
}

func TestParseFormatAcceptsMarkdown(t *testing.T) {
	for _, spelling := range []string{"md", "markdown"} {
		got, err := ParseFormat(spelling)
		if err != nil {
			t.Fatalf("ParseFormat(%q): %v", spelling, err)
		}
		if got != Markdown {
			t.Errorf("ParseFormat(%q) = %q, want %q", spelling, got, Markdown)
		}
	}
	if _, err := ParseFormat("mkd"); err == nil {
		t.Error("an unknown format should still be rejected")
	}
}

// Markdown is the one format that renders the whole union — csv handles a
// single type and says so, and that asymmetry is deliberate, but a report
// format that silently emitted nothing for half the catalogue would be worse
// than not having one.
func TestMarkdownRendersEveryViewType(t *testing.T) {
	cases := []struct {
		name string
		v    view.View
		want string
	}{
		{"text", view.Text{Body: "nothing to report"}, "nothing to report"},
		{"markdown text", view.Text{Body: "# Heading", Markdown: true}, "# Heading"},
		{"keyvalue", view.KeyValue{Pairs: []view.Pair{{Key: "host", Value: "poire"}}}, "| host"},
		{"table", sampleTable, "| alpha"},
		{"tree", view.Tree{Roots: []view.Node{{Label: "root", Detail: "d",
			Children: []view.Node{{Label: "leaf"}}}}}, "  - leaf"},
		{"bar chart", view.Chart{Kind: view.ChartBar, Unit: "%", Series: []view.Series{
			{Name: "cpu", Points: []float64{12}}}}, "| cpu"},
		{"line chart", view.Chart{Kind: view.ChartLine, Series: []view.Series{
			{Name: "rtt", Points: []float64{3, 9, 5}}}}, "| rtt"},
		{"sections", view.Sections{Items: []view.Section{
			{Title: "one", View: view.Text{Body: "body"}}}}, "## one"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := md(t, tc.v)
			if strings.TrimSpace(out) == "" {
				t.Fatal("rendered nothing")
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("missing %q:\n%s", tc.want, out)
			}
		})
	}
}

// A pipe inside a cell ends the cell early, shifting every value after it one
// column left — silently, and only for the rows that happen to contain one.
// Commands, CSP policies and shell snippets all carry pipes routinely.
func TestMarkdownEscapesPipesSoColumnsDoNotShift(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "check"}, {Name: "command"}, {Name: "status"}},
		Rows: [][]string{
			{"plain", "echo hi", "ok"},
			{"piped", "ps aux | grep rta", "ok"},
		},
	}
	out := md(t, tbl)
	checkGrid(t, out)
	if !strings.Contains(out, `\|`) {
		t.Errorf("the pipe was not escaped:\n%s", out)
	}
	if strings.Contains(out, "aux | grep") {
		t.Errorf("a raw pipe survived into a cell:\n%s", out)
	}
}

// inlineMarkdown is the one place every markdown-rendered cell goes through —
// table, tree, key/value, chart. Left unescaped, a link opens for real, a
// backtick opens a code span early, and a raw tag is read as HTML by any
// downstream renderer without its own sanitizer: a wiki, a static-site
// generator, an editor preview, a ticket tracker.
func TestMarkdownEscapesLinkCodeAndHTMLSyntaxInCells(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "note"}},
		Rows: [][]string{{
			"[Click to verify your account](https://evil.example/phish) and some `code` and <img src=x onerror=alert(1)>",
		}},
	}
	out := md(t, tbl)
	checkGrid(t, out)
	for _, raw := range []string{
		"[Click to verify your account](https://evil.example/phish)",
		"some `code` and",
		"and <img src=x onerror=alert(1)>",
	} {
		if strings.Contains(out, raw) {
			t.Errorf("unescaped markdown/HTML syntax survived into a cell (%q):\n%s", raw, out)
		}
	}
	for _, escaped := range []string{
		`\[Click to verify your account\]`,
		"\\`code\\`",
		"\\<img src=x onerror=alert(1)>",
	} {
		if !strings.Contains(out, escaped) {
			t.Errorf("missing escaped form %q:\n%s", escaped, out)
		}
	}
}

// A newline inside a cell breaks out of the row entirely, which turns the
// rest of the table into prose.
func TestMarkdownKeepsMultilineCellsInsideTheirRow(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "key"}, {Name: "detail"}},
		Rows:    [][]string{{"a", "first\nsecond"}, {"b", "windows\r\nline"}},
	}
	out := md(t, tbl)
	checkGrid(t, out)
	if !strings.Contains(out, "first<br>second") {
		t.Errorf("newline not folded into the row:\n%s", out)
	}
	if strings.Contains(out, "\r") {
		t.Errorf("a carriage return survived:\n%s", out)
	}
}

// The report format is the one most likely to be redirected to a file, mailed
// or pasted into a ticket, so it is the last place a secret should surface.
func TestMarkdownMasksRedactedValues(t *testing.T) {
	page := view.Sections{Items: []view.Section{
		{Title: "creds", View: view.KeyValue{
			Pairs:    []view.Pair{{Key: "token", Value: "hunter2"}},
			Redacted: []string{"token"},
		}},
		{Title: "table", View: view.Table{
			Columns:  []view.Column{{Name: "key"}, {Name: "value"}},
			Rows:     [][]string{{"api", "s3cret"}},
			Redacted: []string{"value"},
		}},
	}}
	out := md(t, page)
	for _, secret := range []string{"hunter2", "s3cret"} {
		if strings.Contains(out, secret) {
			t.Errorf("markdown leaked %q:\n%s", secret, out)
		}
	}
	if strings.Count(out, view.Mask) != 2 {
		t.Errorf("expected both values masked:\n%s", out)
	}
}

func TestMarkdownNestsSectionHeadings(t *testing.T) {
	page := view.Sections{Items: []view.Section{
		{Title: "outer", View: view.Sections{Items: []view.Section{
			{Title: "inner", View: view.Text{Body: "leaf"}},
		}}},
	}}
	out := md(t, page)
	if !strings.Contains(out, "## outer") {
		t.Errorf("outer heading wrong:\n%s", out)
	}
	if !strings.Contains(out, "### inner") {
		t.Errorf("inner heading did not go a level deeper:\n%s", out)
	}
}

// Past six, "#######" stops being a heading and renders as literal text, so
// a page nested deeply enough would emit hashes into the reader's document.
func TestMarkdownHeadingsStopAtSix(t *testing.T) {
	v := view.View(view.Text{Body: "leaf"})
	for i := 0; i < 8; i++ {
		v = view.Sections{Items: []view.Section{{Title: "level", View: v}}}
	}
	out := md(t, v)
	if strings.Contains(out, "####### ") {
		t.Errorf("emitted a seventh heading level:\n%s", out)
	}
	if !strings.Contains(out, "###### level") {
		t.Errorf("expected the deepest headings to flatten to six:\n%s", out)
	}
}

// Rows are [][]string with nothing enforcing their width. A report generated
// from a ragged one must still be a table, and must never panic.
func TestMarkdownToleratesRaggedRows(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "a"}, {Name: "b"}},
		Rows:    [][]string{{"only"}, {}, {"x", "y", "dropped"}},
	}
	out := md(t, tbl)
	checkGrid(t, out)
	if strings.Contains(out, "dropped") {
		t.Errorf("a cell with no column was rendered anyway:\n%s", out)
	}
}

// A page is assembled from capabilities that may each fail; plugin.Page drops
// the ones that did. An empty view must not leave a heading pointing at
// nothing, and an empty page must not produce a file of stray whitespace.
func TestMarkdownSkipsEmptyContent(t *testing.T) {
	out := md(t, view.Sections{Items: []view.Section{
		{Title: "missing"},
		{Title: "present", View: view.Text{Body: "here"}},
	}})
	if strings.Contains(out, "## missing") {
		t.Errorf("a section with no view kept its heading:\n%s", out)
	}
	if !strings.Contains(out, "## present") {
		t.Errorf("a section with a view lost its heading:\n%s", out)
	}
	if got := md(t, view.Sections{}); strings.TrimSpace(got) != "" {
		t.Errorf("an empty page rendered %q", got)
	}
	if got := md(t, view.Table{}); strings.TrimSpace(got) != "" {
		t.Errorf("an empty table rendered %q", got)
	}
}

// A paginated table read as the whole set is the sort of quiet wrongness a
// report must not carry: the reader has no terminal to page in.
func TestMarkdownDeclaresATruncatedTable(t *testing.T) {
	out := md(t, view.Table{
		Columns: []view.Column{{Name: "id"}},
		Rows:    [][]string{{"1"}, {"2"}},
		Total:   57,
	})
	if !strings.Contains(out, "2 of 57") {
		t.Errorf("truncation not declared:\n%s", out)
	}
	if strings.Contains(md(t, sampleTable), "of ") {
		t.Error("a complete table should not claim truncation")
	}
}

// The same quiet wrongness as a truncated table, one level up: a page whose
// failing sections were dropped reads as a finished report, and this is the
// format that gets mailed and pasted into a ticket, where the reader has no
// command to re-run and nothing to compare against.
func TestMarkdownDeclaresADegradedPage(t *testing.T) {
	page := view.Sections{
		Items: []view.Section{{Title: "identity", View: view.Text{Body: "poire"}}},
		Warnings: []view.Error{
			{Code: "page.section.failed", Message: "temperature: no such\nsensor", Hint: "linux only"},
		},
	}
	out := md(t, page)
	for _, want := range []string{"> **Warnings**", "`page.section.failed`", "(linux only)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// A newline inside the message would end the blockquote and drop every
	// warning after it into the body as prose.
	if !strings.Contains(out, "temperature: no such<br>sensor") {
		t.Errorf("a raw newline broke out of the blockquote:\n%s", out)
	}
	// Below the content it qualifies, so the report still opens on findings.
	if strings.Index(out, "## identity") > strings.Index(out, "Warnings") {
		t.Errorf("warnings rendered above the sections:\n%s", out)
	}
	// A complete page carries no such block.
	page.Warnings = nil
	if strings.Contains(md(t, page), "Warnings") {
		t.Errorf("a complete page claimed to be degraded:\n%s", md(t, page))
	}
}

// Redirect a failing command to a file and the file must say what went wrong,
// rather than being empty.
func TestMarkdownRendersErrors(t *testing.T) {
	var buf bytes.Buffer
	err := RenderError(&buf, &view.Error{
		Code: "net.dns.failed", Message: "no such host", Hint: "check the name",
	}, Options{Format: Markdown})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"net.dns.failed", "no such host", "> check the name"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// RenderError is a separate entry point from the rest of the page, and
// AsError puts a foreign error's own text into Message — so a hostile
// server's error string needs the same inlineMarkdown treatment
// markdownWarnings already gives a warning, or a newline in it ends the line
// unquoted and a pipe shifts whatever comes after it.
func TestMarkdownEscapesErrorMessageAndHint(t *testing.T) {
	var buf bytes.Buffer
	e := &view.Error{
		Code:    "http.upstream",
		Message: "upstream said 500 | retry\n# Injected Heading",
		Hint:    "second attempt\nstill failing",
	}
	if err := RenderError(&buf, e, Options{Format: Markdown}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `\|`) {
		t.Errorf("the pipe in the message was not escaped:\n%s", out)
	}
	if strings.Contains(out, "500 | retry") {
		t.Errorf("a raw pipe survived in the message:\n%s", out)
	}
	if strings.Contains(out, "\n# Injected Heading") {
		t.Errorf("a newline in the message broke out into a real heading:\n%s", out)
	}
	if !strings.Contains(out, "retry<br># Injected Heading") {
		t.Errorf("the message's newline was not folded:\n%s", out)
	}
	if strings.Contains(out, "attempt\nstill failing") {
		t.Errorf("a newline in the hint broke out of the blockquote:\n%s", out)
	}
	if !strings.Contains(out, "attempt<br>still failing") {
		t.Errorf("the hint's newline was not folded:\n%s", out)
	}
}

// Markdown is a document format, not a terminal one: it must not pick up the
// styling or the width shaping that pretty output applies.
func TestMarkdownIgnoresTerminalShaping(t *testing.T) {
	var narrow, wide bytes.Buffer
	for _, c := range []struct {
		buf   *bytes.Buffer
		width int
	}{{&narrow, 20}, {&wide, 200}} {
		if err := Render(c.buf, sampleTable, Options{Format: Markdown, Width: c.width, Fill: true}); err != nil {
			t.Fatal(err)
		}
	}
	if narrow.String() != wide.String() {
		t.Errorf("width changed markdown output:\n--- 20 ---\n%s\n--- 200 ---\n%s",
			narrow.String(), wide.String())
	}
	if strings.Contains(narrow.String(), "\x1b[") {
		t.Errorf("markdown carried ANSI escapes:\n%q", narrow.String())
	}
}
