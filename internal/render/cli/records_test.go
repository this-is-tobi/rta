package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// The table that made this necessary: five columns of prose, which at sixty
// cells gave each one ten and read as a column of syllables.
func findingsTable() view.Table {
	return view.Table{
		Columns: []view.Column{
			{Name: "Check"},
			{Name: "Status", Kind: view.KindStatus},
			{Name: "Detail"},
			{Name: "Reference"},
			{Name: "Link"},
		},
		Rows: [][]string{
			{"lodash", "fail",
				"lodash 4.17.20 (npm) — indirect, pulled in by express — named in 2 advisories",
				"A03:2025 Software Supply Chain Failures · CWE-1395",
				"https://osv.dev/vulnerability/GHSA-35jh-r3h4-6jhm"},
			{"express", "fail",
				"express 4.17.1 (npm) — a direct dependency — named in 1 advisory",
				"A03:2025 Software Supply Chain Failures · CWE-1395",
				"https://osv.dev/vulnerability/GHSA-rv95-896h-c2vc"},
		},
		Total: 2,
	}
}

func isGrid(out string) bool { return strings.Contains(out, "╭") }

// Below the width a grid needs, the same rows are drawn as records — and
// everything that was in them is still there, whole.
func TestANarrowTableBecomesRecords(t *testing.T) {
	out, widest := renderWidth(t, findingsTable(), Options{Width: 60, Fill: true})
	if isGrid(out) {
		t.Fatalf("still a grid at 60 cells:\n%s", out)
	}
	if widest > 60 {
		t.Errorf("a record line ran to %d cells:\n%s", widest, out)
	}
	// Nothing dropped and nothing truncated. The whole point of the layout is
	// that the values fit, so an ellipsis here would mean it had not worked.
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{
		"lodash 4.17.20 (npm) — indirect, pulled in by express — named in 2 advisories",
		"https://osv.dev/vulnerability/GHSA-35jh-r3h4-6jhm",
		"A03:2025 Software Supply Chain Failures · CWE-1395",
		"express 4.17.1 (npm) — a direct dependency — named in 1 advisory",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("record layout lost %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "…") {
		t.Errorf("record layout truncated something:\n%s", out)
	}
}

// The label column names every field, and the first column is the record's
// name rather than a field of it.
func TestARecordIsNamedAndLabelled(t *testing.T) {
	out, _ := renderWidth(t, findingsTable(), Options{Width: 60, Fill: true})
	for _, want := range []string{"lodash", "status", "detail", "reference", "link"} {
		if !strings.Contains(out, want) {
			t.Errorf("record layout has no %q:\n%s", want, out)
		}
	}
	// "check" is the first column's heading, and a record does not label its
	// own name.
	if strings.Contains(out, "check") {
		t.Errorf("the name column was repeated as a field:\n%s", out)
	}
}

// A pipe has no width to measure, so nothing about it changes. This is what
// keeps redirected output, goldens and diffs byte-identical.
func TestAPipeIsAlwaysAGrid(t *testing.T) {
	out, _ := renderWidth(t, findingsTable(), Options{})
	if !isGrid(out) {
		t.Errorf("unconstrained output stopped being a table:\n%s", out)
	}
}

// And a table that has the room keeps the grid, because a grid is the better
// arrangement whenever it fits.
func TestAWideTerminalKeepsTheGrid(t *testing.T) {
	out, _ := renderWidth(t, findingsTable(), Options{Width: 200, Fill: true})
	if !isGrid(out) {
		t.Errorf("a table with room to spare was drawn as records:\n%s", out)
	}
}

// The everyday shapes must not tip over. A short list at an ordinary width is
// a table and stays one.
func TestOrdinaryTablesStayGrids(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table view.Table
		width int
	}{
		{"two short columns at 80", view.Table{
			Columns: []view.Column{{Name: "Name"}, {Name: "Value"}},
			Rows:    [][]string{{"host", "db.internal"}, {"port", "5432"}},
		}, 80},
		{"numbers and a task at 60", taskTable, 60},
		{"one column at 40", view.Table{
			Columns: []view.Column{{Name: "Entry"}},
			Rows:    [][]string{{"prod-db-password"}, {"staging-token"}},
		}, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := renderWidth(t, tc.table, Options{Width: tc.width, Fill: true})
			if !isGrid(out) {
				t.Errorf("drawn as records with room for a grid:\n%s", out)
			}
		})
	}
}

// The selected row still reads as selected. The marker rather than only a
// background band, because --no-color leaves the band as nothing at all and
// the TUI's row cursor is the one thing on that screen a person is steering.
func TestTheSelectedRecordIsMarked(t *testing.T) {
	out, _ := renderWidth(t, findingsTable(), Options{Width: 60, Fill: true, Highlight: 2})
	var marked []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "▸ ") {
			marked = append(marked, strings.TrimPrefix(line, "▸ "))
		}
	}
	if len(marked) != 1 || marked[0] != "express" {
		t.Errorf("marked %v, want exactly [express]:\n%s", marked, out)
	}
}

// An empty cell is not a fact, and a line saying a field is blank is a line
// spent on nothing — which on a sixty-cell terminal is the whole problem.
func TestAnEmptyFieldGetsNoLine(t *testing.T) {
	tbl := findingsTable()
	tbl.Rows[0][4] = ""
	out, _ := renderWidth(t, tbl, Options{Width: 60, Fill: true})
	records := strings.Split(out, "\n\n")
	if len(records) != 2 {
		t.Fatalf("want two records, got %d:\n%s", len(records), out)
	}
	if strings.Contains(records[0], "link") {
		t.Errorf("an empty field got a line of its own:\n%s", records[0])
	}
	if !strings.Contains(records[1], "link") {
		t.Errorf("the field was dropped for the row that had one:\n%s", records[1])
	}
}

// Redaction is a rule about what may be drawn, not about how it is arranged.
// Both halves of it have to survive the layout: a masked column stays masked,
// and a cell with no column is still drawn nowhere — view.Redact masks by
// column name, so a nameless cell is one it cannot reach.
func TestRecordsObeyRedaction(t *testing.T) {
	tbl := findingsTable()
	tbl.Columns = append(tbl.Columns, view.Column{Name: "Token"})
	tbl.Redacted = []string{"Token"}
	tbl.Rows[0] = append(tbl.Rows[0],
		"ghp_notarealtoken",
		"ghp_asecondnotarealtoken", // no column: nothing can name it, so nothing may show it
	)
	out, _ := renderWidth(t, tbl, Options{Width: 50, Fill: true})
	if isGrid(out) {
		t.Fatalf("expected records at 50 cells:\n%s", out)
	}
	for _, secret := range []string{"ghp_notarealtoken", "ghp_asecondnotarealtoken"} {
		if strings.Contains(out, secret) {
			t.Errorf("a redacted value reached the screen:\n%s", out)
		}
	}
	if !strings.Contains(out, "token") {
		t.Errorf("the masked column vanished instead of being masked:\n%s", out)
	}
}

// A hyphen is not a place to break a line — inside a table cell either.
//
// wrap() has shielded them since `rta explain` broke `--endpoint` in half, and
// a lipgloss table does its own wrapping and never went through it. So a grid
// narrow enough to wrap a cell produced "GHSA-qw6h-vgh9-" / "j6wx": an
// advisory ID that cannot be copied off the screen, in the report whose entire
// purpose is to be acted on.
func TestAGridCellDoesNotBreakOnAHyphen(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "Check"}, {Name: "Detail"}},
		Rows: [][]string{{"lodash",
			"named in 3 advisories: GHSA-29mw-wpgm-hmr9, GHSA-35jh-r3h4-6jhm, GHSA-f23m-r3pf-42rh"}},
	}
	out, _ := renderWidth(t, tbl, Options{Width: 60, Fill: true})
	if !isGrid(out) {
		t.Fatalf("this test is about the grid, and it drew records:\n%s", out)
	}
	for _, id := range []string{"GHSA-29mw-wpgm-hmr9", "GHSA-35jh-r3h4-6jhm", "GHSA-f23m-r3pf-42rh"} {
		if !strings.Contains(out, id) {
			t.Errorf("%s was broken across lines:\n%s", id, out)
		}
	}
	// And the shielding is undone: what is on screen is a real hyphen, not the
	// non-breaking stand-in, or copying it out yields a string that matches
	// nothing.
	if strings.Contains(out, nonBreakingHyphen) {
		t.Errorf("a non-breaking hyphen survived into the output:\n%s", out)
	}
}

// An empty table is a sentence to a person and a table to everything else.
//
// A listing with nothing in it answered with a Text view, which every format
// carried: `-o json | jq '.rows[]'` met a view with no rows, and -o csv a
// shape it refused. The table now carries the sentence beside it, for the
// renderers drawn for a person alone: pretty on a screen, and md, a report
// somebody reads. Pretty into a pipe or a file is a program's, and gets the
// headings: the sentence redirected is a line a script reads as a row.
func TestAnEmptyTableIsASentenceOnlyToAPerson(t *testing.T) {
	const say = "Nothing here yet — add one with: rta note add --tag <name>"
	empty := view.Table{
		Columns:  []view.Column{{Name: "ID", Kind: view.KindNumber}, {Name: "Note"}},
		Empty:    say,
		Warnings: []view.Error{{Code: "note.store.partial", Message: "one file was skipped"}},
	}
	for _, width := range []int{0, 80} {
		out, _ := renderWidth(t, empty, Options{Width: width, Screen: true})
		if !strings.Contains(out, say) || isGrid(out) || !strings.Contains(out, "one file was skipped") {
			t.Errorf("pretty on a screen at width %d = %q, want the sentence and the warning, no grid", width, out)
		}
		out, _ = renderWidth(t, empty, Options{Width: width})
		if strings.Contains(out, "Nothing here yet") || !isGrid(out) || !strings.Contains(out, "one file was skipped") {
			t.Errorf("pretty into a pipe at width %d = %q, want the headings and the warning, no sentence", width, out)
		}
	}
	for _, f := range []Format{JSON, YAML, CSV} {
		if out := render(t, empty, f); strings.Contains(out, "Nothing here yet") {
			t.Errorf("%s carried the sentence meant for a person:\n%s", f, out)
		}
	}
	// md draws it as a paragraph in place of the grid, escaped the way a
	// cell is: a bare <name> is an HTML tag to whatever renders the report.
	if out := render(t, empty, Markdown); !strings.Contains(out, `--tag \<name>`) ||
		strings.Contains(out, "| ID") || !strings.Contains(out, "one file was skipped") {
		t.Errorf("md = %q, want the escaped sentence and the warning, no grid", out)
	}
	if out := render(t, empty, CSV); strings.TrimSpace(out) != "ID,Note" {
		t.Errorf("csv = %q, want the header row alone", out)
	}
	var env struct {
		Type string     `json:"type"`
		Rows [][]string `json:"rows"`
	}
	if err := json.Unmarshal([]byte(render(t, empty, JSON)), &env); err != nil || env.Type != "table" || env.Rows == nil {
		t.Errorf("json = %+v (%v), want a table whose rows are an empty array", env, err)
	}
	// A table with nothing to say keeps its headings, and one with rows
	// never shows the sentence.
	empty.Empty, empty.Warnings = "", nil
	if out, _ := renderWidth(t, empty, Options{Width: 80, Screen: true}); !isGrid(out) {
		t.Errorf("an empty table with no sentence lost its headings:\n%s", out)
	}
	full := view.Table{Columns: empty.Columns, Rows: [][]string{{"1", "x"}}, Empty: say}
	if out, _ := renderWidth(t, full, Options{Width: 80, Screen: true}); strings.Contains(out, "Nothing here yet") {
		t.Errorf("a table with rows showed the empty sentence:\n%s", out)
	}
	if out := render(t, full, Markdown); strings.Contains(out, "Nothing here yet") {
		t.Errorf("md of a table with rows showed the empty sentence:\n%s", out)
	}
}

// An empty text, tree or page follows the table's rule: a sentence to a
// person, and nothing at all to a program.
//
// A text that is the answer — a patch — carried its "nothing here" in the
// body, so `rta git diff > x.patch` on a clean tree wrote the sentence into
// the patch and -o json handed it to a script as the diff; a tree swapped
// itself for a text, so `jq '.roots[]'` met a view with no roots; and a page
// of fixes with none to offer did the same, so `rta audit clients --fix >
// fix.txt` wrote the sentence where the fixes go.
func TestAnEmptyTextTreeOrPageIsASentenceOnlyToAPerson(t *testing.T) {
	const say = "no uncommitted changes"
	for name, v := range map[string]view.View{
		"text":     view.Text{Empty: say},
		"tree":     view.Tree{Roots: []view.Node{}, Empty: say},
		"sections": view.Sections{Empty: say},
	} {
		if out, _ := renderWidth(t, v, Options{Width: 80, Screen: true}); out != say {
			t.Errorf("%s: pretty on a screen = %q, want the sentence", name, out)
		}
		var buf bytes.Buffer
		if err := Render(&buf, v, Options{Format: Pretty, NoColor: true}); err != nil || buf.Len() != 0 {
			t.Errorf("%s: pretty into a pipe = %q (%v), want nothing at all", name, buf.String(), err)
		}
		if out := render(t, v, Markdown); strings.TrimSpace(out) != say {
			t.Errorf("%s: md = %q, want the sentence", name, out)
		}
		for _, f := range []Format{JSON, YAML, CSV} {
			if out := render(t, v, f); strings.Contains(out, say) {
				t.Errorf("%s: %s carried the sentence meant for a person:\n%s", name, f, out)
			}
		}
	}
	var env struct {
		Roots []view.Node `json:"roots"`
	}
	if err := json.Unmarshal([]byte(render(t, view.Tree{Roots: []view.Node{}, Empty: say}, JSON)), &env); err != nil || env.Roots == nil {
		t.Errorf("json = %+v (%v), want a tree whose roots are an empty array", env, err)
	}
	var page struct {
		Items []view.Section `json:"items"`
	}
	if err := json.Unmarshal([]byte(render(t, view.Sections{Empty: say}, JSON)), &page); err != nil || page.Items == nil {
		t.Errorf("json = %+v (%v), want a page whose items are an empty array", page, err)
	}
	// A page with warnings and no sections says both: that it is empty, and
	// what it could not produce.
	partial := view.Sections{Empty: say, Warnings: []view.Error{{Code: "x.partial", Message: "one file was skipped"}}}
	if out, _ := renderWidth(t, partial, Options{Width: 80, Screen: true}); !strings.HasPrefix(out, say+"\n\n") ||
		!strings.Contains(out, "one file was skipped") {
		t.Errorf("a partial empty page on a screen = %q, want the sentence, a blank line and the warning", out)
	}
	// A text with a body, a tree with roots and a page with sections never
	// show the sentence.
	for name, v := range map[string]view.View{
		"text":     view.Text{Body: "diff --git a/x b/x", Empty: say},
		"tree":     view.Tree{Roots: []view.Node{{Label: "x"}}, Empty: say},
		"sections": view.Sections{Items: []view.Section{{Title: "one", View: view.Text{Body: "x"}}}, Empty: say},
	} {
		if out, _ := renderWidth(t, v, Options{Width: 80, Screen: true}); strings.Contains(out, say) {
			t.Errorf("%s with content showed the empty sentence:\n%s", name, out)
		}
		if out := render(t, v, Markdown); strings.Contains(out, say) {
			t.Errorf("%s with content showed the empty sentence in md:\n%s", name, out)
		}
	}
}

// A grid is drawn whole or not at all.
//
// A table a few cells wider than the terminal — narrower than its natural
// width by less than its borders take — was handed back by lipgloss at its
// natural width and cut at the edge: the last column's text stopped mid-word
// with no ellipsis, and the right border was gone. The agent log did it on a
// 140-column terminal, and since COLUMNS shapes a pipe, in a redirect too.
func TestAGridIsNeverCutAtTheEdge(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{
			{Name: "seq"}, {Name: "at", Kind: view.KindTimestamp}, {Name: "agent"}, {Name: "credential"},
			{Name: "capability"}, {Name: "arguments"}, {Name: "profile"},
			{Name: "outcome", Kind: view.KindStatus}, {Name: "authorized"}, {Name: "code"}, {Name: "why"},
		},
		Rows: [][]string{
			{"1", "2026-09-24 10:00:01", "probe", "-", "codec.jwt", "-", "-", "error", "grant", "-",
				`codec.jwt failed unexpectedly: unknown argument: "secret_file"`},
			{"2", "2026-09-24 10:00:02", "probe", "-", "sys.cpu", "-", "-", "ok", "grant", "-", "-"},
		},
	}
	for width := 120; width <= 190; width++ {
		out, widest := renderWidth(t, tbl, Options{Width: width})
		if widest > width {
			t.Errorf("width %d: drawn %d wide", width, widest)
		}
		if !isGrid(out) {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			if r := []rune(line); len(r) == 0 || !strings.ContainsRune("╮│┤╯", r[len(r)-1]) {
				t.Errorf("width %d: a grid line was cut at the edge: %q\n%s", width, line, out)
				break
			}
		}
		for _, word := range strings.Fields(tbl.Rows[0][10]) {
			if !strings.Contains(out, word) {
				t.Errorf("width %d: %q is missing from the grid:\n%s", width, word, out)
			}
		}
	}
}

// A table drawn as records says what a grid says under it: that the listing
// continues, and what it could not read.
//
// The record layout returned before the footer and the warnings were written,
// so the same partial listing read as complete on a narrow terminal — a split
// pane, a phone over ssh — and as partial on a wide one.
func TestRecordsSayTheListingIsPartialAsAGridDoes(t *testing.T) {
	tbl := findingsTable()
	tbl.Total = 40
	tbl.Page = &view.Cursor{Next: "express"}
	tbl.Warnings = []view.Error{{Code: "audit.deps.unreadable", Message: "no lock file"}}
	for _, width := range []int{60, 0} {
		out, _ := renderWidth(t, tbl, Options{Width: width})
		for _, want := range []string{"2 of 40 rows", "more rows after express", "no lock file"} {
			if !strings.Contains(out, want) {
				t.Errorf("width %d (grid %v) left out %q:\n%s", width, isGrid(out), want, out)
			}
		}
	}
}

// A non-breaking hyphen in the data is drawn as itself.
//
// It is the character shieldHyphens stands in for "-", and the restore turned
// every one back into "-" — the data's own included. So a host or a file name
// holding U+2011 was drawn as a different string: copied off the screen it
// named nothing, and a name made to look like another was shown as the one it
// imitates. The layout it gives up is a break at a hyphen, in the one table or
// value that holds the character.
func TestANonBreakingHyphenInTheDataIsDrawnAsItself(t *testing.T) {
	host := "paypal" + string(rune(0x2011)) + "login.example"
	tbl := view.Table{
		Columns: []view.Column{{Name: "Host"}, {Name: "Flag"}},
		Rows:    [][]string{{host, "--max-uses"}},
	}
	kv := view.KeyValue{Pairs: []view.Pair{{Key: "host", Value: host}}}
	for name, v := range map[string]view.View{"table": tbl, "keyvalue": kv} {
		for _, width := range []int{0, 80} {
			out, _ := renderWidth(t, v, Options{Width: width})
			if !strings.Contains(out, host) {
				t.Errorf("%s at width %d drew the name as another string:\n%s", name, width, out)
			}
		}
	}
	// And the rest of the renderer still shields: a flag near the margin of a
	// value holding no U+2011 does not break at its hyphen.
	flags := view.Text{Body: strings.Repeat("x", 14) + " --max-uses"}
	if out, _ := renderWidth(t, flags, Options{Width: 20}); !strings.Contains(out, "--max-uses") {
		t.Errorf("a flag broke at its hyphen:\n%s", out)
	}
}

// A record whose name is a number says what the number is.
//
// Most first columns are the record's identity in words and repeating the
// column name above every one of them is noise. `todo list` leads with an ID,
// and a bare "2" heading a block reads as a stray figure where "ID │ 2" under
// a heading never could.
func TestAKindedNameKeepsItsLabel(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{
			{Name: "ID", Kind: view.KindNumber},
			{Name: "Status", Kind: view.KindStatus},
			{Name: "Age", Kind: view.KindDuration},
			{Name: "Task"},
		},
		Rows: [][]string{{"2", "open", "5d", "polish the TUI forms and every screen they open from"}},
	}
	out, _ := renderWidth(t, tbl, Options{Width: 34, Fill: true})
	if isGrid(out) {
		t.Fatalf("expected records at 34 cells:\n%s", out)
	}
	if !strings.Contains(out, "id 2") {
		t.Errorf("a numeric record name lost its label:\n%s", out)
	}

	// And a text one does not get told, because it already says what it is.
	out, _ = renderWidth(t, findingsTable(), Options{Width: 60, Fill: true})
	if strings.Contains(out, "check lodash") {
		t.Errorf("a named record was labelled anyway:\n%s", out)
	}
}

// A URL that cannot fit beside its label takes the line to itself rather than
// being split in half. An advisory link broken across two lines is a link
// nobody can copy, which is the defect this whole layout came from.
func TestAnUnbreakableValueGetsItsOwnLine(t *testing.T) {
	out, _ := renderWidth(t, findingsTable(), Options{Width: 60, Fill: true})
	if !strings.Contains(out, "https://osv.dev/vulnerability/GHSA-35jh-r3h4-6jhm") {
		t.Errorf("the link was split:\n%s", out)
	}
	// Prose is not moved: it has spaces to break on, so it reads better
	// beside its label than under it.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "detail") && strings.TrimSpace(line) == "detail" {
			t.Errorf("a wrappable value was pushed onto its own line:\n%s", out)
		}
	}
}

// The layout whose entire job is to fit a narrow terminal has to fit every
// narrow terminal, including the ones below the width wrap() will break at.
//
// Two ways it overflowed: wrap() floors its budget at minWrap and returns the
// string untouched below it, so a value beside its label simply ran past the
// edge; and the label gutter was sized from the column headings with no cap,
// so a thirty-three-cell heading pushed every value line five cells past a
// fifty-cell terminal.
func TestRecordsNeverOverflowTheWidth(t *testing.T) {
	// From minWrap up: below it wrap() declines to break at all, on the stated
	// grounds that every break would land mid-word and the overflow reads
	// better. That is a rule of the wrapper, not of this layout, and no real
	// terminal is fifteen cells wide.
	for w := minWrap; w <= 80; w++ {
		out, widest := renderWidth(t, findingsTable(), Options{Width: w, Fill: true})
		if widest > w {
			t.Fatalf("width %d produced a %d-cell line:\n%s", w, widest, out)
		}
	}
}

func TestALongColumnHeadingDoesNotPushValuesOffTheEdge(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{
			{Name: "One"},
			{Name: "A very long column heading indeed"},
			{Name: "Detail"},
		},
		Rows: [][]string{{"first", "some value here",
			"a detail long enough that no grid could be drawn for this table at all"}},
	}
	out, widest := renderWidth(t, tbl, Options{Width: 50, Fill: true})
	if isGrid(out) {
		t.Fatalf("expected records at 50 cells:\n%s", out)
	}
	if widest > 50 {
		t.Errorf("a %d-cell line in a 50-cell terminal:\n%s", widest, out)
	}
}

// A record's name is a value too, and a single token longer than the budget
// left the title line holding nothing but the marker, with the name hanging
// underneath a blank heading.
func TestALongNameDoesNotLeaveABlankTitle(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "Package"}, {Name: "Detail"}},
		Rows: [][]string{{"@babel/plugin-transform-runtime-and-then-some",
			"a detail long enough to force the record layout at this width"}},
	}
	out, _ := renderWidth(t, tbl, Options{Width: 30, Fill: true})
	if isGrid(out) {
		t.Fatalf("expected records at 30 cells:\n%s", out)
	}
	if strings.HasPrefix(out, "\n") || strings.HasPrefix(strings.TrimRight(strings.Split(out, "\n")[0], " "), "") &&
		strings.TrimSpace(strings.Split(out, "\n")[0]) == "" {
		t.Errorf("the record opens with a blank title line:\n%q", out)
	}
	if !strings.Contains(strings.Join(strings.Fields(out), ""), "@babel/plugin-transform-runtime-and-then-some") {
		t.Errorf("the name was lost:\n%s", out)
	}
}

// The label was the one thing on the line nothing measured.
//
// A heading wider than the capped gutter gets the line to itself — the right
// call, since shortening the label is the wrong economy — but it was then
// emitted raw, `"    " + label`, with no wrap and no cap. So the layout whose
// entire job is to fit a narrow terminal ran past the right edge of one
// whenever `4 + width(heading) > width`. Real headings reach that under about
// twenty columns; a plugin-declared heading is a string this renderer does not
// control and reaches it far sooner.
//
// Swept across every width rather than sampled, because the previous two
// defects in this file were both a single width where one branch was taken.
func TestARecordLabelNeverRunsPastTheEdge(t *testing.T) {
	long := "Backup-eligible retention window"
	table := view.Table{
		Columns: []view.Column{
			{Name: "Check"},
			{Name: long},
			{Name: "Link"},
		},
		Rows: [][]string{{
			"lodash",
			"kept for ninety days after the last write, then collected",
			"https://osv.dev/vulnerability/GHSA-35jh-r3h4-6jhm",
		}},
		Total: 1,
	}
	// From minWrap up: below it wrap() declines to break at all and says so,
	// which is a stated limit of the whole renderer rather than this path.
	for width := minWrap; width <= 90; width++ {
		out, widest := renderWidth(t, table, Options{Width: width, Fill: true})
		if widest > width {
			t.Fatalf("width %d: output is %d cells wide\n%s", width, widest, out)
		}
		// And it is broken, not cut: every character of the heading survives,
		// so a reader can still find their way down the record.
		// Case-insensitively: the renderer lowercases a heading, which is a
		// styling choice and not a loss of information.
		squashed := strings.ToLower(strings.Join(strings.Fields(out), ""))
		if !strings.Contains(squashed, strings.ToLower(strings.ReplaceAll(long, " ", ""))) {
			t.Fatalf("width %d: the heading lost characters\n%s", width, out)
		}
	}
}
