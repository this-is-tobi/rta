package cli

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

var sampleTable = view.Table{
	Columns: []view.Column{
		{Name: "Name"},
		{Name: "Size", Kind: view.KindBytes},
	},
	Rows: [][]string{
		{"alpha", "10 MB"},
		{"beta", "2 GB"},
	},
	Total: 2,
}

func render(t *testing.T, v view.View, f Format) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, v, Options{Format: f, NoColor: true}); err != nil {
		t.Fatalf("render %s: %v", f, err)
	}
	return buf.String()
}

func TestParseFormat(t *testing.T) {
	for _, ok := range []string{"", "pretty", "json", "yaml", "csv"} {
		if _, err := ParseFormat(ok); err != nil {
			t.Errorf("ParseFormat(%q) unexpected error: %v", ok, err)
		}
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Error("ParseFormat(xml) should fail")
	}
}

func TestJSONEnvelope(t *testing.T) {
	out := render(t, sampleTable, JSON)
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out)
	}
	if m["type"] != "table" {
		t.Errorf("type = %v", m["type"])
	}
}

func TestYAML(t *testing.T) {
	out := render(t, view.Text{Body: "hello"}, YAML)
	if !strings.Contains(out, "type: text") || !strings.Contains(out, "body: hello") {
		t.Errorf("yaml output missing fields:\n%s", out)
	}
}

func TestCSV(t *testing.T) {
	out := render(t, sampleTable, CSV)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || lines[0] != "Name,Size" || lines[1] != "alpha,10 MB" {
		t.Errorf("csv output wrong:\n%s", out)
	}
	// CSV rejects non-tables.
	var buf bytes.Buffer
	if err := Render(&buf, view.Text{Body: "x"}, Options{Format: CSV}); err == nil {
		t.Error("csv of non-table should fail")
	}
}

// csv is the format whose only reason to exist is being fed to another
// program, and it was the one renderer that dropped Table.Total: three rows
// of a 744-row result came out byte-identical to a complete three-row
// answer, so nothing downstream could tell a page from the whole set.
func TestCSVReportsTruncationOnTheNotesChannel(t *testing.T) {
	page := view.Table{
		Columns: []view.Column{{Name: "pid"}},
		Rows:    [][]string{{"1"}, {"2"}, {"3"}},
		Total:   744,
	}
	var out, notes bytes.Buffer
	if err := Render(&out, page, Options{Format: CSV, Notes: &notes}); err != nil {
		t.Fatal(err)
	}
	if got := notes.String(); got != "# 3 of 744 rows\n" {
		t.Errorf("notes = %q, want the truncation reported", got)
	}
	// And the body stays pure csv: a consumer that has never heard of the
	// note still parses every byte it is handed as data.
	if strings.Contains(out.String(), "#") {
		t.Errorf("the note leaked into the csv body:\n%s", out.String())
	}
	rows, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
	if err != nil {
		t.Fatalf("body is not csv: %v\n%s", err, out.String())
	}
	if len(rows) != 4 {
		t.Errorf("csv rows = %d, want the header and three rows: %v", len(rows), rows)
	}

	// A complete table says nothing. A note printed on every run is a note
	// nobody reads on the run that matters.
	out.Reset()
	notes.Reset()
	if err := Render(&out, sampleTable, Options{Format: CSV, Notes: &notes}); err != nil {
		t.Fatal(err)
	}
	if notes.Len() != 0 {
		t.Errorf("a complete table wrote a note: %q", notes.String())
	}

	// nil Notes is the documented default, and a host with nowhere to put
	// them must not pay for that with a panic.
	if err := Render(io.Discard, page, Options{Format: CSV}); err != nil {
		t.Fatalf("csv with no notes channel: %v", err)
	}
}

// -o csv says it feeds a spreadsheet, and Excel, LibreOffice Calc and Google
// Sheets all read a cell opening with =, +, - or @ as a formula to evaluate
// rather than text to display (CWE-1236). encoding/csv quotes a field for a
// comma, a quote or a newline — never for a leading character like this —
// so nothing between a capability's own data and the file on disk was
// neutralizing it.
func TestCSVNeutralizesFormulaTriggerCells(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "=cmd"}, {Name: "note"}},
		Rows: [][]string{
			{"=cmd|' /C calc'!A0", "ok"},
			{"+1+1", "ok"},
			{"-1+1", "ok"},
			{"@SUM(A1:A2)", "ok"},
			{"alpha", "hello, world"},
		},
	}
	out := render(t, tbl, CSV)
	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("body is not csv: %v\n%s", err, out)
	}
	want := [][]string{
		{"'=cmd", "note"},
		{"'=cmd|' /C calc'!A0", "ok"},
		{"'+1+1", "ok"},
		{"'-1+1", "ok"},
		{"'@SUM(A1:A2)", "ok"},
		// An ordinary cell, including one whose standard CSV quoting (the
		// comma) has nothing to do with formula triggers, must round-trip
		// untouched.
		{"alpha", "hello, world"},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d:\n%v", len(rows), len(want), rows)
	}
	for i := range want {
		for j := range want[i] {
			if rows[i][j] != want[i][j] {
				t.Errorf("row %d col %d = %q, want %q", i, j, rows[i][j], want[i][j])
			}
		}
	}
}

func TestPrettyTable(t *testing.T) {
	out := render(t, sampleTable, Pretty)
	for _, want := range []string{"NAME", "SIZE", "alpha", "2 GB"} {
		if !strings.Contains(out, want) {
			t.Errorf("pretty table missing %q:\n%s", want, out)
		}
	}
}

func TestPrettyKeyValueRedaction(t *testing.T) {
	kv := view.KeyValue{
		Pairs:    []view.Pair{{Key: "user", Value: "tobi"}, {Key: "password", Value: "hunter2"}},
		Redacted: []string{"password"},
	}
	for _, f := range []Format{Pretty, JSON, YAML} {
		out := render(t, kv, f)
		if strings.Contains(out, "hunter2") {
			t.Errorf("%s output leaks secret:\n%s", f, out)
		}
		if !strings.Contains(out, view.Mask) {
			t.Errorf("%s output missing mask:\n%s", f, out)
		}
	}
}

// Every format that turns a view into bytes a caller can read has to mask,
// and csv is the one that could not: it asserted straight to view.Table and
// wrote the rows, so the redaction promise held in three formats out of four.
// It was harmless only because no view could declare a secret in a table yet.
func TestEveryFormatMasksARedactedTable(t *testing.T) {
	tbl := view.Table{
		Columns:  []view.Column{{Name: "key"}, {Name: "value"}},
		Rows:     [][]string{{"api-token", "hunter2"}},
		Redacted: []string{"value"},
	}
	for _, f := range []Format{Pretty, JSON, YAML, CSV} {
		out := render(t, tbl, f)
		if strings.Contains(out, "hunter2") {
			t.Errorf("%s output leaks the secret:\n%s", f, out)
		}
		if !strings.Contains(out, view.Mask) {
			t.Errorf("%s output is missing the mask:\n%s", f, out)
		}
		if !strings.Contains(out, "api-token") {
			t.Errorf("%s output dropped the non-secret column:\n%s", f, out)
		}
	}
}

func TestPrettyTree(t *testing.T) {
	tree := view.Tree{Roots: []view.Node{{
		Label: "public",
		Children: []view.Node{
			{Label: "users", Detail: "12 rows"},
			{Label: "orders"},
		},
	}}}
	out := render(t, tree, Pretty)
	for _, want := range []string{"public", "├── users", "12 rows", "└── orders"} {
		if !strings.Contains(out, want) {
			t.Errorf("tree missing %q:\n%s", want, out)
		}
	}
}

func TestPrettyChartBar(t *testing.T) {
	chart := view.Chart{
		Kind: view.ChartBar,
		Unit: "%",
		Max:  100,
		Series: []view.Series{
			{Name: "core0", Points: []float64{50}},
			{Name: "core1", Points: []float64{100}},
		},
	}
	out := render(t, chart, Pretty)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("bar lines = %d:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "core0") || !strings.Contains(lines[0], "50.0%") {
		t.Errorf("bar row missing label/value: %q", lines[0])
	}
	// 100% fills the whole bar; 50% fills half.
	if strings.Count(lines[1], "█") != strings.Count(lines[0], "█")*2 {
		t.Errorf("bar scaling wrong:\n%s", out)
	}
}

func TestPrettyChartLine(t *testing.T) {
	chart := view.Chart{
		Kind:   view.ChartLine,
		Unit:   "ms",
		Series: []view.Series{{Name: "rtt", Points: []float64{1, 5, 3, 8, 2}}},
	}
	out := render(t, chart, Pretty)
	if !strings.Contains(out, "rtt (ms)") {
		t.Errorf("caption missing:\n%s", out)
	}
	if !strings.Contains(out, "┤") {
		t.Errorf("no plot axis drawn:\n%s", out)
	}
}

func TestChartEnvelope(t *testing.T) {
	chart := view.Chart{Kind: view.ChartBar, Series: []view.Series{{Name: "x", Points: []float64{1}}}}
	out := render(t, chart, JSON)
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "chart" || m["kind"] != "bar" {
		t.Errorf("chart envelope wrong: %v", m)
	}
}

func TestEmptyChart(t *testing.T) {
	out := render(t, view.Chart{Kind: view.ChartLine}, Pretty)
	if !strings.Contains(out, "no data") {
		t.Errorf("empty chart should say so: %q", out)
	}
}

func TestRenderError(t *testing.T) {
	e := view.Errorf("pg.conn.refused", "connection refused").WithHint("run rta doctor")
	var buf bytes.Buffer
	if err := RenderError(&buf, e, Options{NoColor: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"ERROR", "pg.conn.refused", "connection refused", "HINT", "run rta doctor"} {
		if !strings.Contains(out, want) {
			t.Errorf("error output missing %q:\n%s", want, out)
		}
	}
	// JSON form is structured.
	buf.Reset()
	if err := RenderError(&buf, e, Options{Format: JSON}); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("error json invalid: %v", err)
	}
	if m["type"] != "error" || m["code"] != "pg.conn.refused" {
		t.Errorf("error json wrong: %v", m)
	}
}

// --output has to keep applying when something goes wrong, which is exactly
// when a script needs a code it can branch on. RenderError special-cased json
// alone, so `-o json` gave a structured envelope and `-o yaml` gave a styled
// "ERROR pg.conn.refused" block no parser accepts: the CLI's two
// machine-readable formats disagreed about whether a failure was machine
// readable at all.
func TestErrorsHonourEveryMachineReadableFormat(t *testing.T) {
	e := view.Errorf("pg.conn.refused", "connection refused").WithHint("run rta doctor")

	decode := map[Format]func([]byte, any) error{
		JSON: json.Unmarshal,
		YAML: yaml.Unmarshal,
	}
	for _, f := range []Format{JSON, YAML} {
		var buf bytes.Buffer
		if err := RenderError(&buf, e, Options{Format: f}); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var m map[string]any
		if err := decode[f](buf.Bytes(), &m); err != nil {
			t.Fatalf("%s error output does not parse as %s: %v\n%s", f, f, err, buf.String())
		}
		if m["type"] != "error" || m["code"] != "pg.conn.refused" {
			t.Errorf("%s error envelope wrong: %v", f, m)
		}
		if m["message"] != "connection refused" || m["hint"] != "run rta doctor" {
			t.Errorf("%s dropped the message or the hint: %v", f, m)
		}
	}
}

// csv is the deliberate exception and this pins it, so that the next reader
// finds a decision rather than an oversight. An error is one coded fact and a
// hint; a one-row code,message,hint table is a shape no capability produces,
// so a consumer piping csv into a column-indexed reader would get something
// that parses and means nothing.
func TestCSVErrorsFallBackToProseOnPurpose(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderError(&buf, view.Errorf("kv.locked", "store is locked"), Options{Format: CSV, NoColor: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "ERROR") || !strings.Contains(out, "store is locked") {
		t.Errorf("csv errors should render the human block:\n%s", out)
	}
	if strings.Contains(out, "kv.locked,") {
		t.Errorf("csv grew an error table shape:\n%s", out)
	}
}

// TestMarkdownTextIsRendered: on styled output a markdown Text view goes
// through glamour (heading markers disappear); without color the notty style
// keeps markdown source — honest, grep-able pipe output. Plain Text stays
// verbatim everywhere, and structured formats always carry the raw body.
func TestMarkdownTextIsRendered(t *testing.T) {
	md := view.Text{Body: "# Title\n\nsome *emphasis* here", Markdown: true}
	var buf bytes.Buffer
	if err := Render(&buf, md, Options{Format: Pretty}); err != nil { // color on
		t.Fatal(err)
	}
	styled := buf.String()
	if strings.Contains(styled, "# Title") {
		t.Errorf("markdown not rendered, raw heading survives: %q", styled)
	}
	if !strings.Contains(styled, "Title") || !strings.Contains(styled, "emphasis") {
		t.Errorf("rendered markdown lost content: %q", styled)
	}

	piped := render(t, md, Pretty) // NoColor: markdown source preserved
	if !strings.Contains(piped, "# Title") {
		t.Errorf("no-color markdown must keep the source: %q", piped)
	}

	plain := render(t, view.Text{Body: "# Title"}, Pretty)
	if !strings.Contains(plain, "# Title") {
		t.Errorf("plain text must stay verbatim: %q", plain)
	}

	// Structured formats always carry the raw body: agents get the source.
	raw := render(t, md, JSON)
	if !strings.Contains(raw, "# Title") {
		t.Errorf("json body must keep raw markdown: %q", raw)
	}
}

// TestMarkdownWrapsToWidth: word-wrap follows Options.Width so TUI panes and
// tiles reflow like every other view.
func TestMarkdownWrapsToWidth(t *testing.T) {
	long := view.Text{Body: strings.Repeat("word ", 40), Markdown: true}
	var buf bytes.Buffer
	if err := Render(&buf, long, Options{Format: Pretty, NoColor: true, Width: 40}); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		if len(line) > 45 { // small glamour margin tolerance
			t.Fatalf("line exceeds width: %q", line)
		}
	}
}

// TestPrettySectionsComposes: a composite renders each child through the same
// pipeline, under its own heading — the component model end to end.
func TestPrettySectionsComposes(t *testing.T) {
	v := view.Sections{Items: []view.Section{
		{Title: "identity", View: view.KeyValue{Pairs: []view.Pair{{Key: "host", Value: "poire"}}}},
		{Title: "storage", View: sampleTable},
	}}
	out := render(t, v, Pretty)
	for _, want := range []string{"IDENTITY", "host", "poire", "STORAGE", "alpha", "10 MB"} {
		if !strings.Contains(out, want) {
			t.Errorf("sections output missing %q:\n%s", want, out)
		}
	}
	// Headings order follows the composition.
	if strings.Index(out, "IDENTITY") > strings.Index(out, "STORAGE") {
		t.Error("sections rendered out of order")
	}
}

// A section whose handler failed is dropped from the page, which is the
// right default — one dead sensor should not cost the reader the six that
// answered — but it used to leave nothing behind at all: six sections where
// there should be seven looked exactly like a page that only ever had six,
// to a person and to a monitoring check alike.
func TestPrettySectionsShowsWhatThePageCouldNotProduce(t *testing.T) {
	page := view.Sections{
		Items: []view.Section{
			{Title: "identity", View: view.KeyValue{Pairs: []view.Pair{{Key: "host", Value: "poire"}}}},
		},
		Warnings: []view.Error{
			{Code: "page.section.failed", Message: "temperature: no such sensor", Hint: "linux only"},
		},
	}
	out := render(t, page, Pretty)
	for _, want := range []string{"page.section.failed", "temperature: no such sensor", "linux only"} {
		if !strings.Contains(out, want) {
			t.Errorf("the degraded page does not say so, missing %q:\n%s", want, out)
		}
	}
	// Under the sections, not among them: a warning is not a seventh section.
	if strings.Index(out, "IDENTITY") > strings.Index(out, "page.section.failed") {
		t.Errorf("warnings rendered above the content:\n%s", out)
	}
	// A complete page stays exactly as quiet as it was.
	page.Warnings = nil
	if quiet := render(t, page, Pretty); strings.Contains(quiet, "!") {
		t.Errorf("a complete page grew a warning marker:\n%s", quiet)
	}
}

// Section.ID is the handle a script addresses a section by; Title is what a
// reader sees, and is free to be reworded. A renderer that printed Key would
// show "cpu_temp" where the page says "Temperature" the moment a producer
// started declaring ids.
func TestRenderersShowTheSectionTitleNotItsID(t *testing.T) {
	page := view.Sections{Items: []view.Section{
		{ID: "cpu_temp", Title: "Temperature", View: view.Text{Body: "48C"}},
	}}
	for _, f := range []Format{Pretty, Markdown} {
		out := render(t, page, f)
		if !strings.Contains(strings.ToLower(out), "temperature") {
			t.Errorf("%s dropped the title:\n%s", f, out)
		}
		if strings.Contains(strings.ToLower(out), "cpu_temp") {
			t.Errorf("%s showed the section id to the reader:\n%s", f, out)
		}
	}
}

func TestSectionsJSONRoundTrip(t *testing.T) {
	v := view.Sections{Items: []view.Section{
		{Title: "one", View: view.Text{Body: "hello"}},
	}}
	out := render(t, v, JSON)
	if !strings.Contains(out, `"sections"`) || !strings.Contains(out, `"text"`) || !strings.Contains(out, "hello") {
		t.Errorf("sections json wrong:\n%s", out)
	}
}

// --- Filling ------------------------------------------------------------
//
// A bordered pane is drawn at its own size whatever the content does, so a
// table that stops halfway across it is a gap, not restraint.

func renderWidth(t *testing.T, v view.View, opts Options) (string, int) {
	t.Helper()
	var buf bytes.Buffer
	opts.Format = Pretty
	opts.NoColor = true
	if err := Render(&buf, v, opts); err != nil {
		t.Fatal(err)
	}
	out := strings.TrimRight(buf.String(), "\n")
	widest := 0
	for _, line := range strings.Split(out, "\n") {
		widest = max(widest, lipgloss.Width(line))
	}
	return out, widest
}

var taskTable = view.Table{
	Columns: []view.Column{
		{Name: "ID", Kind: view.KindNumber},
		{Name: "Age", Kind: view.KindDuration},
		{Name: "Task"},
	},
	Rows: [][]string{{"3", "16h", "add users"}},
}

func TestFillUsesTheWholeWidth(t *testing.T) {
	_, got := renderWidth(t, taskTable, Options{Width: 80, Fill: true})
	if got != 80 {
		t.Errorf("table width = %d, want the 80 it was given", got)
	}
}

// columnWidths reads the rendered column widths off the top border, which is
// the only measurement that cannot disagree with what is on screen.
func columnWidths(out string) []int {
	top := strings.Split(out, "\n")[0]
	top = strings.TrimPrefix(strings.TrimSuffix(top, "╮"), "╭")
	var widths []int
	for _, seg := range strings.Split(top, "┬") {
		widths = append(widths, lipgloss.Width(seg))
	}
	return widths
}

// …and the space goes to the prose, not to the ID column. lipgloss's own
// expansion widens the *narrowest* column first, which is the wrong answer
// given with great precision.
func TestFillWidensTheTextColumn(t *testing.T) {
	out, _ := renderWidth(t, taskTable, Options{Width: 80, Fill: true})
	w := columnWidths(out)
	if len(w) != 3 {
		t.Fatalf("columns = %v", w)
	}
	if w[0] > 4 || w[1] > 5 {
		t.Errorf("the ID and Age columns grew (%v):\n%s", w, out)
	}
	if w[2] < 60 {
		t.Errorf("the task column got %d cells of the 80:\n%s", w[2], out)
	}
}

// Without Fill nothing changes: a command writing to a pipe or a scrollback
// wants its natural width.
func TestWidthAloneIsStillACeiling(t *testing.T) {
	_, natural := renderWidth(t, taskTable, Options{})
	_, got := renderWidth(t, taskTable, Options{Width: 80})
	if got != natural {
		t.Errorf("width without fill changed the table: %d, natural %d", got, natural)
	}
}

// A table of numbers gains nothing from being stretched: pulling columns
// apart makes a row harder to follow, not easier.
func TestFillLeavesATableWithNoProseAlone(t *testing.T) {
	numbers := view.Table{
		Columns: []view.Column{{Name: "ID", Kind: view.KindNumber}, {Name: "Age", Kind: view.KindDuration}},
		Rows:    [][]string{{"3", "16h"}},
	}
	_, natural := renderWidth(t, numbers, Options{})
	_, got := renderWidth(t, numbers, Options{Width: 80, Fill: true})
	if got != natural {
		t.Errorf("numeric table stretched to %d, natural %d", got, natural)
	}
}

// Two prose columns share what is going spare.
func TestFillSharesTheSlackBetweenTextColumns(t *testing.T) {
	two := view.Table{
		Columns: []view.Column{{Name: "ID", Kind: view.KindNumber}, {Name: "Task"}, {Name: "Note"}},
		Rows:    [][]string{{"3", "add users", "later"}},
	}
	out, got := renderWidth(t, two, Options{Width: 80, Fill: true})
	if got != 80 {
		t.Fatalf("table width = %d, want 80", got)
	}
	// Equal shares of the slack, so each keeps the head start its own content
	// gave it: "add users" is four cells longer than "later" and stays so.
	w := columnWidths(out)
	if diff := w[1] - w[2]; diff != 4 {
		t.Errorf("prose columns differ by %d, want the 4 their content differs by (%v):\n%s", diff, w, out)
	}
}

// Too wide is still too wide: filling must not disable shrinking.
func TestFillStillShrinksAnOverwideTable(t *testing.T) {
	long := view.Table{
		Columns: []view.Column{{Name: "Task"}, {Name: "Detail"}},
		Rows:    [][]string{{strings.Repeat("a", 60), strings.Repeat("b", 60)}},
	}
	_, got := renderWidth(t, long, Options{Width: 40, Fill: true})
	if got > 40 {
		t.Errorf("table width = %d, want it shrunk to 40", got)
	}
}

// A hyphen is not a place to break a line.
//
// x/ansi treats "-" as a breakpoint always, which is right for prose and wrong
// for everything this renderer shows: `rta explain s3.object.get` wrapped its
// own command line as "[--" / "endpoint <string>]", which is a line nobody can
// copy and a flag that reads as two things. Every hyphen in this tool is
// inside an identifier somebody may be about to paste.
func TestAFlagIsNeverBrokenInHalf(t *testing.T) {
	v := view.KeyValue{Pairs: []view.Pair{{
		Key:   "cli",
		Value: "rta s3 object get <bucket> <key> [--out <path>] [--endpoint <string>] [--access-key <string>]",
	}}}
	var buf bytes.Buffer
	if err := Render(&buf, v, Options{Format: Pretty, NoColor: true, Width: 60}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Count(out, "\n") < 2 {
		t.Fatalf("nothing wrapped, so nothing is under test:\n%s", out)
	}
	for _, whole := range []string{"--out", "--endpoint", "--access-key"} {
		if !strings.Contains(out, whole) {
			t.Errorf("%q was broken across lines:\n%s", whole, out)
		}
	}
}

// And a single token longer than the whole budget still cannot escape it: the
// word wrap leaves it alone, so a hard break has to finish the job.
func TestATokenLongerThanTheWidthIsStillBroken(t *testing.T) {
	v := view.Text{Body: "see https://example.com/" + strings.Repeat("a-very-long-path-segment/", 6)}
	var buf bytes.Buffer
	if err := Render(&buf, v, Options{Format: Pretty, NoColor: true, Width: 40}); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if w := lipgloss.Width(line); w > 40 {
			t.Errorf("line is %d cells wide, want at most 40: %q", w, line)
		}
	}
}
