package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The line under a result counts what came back, and a one-row table is as
// ordinary a result as there is: it read "1 of 1 rows".
func TestTheResultLineCountsInTheRightNumber(t *testing.T) {
	c := plugin.Capability{ID: "demo.list", Safety: plugin.Read}
	for rows, want := range map[int]string{1: "1 of 1 row", 3: "3 of 3 rows"} {
		table := view.Table{Columns: []view.Column{{Name: "Name"}}}
		for range rows {
			table.Rows = append(table.Rows, []string{"x"})
		}
		m := Model{current: c, result: resultMsg{cap: c, view: table}}
		got := plain(m.resultMeta())
		if !strings.Contains(got, want) || (rows == 1 && strings.Contains(got, "rows")) {
			t.Errorf("%d rows: meta = %q, want %q", rows, got, want)
		}
	}
}

// A table that could not count what it left out has no total and says so in a
// warning (a git log stopped at its limit), and "2 of 2 rows" beside "the history
// continues" read as the whole of the thing the warning says is not whole. A table
// that never counted and has nothing to warn of is whole, and keeps its "N of N".
func TestATableThatCouldNotCountDoesNotClaimItsRowsAreAll(t *testing.T) {
	c := plugin.Capability{ID: "git.log", Safety: plugin.Read}
	older := view.Error{Code: "git.log.older", Message: "the history continues past the newest 2 commits", Advisory: true}
	denied := view.Error{Code: "fs.unreadable", Message: "a directory could not be read"}
	table := func(total int, warnings ...view.Error) view.Table {
		return view.Table{Columns: []view.Column{{Name: "Hash"}}, Rows: [][]string{{"a1"}, {"b2"}},
			Total: total, Warnings: warnings}
	}
	for _, tc := range []struct {
		name string
		v    view.Table
		want string
		not  string
	}{
		{"a cut log", table(0, older), "2 rows · ⚠ 1 warning", "of"},
		{"a listing with a missing part and no count", table(0, denied), "2 rows · ⚠ partial (1 warning)", "of"},
		{"a listing that counted", table(463, denied), "2 of 463 rows", ""},
		{"a whole one that never counted", table(0), "2 of 2 rows", ""},
	} {
		m := Model{current: c, result: resultMsg{cap: c, view: tc.v}}
		got := plain(m.resultMeta())
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: meta = %q, want %q", tc.name, got, tc.want)
		}
		if tc.not != "" && strings.Contains(got, " "+tc.not+" ") {
			t.Errorf("%s: meta = %q, claims a total nobody counted", tc.name, got)
		}
	}
}

// The line above the fold heads an answer partial only for a warning that
// says something is missing from it. The roster's advisories — a grant bound
// to a replaced plugin build, a server on another build of rta — sit beside
// rows that are all there, and read "partial" once a role stood. A table's
// own warnings are headed as a page's are: the flat roster said nothing.
func TestOnlyAMissingPartHeadsAnAnswerPartial(t *testing.T) {
	c := plugin.Capability{ID: "grant.list", Safety: plugin.Read}
	missing := view.Error{Code: "fs.unreadable", Message: "a directory could not be read"}
	replaced := view.Error{Code: "grant.artifact.replaced", Message: "1 grant was issued on a replaced plugin",
		Advisory: true}
	older := view.Error{Code: "core.grant.older.server", Message: "1 server is open on another build",
		Advisory: true}
	table := func(warnings ...view.Error) view.Table {
		return view.Table{Columns: []view.Column{{Name: "Capability"}}, Rows: [][]string{{"hello.wipe"}},
			Warnings: warnings}
	}
	page := func(warnings ...view.Error) view.Sections {
		return view.Sections{Items: []view.Section{
			{ID: "roles", Title: "Roles in force", View: view.Text{Body: "dev for claude"}},
			{ID: "grants", Title: "Allowed", View: table(warnings...)},
		}}
	}
	for _, tc := range []struct {
		name string
		v    view.View
		want string
	}{
		{"a flat roster's advisory", table(replaced), "⚠ 1 warning"},
		{"a flat table's missing part", table(missing), "⚠ partial (1 warning)"},
		{"a page's advisories", page(replaced, older), "⚠ 2 warnings"},
		{"a page with a missing part among them", page(replaced, missing), "⚠ partial (2 warnings)"},
	} {
		m := Model{current: c, result: resultMsg{cap: c, view: tc.v}}
		got := plain(m.resultMeta())
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: meta = %q, want %q", tc.name, got, tc.want)
		}
		if !strings.Contains(tc.want, "partial") && strings.Contains(got, "partial") {
			t.Errorf("%s: meta = %q, heads a whole answer partial", tc.name, got)
		}
	}
	m := Model{current: c, result: resultMsg{cap: c, view: table()}}
	if got := plain(m.resultMeta()); strings.Contains(got, "⚠") {
		t.Errorf("meta = %q, warns over a table with nothing to warn of", got)
	}
}

// An empty listing's pane says what would fill it, and the line above it
// does not count the nothing it replaces: "0 of 0 rows" over "nothing is
// locked" said the same thing twice, the second time as a figure.
//
// A table with no sentence keeps its count, since there the headings are
// drawn and the count is what says nothing came back.
func TestAnEmptyListingIsASentenceNotACount(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = sized.(Model)
	c := plugin.Capability{ID: "demo.list", Safety: plugin.Read}
	table := view.Table{Columns: []view.Column{{Name: "Name"}}, Empty: "nothing is locked"}
	m.mode, m.current = modeResult, c
	m.result = resultMsg{cap: c, view: table, raw: table}
	m.renderResult()
	pane := plain(m.viewport.View())
	if !strings.Contains(pane, "nothing is locked") || strings.Contains(pane, "NAME") {
		t.Errorf("pane = %q, want the sentence in place of the headings", pane)
	}
	if meta := plain(m.resultMeta()); strings.Contains(meta, "row") {
		t.Errorf("meta = %q, want no count above the sentence", meta)
	}
	table.Empty = ""
	m.result = resultMsg{cap: c, view: table, raw: table}
	if meta := plain(m.resultMeta()); !strings.Contains(meta, "0 of 0 rows") {
		t.Errorf("meta = %q, want the count over an empty grid", meta)
	}
}

// A page with no sections says what would fill it in the pane, and the line
// above it lists no titles: an empty list of them left a separator pointing
// at nothing.
func TestAnEmptyPageIsASentenceNotAnEmptyContents(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = sized.(Model)
	c := plugin.Capability{ID: "demo.fix", Safety: plugin.Read}
	page := view.Sections{Empty: "nothing to paste"}
	m.mode, m.current = modeResult, c
	m.result = resultMsg{cap: c, view: page, raw: page}
	m.renderResult()
	if pane := plain(m.viewport.View()); !strings.Contains(pane, "nothing to paste") {
		t.Errorf("pane = %q, want the sentence", pane)
	}
	if meta := strings.TrimSpace(plain(m.resultMeta())); strings.HasSuffix(meta, "·") {
		t.Errorf("meta = %q, want no separator before an empty list of titles", meta)
	}
}

// A narrow pane drops the line's counts, not its warning. At forty columns the
// pane clipped the right end of "read · idempotent · 15 of 463 rows · ⚠ partial
// (1 warning)", which is the one part saying the answer is not all there.
func TestANarrowResultLineKeepsItsWarning(t *testing.T) {
	c := plugin.Capability{ID: "sys.ps", Safety: plugin.Read, Idempotent: true}
	tbl := view.Table{
		Columns:  []view.Column{{Name: "PID"}},
		Rows:     [][]string{{"1"}, {"2"}},
		Total:    463,
		Warnings: []view.Error{{Code: "sys.ps.denied", Message: "a process could not be read"}},
	}
	m := Model{current: c, result: resultMsg{cap: c, view: tbl}}
	for _, width := range []int{36, 30, 24} {
		m.viewport.SetWidth(width)
		got := plain(m.resultMeta())
		if !strings.Contains(got, "⚠ partial (1 warning)") {
			t.Errorf("at %d columns the warning went: %q", width, got)
		}
		if w := lipgloss.Width(got); w > width {
			t.Errorf("at %d columns the line is %d wide: %q", width, w, got)
		}
	}
	m.viewport.SetWidth(200)
	if got := plain(m.resultMeta()); !strings.Contains(got, "read · idempotent · 2 of 463 rows") {
		t.Errorf("a wide pane lost its counts: %q", got)
	}
}

// `r` is advertised as "refresh" and ran the call again, so on a result that
// said "added note 2" a second press made note 3 with nothing asked. A write
// that is not idempotent is not re-run once it has succeeded; a failure is
// retried, and a read or an idempotent write is refreshed as before.
func TestAWriteThatSucceededIsNotRerunByTheRefreshKey(t *testing.T) {
	m := storeModel(t)
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	done := view.Text{Body: "added note 2: dup test"}
	for name, tc := range map[string]struct {
		cap  plugin.Capability
		res  resultMsg
		want bool
	}{
		"a read":                         {plugin.Capability{ID: "note.list", Safety: plugin.Read, Run: run}, resultMsg{view: done}, true},
		"an idempotent write":            {plugin.Capability{ID: "kv.set", Safety: plugin.Write, Idempotent: true, Run: run}, resultMsg{view: done}, true},
		"a write that is not":            {plugin.Capability{ID: "note.add", Safety: plugin.Write, Run: run}, resultMsg{view: done}, false},
		"a destructive call that is not": {plugin.Capability{ID: "docker.container.rm", Safety: plugin.Destructive, Run: run}, resultMsg{view: done}, false},
		"the same write, failed":         {plugin.Capability{ID: "note.add", Safety: plugin.Write, Run: run}, resultMsg{err: view.Errorf("note.add.failed", "no")}, true},
		"nothing yet":                    {plugin.Capability{ID: "note.list", Safety: plugin.Read, Run: run}, resultMsg{}, false},
	} {
		m.current = tc.cap
		m.result = tc.res
		if got := m.canRerun(); got != tc.want {
			t.Errorf("%s: canRerun = %v, want %v", name, got, tc.want)
		}
		offered := false
		for _, it := range m.resultFooterItems() {
			if it.label == "refresh" || it.label == "re-run" {
				offered = true
			}
		}
		if offered != tc.want {
			t.Errorf("%s: the footer offers the key = %v, want %v", name, offered, tc.want)
		}
	}
}
