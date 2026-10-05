package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func notebook() view.Table {
	return view.Table{
		Columns: []view.Column{
			{Name: "ID", Kind: view.KindNumber},
			{Name: "Status", Kind: view.KindStatus},
			{Name: "Due", Kind: view.KindStatus},
			{Name: "Age", Kind: view.KindDuration},
			{Name: "Note"},
		},
		Rows: [][]string{
			{"1", "open", "OVERDUE", "9h", "past due thing"},
			{"3", "open", "ok", "2h", "call the dentist about the appointment"},
			{"2", "open", "", "5m", "write the release notes"},
		},
	}
}

func glanced(t *testing.T, v view.View, inner int) []string {
	t.Helper()
	lines, ok := glanceTable(v, inner)
	if !ok {
		t.Fatalf("a table of %v at %d cells was left to the renderer", v, inner)
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = plain(l)
		if w := lipgloss.Width(out[i]); w > inner {
			t.Errorf("line %d is %d cells in a %d-cell tile: %q", i, w, inner, out[i])
		}
	}
	return out
}

// A notebook of three entries took five lines each in a tile at 80 columns, so
// one and a half notes were on screen. As a table without borders it is the
// heading and a line per note, and what a note says survives: the name, how
// overdue it is, and its words.
func TestATableTooWideForItsTileIsOneLinePerRow(t *testing.T) {
	lines := glanced(t, notebook(), 35)
	if len(lines) != 4 {
		t.Fatalf("a header and three rows are %d lines:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], "ID") || !strings.Contains(lines[0], "DUE") || !strings.Contains(lines[0], "NOTE") {
		t.Errorf("the headings are gone, so the cells have no names: %q", lines[0])
	}
	if !strings.Contains(lines[1], "OVERDUE") || !strings.Contains(lines[1], "past due thing") {
		t.Errorf("the first note lost its grade or its words: %q", lines[1])
	}
	if strings.Contains(strings.Join(lines, ""), "│") {
		t.Errorf("a borderless table has borders:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[2], "…") {
		t.Errorf("the long note is not cut to the room there is: %q", lines[2])
	}
}

// A table that has the room is drawn by the renderer as the grid it has always
// been; the reshaping is for the box that cannot hold one.
func TestATableThatFitsIsLeftToTheRenderer(t *testing.T) {
	if lines, ok := glanceTable(notebook(), 100); ok {
		t.Errorf("a table with room for its grid was reshaped:\n%s", strings.Join(lines, "\n"))
	}
}

// "open" down a list of open notes is not a thing to glance at, and neither is
// a column with nothing in it. They go before a column that varies is touched,
// and a lone row keeps what it has, because one row is not "the same as the
// others".
func TestAColumnThatSaysNothingGoesFirst(t *testing.T) {
	tbl := notebook()
	tbl.Rows[0][3], tbl.Rows[1][3], tbl.Rows[2][3] = "9h", "9h", "9h"
	lines := glanced(t, tbl, 44)
	for _, gone := range []string{"STATUS", "AGE"} {
		if strings.Contains(lines[0], gone) {
			t.Errorf("%s says the same in every row and is still drawn: %q", gone, lines[0])
		}
	}
	for _, kept := range []string{"ID", "DUE", "NOTE"} {
		if !strings.Contains(lines[0], kept) {
			t.Errorf("%s varies and was dropped: %q", kept, lines[0])
		}
	}

	single := notebook()
	single.Rows = single.Rows[:1]
	one := glanced(t, single, 42)
	if !strings.Contains(one[0], "STATUS") {
		t.Errorf("a lone row lost a column for being like its neighbours, which it has none of: %q", one[0])
	}
}

func services() view.Table {
	return view.Table{
		Columns: []view.Column{
			{Name: "Service"},
			{Name: "Owner"},
			{Name: "State", Kind: view.KindStatus},
			{Name: "Since", Kind: view.KindDuration},
			{Name: "Summary"},
		},
		Rows: [][]string{
			{"billing", "payments", "ok", "3d", "invoices and refunds for every plan"},
			{"search", "discovery", "warn", "5h", "the index and its nightly rebuild"},
			{"mail", "platform", "ok", "9d", "outbound transactional email"},
		},
	}
}

// When a column has to go it is a plain one, from the end of the table back: the
// columns at the end are the secondary facts. The ones that say when go next and
// the ones that grade themselves are the last of all, because a glance is for a
// verdict.
func TestWhenAColumnMustGoThePlainOnesGoBeforeTheOnesThatDateOrGrade(t *testing.T) {
	header := glanced(t, services(), 38)[0]
	if strings.Contains(header, "OWNER") {
		t.Errorf("a plain column survived where a verdict had to go: %q", header)
	}
	for _, want := range []string{"SERVICE", "STATE", "SINCE", "SUMMARY"} {
		if !strings.Contains(header, want) {
			t.Errorf("%s was dropped before the plain column: %q", want, header)
		}
	}
	tight := glanced(t, services(), 28)[0]
	if strings.Contains(tight, "SINCE") || !strings.Contains(tight, "STATE") {
		t.Errorf("at 28 cells the date goes before the verdict: %q", tight)
	}
}

// A grant's note is on the screen for the one grant that has one, and costs the
// other rows a column of nothing: it goes before a full column does. The
// notebook's text, full on every row, is what it is for, and stays.
func TestAMostlyEmptyColumnGoesBeforeAFullOne(t *testing.T) {
	grants := view.Table{
		Columns: []view.Column{
			{Name: "Capability"},
			{Name: "Record"},
			{Name: "Expires In", Kind: view.KindDuration},
			{Name: "Budget Left"},
			{Name: "Note"},
		},
		Rows: [][]string{
			{"note.add", "any", "15m", "unlimited", ""},
			{"net.dns", "any", "30m", "unlimited", "debugging dns"},
			{"kv.get", "db-password", "1h", "3 of 3 uses", ""},
		},
	}
	header := glanced(t, grants, 45)[0]
	if strings.Contains(header, "NOTE") {
		t.Errorf("a column that is empty on two rows of three outlived a full one: %q", header)
	}
	for _, want := range []string{"CAPABILITY", "RECORD", "EXPIRES IN"} {
		if !strings.Contains(header, want) {
			t.Errorf("%s was dropped: %q", want, header)
		}
	}
	if kept := glanced(t, notebook(), 35)[0]; !strings.Contains(kept, "NOTE") {
		t.Errorf("the text of a notebook, full on every row, was dropped as an annotation: %q", kept)
	}
}

// The mask is the renderer's, and the lines are built from the cells, so the
// reshaping masks first: the one place a secret column could be read raw would
// be the layout that was meant to be a courtesy.
func TestAGlancedTableStillMasksWhatItIsToldToMask(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "Name"}, {Name: "Kind"}, {Name: "Value"}},
		Rows: [][]string{
			{"db-main-url", "connection", "marker-one-and-a-long-tail"},
			{"api-main-ref", "reference", "marker-two-and-a-long-tail-too"},
		},
		Redacted: []string{"Value"},
	}
	joined := strings.Join(glanced(t, tbl, 30), "\n")
	if strings.Contains(joined, "marker-one") || strings.Contains(joined, "marker-two") {
		t.Errorf("a redacted column reached the screen through the reshaped table:\n%s", joined)
	}

	// Masked, every cell reads the same, so beside other rows the column says
	// nothing and goes; a lone row keeps it, and keeps it masked.
	tbl.Rows = tbl.Rows[:1]
	one := strings.Join(glanced(t, tbl, 30), "\n")
	if strings.Contains(one, "marker-one") || !strings.Contains(one, view.Mask) {
		t.Errorf("the one row shows its value or not its mask:\n%s", one)
	}
}

// What the renderer says under a table it drew whole is owed under one it drew
// as lines: rows missing, a cursor, and the warnings of a partial or flagged
// answer. A reshaping must never be why somebody does not see them.
func TestAGlancedTableKeepsWhatTheRendererPutsUnderATable(t *testing.T) {
	tbl := notebook()
	tbl.Total = 40
	tbl.Page = &view.Cursor{Next: "c7"}
	tbl.Warnings = []view.Error{{Code: "demo.partial", Message: "2 unreadable"}}
	joined := strings.Join(glanced(t, tbl, 35), "\n")
	for _, want := range []string{"3 of 40 rows", "more rows after c7", "2 unreadable"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the footer lost %q:\n%s", want, joined)
		}
	}
}

// Somebody has to be able to read the tile, so a box too narrow for the name
// and a stub of text is left to the renderer's cards instead of drawn as a
// column of ellipses.
func TestATileTooNarrowForAGlanceIsLeftToTheRenderer(t *testing.T) {
	if lines, ok := glanceTable(notebook(), 14); ok {
		t.Errorf("a 14-cell tile was reshaped:\n%s", strings.Join(lines, "\n"))
	}
	if _, ok := glanceTable(view.Text{Body: "not a table"}, 35); ok {
		t.Error("a text view was reshaped as a table")
	}
	empty := notebook()
	empty.Rows = nil
	empty.Empty = "Nothing here yet"
	if _, ok := glanceTable(empty, 35); ok {
		t.Error("an empty table was reshaped, and its sentence is the renderer's to draw")
	}
}

// A path longer than the box wrapped over three lines of its own, and the tile
// that said where the store is spent most of its height on the one value that
// is the same whatever the box is. The middle goes, because the end is what
// names the file; the page enter opens has all of it.
func TestAPathTooLongForItsTileIsCutInTheMiddle(t *testing.T) {
	path := "/Users/somebody/.local/share/rta/stores/work/team-secrets.age"
	kv := view.KeyValue{Pairs: []view.Pair{
		{Key: "store", Value: path},
		{Key: "state", Value: "no store yet — `kv.set` creates it the first time it runs"},
	}}
	got, ok := glanceKeyValue(kv, 36).(view.KeyValue)
	if !ok {
		t.Fatal("a key/value view came back as something else")
	}
	cut := got.Pairs[0].Value
	if w := lipgloss.Width(cut); w > 36-len("state")-2 {
		t.Errorf("the path is %d cells and does not fit beside its key: %q", w, cut)
	}
	if !strings.HasPrefix(cut, "/Users") || !strings.HasSuffix(cut, "team-secrets.age") || !strings.Contains(cut, "…") {
		t.Errorf("a path cut in the middle keeps its start and the file's name: %q", cut)
	}
	if got.Pairs[1].Value != kv.Pairs[1].Value {
		t.Errorf("prose was cut, and it wraps instead: %q", got.Pairs[1].Value)
	}
	if kv.Pairs[0].Value != path {
		t.Error("the view the capability returned was edited in place; the copy key reads it")
	}

	body := plain(tileBody(kv, 36))
	if n := strings.Count(strings.TrimRight(body, "\n"), "\n") + 1; n > 4 {
		t.Errorf("the tile still takes %d lines:\n%s", n, body)
	}
}

// A value that fits, or that is words and not a path, is left exactly as it is.
func TestAValueThatFitsOrIsProseIsNotCut(t *testing.T) {
	kv := view.KeyValue{Pairs: []view.Pair{
		{Key: "store", Value: "/tmp/kv.age"},
		{Key: "note", Value: strings.Repeat("words ", 12)},
	}}
	if got := glanceKeyValue(kv, 36); got.(view.KeyValue).Pairs[0].Value != "/tmp/kv.age" || got.(view.KeyValue).Pairs[1].Value != kv.Pairs[1].Value {
		t.Errorf("a short path or prose was changed: %v", got)
	}
	if got := glanceKeyValue(view.Text{Body: "/a/very/long/path/that/is/not/a/pair"}, 10); got.(view.Text).Body != "/a/very/long/path/that/is/not/a/pair" {
		t.Error("a text view was cut")
	}
}

// On the dashboard itself, at the width of the screen most people open it on:
// the notebook's tile is the heading and a line per note, not five lines each.
func TestATableTileOnTheDashboardIsOneLinePerRow(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	reg := registry.New()
	// Two tiles on the row, so each has half the screen and the notebook has no
	// room for its grid.
	for _, p := range []plugin.Plugin{
		{Name: "aside", Summary: "aside", Capabilities: []plugin.Capability{{
			ID: "aside.overview", Summary: "beside it", Safety: plugin.Read, Idempotent: true,
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "beside"}, nil },
		}}},
		{Name: "pad", Summary: "pad", Capabilities: []plugin.Capability{{
			ID: "pad.overview", Summary: "the notebook", Safety: plugin.Read, Idempotent: true,
			Run: func(context.Context, plugin.Request) (view.View, error) { return notebook(), nil },
		}}},
	} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	m := filled(t, New(reg, config.Dashboard{}, nil), 80, 24)
	frame := plain(m.View().Content)
	for _, want := range []string{"OVERDUE", "past due thing", "call the dentist", "write the release"} {
		if !strings.Contains(frame, want) {
			t.Errorf("%q is not on the first screen:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "status") || strings.Contains(frame, "┬") {
		t.Errorf("the tile is drawn as cards or as a bordered grid:\n%s", frame)
	}
}

// A tile is handed what the capability returned, and the renderer cleans what it
// draws. The lines of a glanced table are built here, so the cleaning is too: a
// note's title is what an agent with a grant writes, and an OSC 52 in it would
// put text on the reader's clipboard the moment the tile painted.
func TestAGlancedTableIsCleanedOfTerminalEscapes(t *testing.T) {
	tbl := notebook()
	tbl.Rows[0][4] = "past due\x1b]52;c;aGk=\x07 thing\x1b[2J"
	tbl.Columns[4].Name = "Note\x1b]0;owned\x07"
	for _, body := range []string{
		strings.Join(glanced(t, tbl, 35), "\n"),
		plain(tileBody(tbl, 35)),
	} {
		if strings.ContainsAny(body, "\x1b\a\r") {
			t.Errorf("a control character reached the screen through a glanced table: %q", body)
		}
		if !strings.Contains(body, "past due") {
			t.Errorf("the cell lost its text along with its escape: %q", body)
		}
	}

	kv := view.KeyValue{Pairs: []view.Pair{{Key: "store", Value: "/home/somebody/\x1b]52;c;aGk=\x07/a/long/path/to/the/file.age"}}}
	if body := plain(tileBody(kv, 30)); strings.ContainsAny(body, "\x1b\a") {
		t.Errorf("a control character reached the screen through a cut path: %q", body)
	}
}

// A cell can hold a line break: a note's title is free text, and one with a
// newline in it used to put its second half at the left edge of the tile as a
// row of its own, under the name column, where it reads as a line the tile
// said. A glanced table is one line per row, whatever the cells hold.
func TestAGlancedCellWithALineBreakStaysOnItsRow(t *testing.T) {
	tbl := notebook()
	tbl.Rows[0][4] = "past due\nfake  3 waiting on you\r\nthing"
	lines := glanced(t, tbl, 35)
	if len(lines) != 4 {
		t.Fatalf("a header and three rows are %d lines, a break in a cell made more:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	for _, l := range lines {
		if strings.ContainsAny(l, "\r\n") {
			t.Errorf("a line break is inside a glanced line: %q", l)
		}
	}
	if !strings.Contains(lines[1], "past due") {
		t.Errorf("the row lost its words: %q", lines[1])
	}
}
