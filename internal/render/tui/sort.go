package tui

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Sorting a result: `S` on a result whose table has rows to put in order opens
// a picker of the orders it can be read in — each column one way and the
// other, and as the capability returned it — the way a web table sorts on its
// header and a list on GitHub sorts from one menu. A menu rather than a key
// per column or a column cursor, because a result's keys are most of a
// keyboard already (every letter a plugin's row actions bind), and a menu
// names the orders in words: "Size — largest first" is what a header arrow
// stands for, said.
//
// **By what a cell is, not by how it is written.** A size sorts by its bytes
// and a time by its moment, read through view.Table.Value — the value a
// program gets in -o json — so 9.0 KiB comes after 10.0 GiB. A status sorts
// by how bad it is, as the theme grades it, a name the way a person reads one
// (pod-2 before pod-10). A cell with nothing to sort by — a dash, a blank, a
// measurement nobody took — goes last whichever way the column is sorted,
// and rows that tie keep the order they came in.
//
// **The order stays with the result, and the cursor with its row.** A re-run
// and a live refresh of the same capability come back in it, as a web table
// keeps its sort when it reloads, and a result held behind a drill-down is
// restored in the order it was left in. In both the cursor is put back on the
// row it was on, found by its first cell, since a sorted refresh can put a
// new row above it and the next key acts on whatever row the cursor is on. A
// masked column is not offered: sorting by it would say how the values nobody
// may see compare.

// tableOrder is how a result's table is sorted: by the column named, either
// way. The zero order is the capability's own.
type tableOrder struct {
	column string
	desc   bool
}

// pickValue spells o as a sort picker's value: a direction letter and the
// column's name, or "" for the capability's own order.
func (o tableOrder) pickValue() string {
	if o.column == "" {
		return ""
	}
	if o.desc {
		return "d" + o.column
	}
	return "a" + o.column
}

// orderOfPick is pickValue read back.
func orderOfPick(value string) tableOrder {
	if len(value) < 2 {
		return tableOrder{}
	}
	return tableOrder{column: value[1:], desc: value[0] == 'd'}
}

// orderWords are the two ways a column of kind reads in order, the one a
// person scanning it wants first: the largest, the longest, the newest and
// the worst at the top, and names from A.
func orderWords(kind view.ColumnKind) (first, second string, firstDesc bool) {
	switch kind {
	case view.KindBytes:
		return "largest first", "smallest first", true
	case view.KindNumber, view.KindPercent, view.KindUsage:
		return "highest first", "lowest first", true
	case view.KindDuration:
		return "longest first", "shortest first", true
	case view.KindTimestamp:
		return "newest first", "oldest first", true
	case view.KindStatus:
		return "worst first", "best first", false
	}
	return "A to Z", "Z to A", false
}

// orderSaid is o as the result's line says it.
func orderSaid(t view.Table, o tableOrder) string {
	col := orderColumn(t, o.column)
	if col < 0 {
		return ""
	}
	first, second, firstDesc := orderWords(t.Columns[col].Kind)
	words := first
	if o.desc != firstDesc {
		words = second
	}
	said := "sorted by " + o.column + ", " + words
	// A page of a longer list is sorted as far as it goes: the rows past it
	// are not here to be put in order, and the line says so rather than
	// letting "largest first" read as the largest of all of them.
	if isPart(t) {
		said += ", on this page"
	}
	return said
}

// isPart reports whether t is a part of a longer list: a total past its rows,
// a cursor to more of them, or no total and a warning that says something is
// missing, which is how a list stopped at its limit with nothing to count it
// says it goes on (rowCount, warningsHead). Tested on the total alone, git log
// at its limit sorted oldest first read as the repository's first commits.
func isPart(t view.Table) bool {
	if t.Total > len(t.Rows) || t.Page != nil && t.Page.Next != "" {
		return true
	}
	return t.Total == 0 && slices.ContainsFunc(t.Warnings, func(w view.Error) bool { return !w.Advisory })
}

// orderColumn is the index of the column named in t that may be sorted by, or
// -1: a masked column may not.
func orderColumn(t view.Table, name string) int {
	if name == "" || slices.Contains(t.Redacted, name) {
		return -1
	}
	return slices.IndexFunc(t.Columns, func(c view.Column) bool { return c.Name == name })
}

// valued reports whether column col has anything to sort by: a column whose
// kind sorts by value with a value in at least one row, or any other. A
// plugin's table filled through Rows alone holds its sizes as text with no
// value, and offered, a sort by one moved no row while the line said the
// table was sorted by it.
func valued(t view.Table, col int) bool {
	switch t.Columns[col].Kind {
	case view.KindNumber, view.KindBytes, view.KindPercent, view.KindUsage, view.KindDuration, view.KindTimestamp:
		for i := range t.Rows {
			if t.Value(i, col) != "" {
				return true
			}
		}
		return false
	}
	return true
}

// orderOptions are the orders t can be read in: the capability's own first,
// then each column it may be sorted by, the way a person wants it first and
// then the other way.
func orderOptions(t view.Table) []pickOption {
	options := []pickOption{{label: "as returned"}}
	for i, c := range t.Columns {
		if orderColumn(t, c.Name) < 0 || !valued(t, i) {
			continue
		}
		first, second, firstDesc := orderWords(c.Kind)
		label := textclean.Terminal(c.Name)
		options = append(options,
			pickOption{label: label + " — " + first, value: tableOrder{c.Name, firstDesc}.pickValue()},
			pickOption{label: label + " — " + second, value: tableOrder{c.Name, !firstDesc}.pickValue()})
	}
	return options
}

// sortable reports whether the result on screen has a table with rows to put
// in order and a column to put them in order by.
func (m Model) sortable() bool {
	tbl, _, ok := rowTable(m.result.raw)
	return ok && m.result.returned != nil && len(tbl.Rows) > 1 && len(orderOptions(tbl)) > 1
}

// startSortPick opens the sort picker on the order the result is in.
func (m Model) startSortPick() (tea.Model, tea.Cmd) {
	tbl, _, _ := rowTable(m.result.raw)
	return m.openPick(newPickForm(pickSort, "sort by", m.result.order.pickValue(), orderOptions(tbl), m.current, modeResult))
}

// sortResult puts the result's table in order o, the cursor staying on the row
// it was on rather than on whatever row moved under it: the row an action acts
// on is the one the cursor shows.
func (m Model) sortResult(o tableOrder) Model {
	at := m.row
	if at < len(m.result.perm) {
		at = m.result.perm[at]
	}
	m.result = m.result.inOrder(o)
	m.row = at
	if m.result.perm != nil {
		m.row = max(slices.Index(m.result.perm, at), 0)
	}
	m.renderResult()
	return m
}

// inOrder is r with its table in order o: the rows, and their values with
// them, rearranged from the order the capability returned them in, and the
// screen's copy cleaned again from that. An order the table cannot take — a
// column a re-run no longer returns — leaves it as returned.
func (r resultMsg) inOrder(o tableOrder) resultMsg {
	if r.returned == nil || o == r.order {
		return r
	}
	r.raw, r.order, r.perm = r.returned, tableOrder{}, nil
	if v, perm, ok := ordered(r.returned, o); ok {
		r.raw, r.order, r.perm = v, o, perm
	}
	r.view = view.MapStrings(r.raw, textclean.Terminal)
	return r
}

// heldState is the order a result of capability id lands in and the row its
// cursor goes back to (cursorKey), and whether that came from the trail: the
// result on screen's when it is the same capability (a re-run, a refresh, a
// live tick), and otherwise what the trail kept for id when it last left the
// screen. The result on screen is kept on its own trail entry first, so that
// coming back to it, after the drill-down this result may be, finds it.
//
// By the row and not its index. Kept by index, a refresh that sorted a newly
// parked call above the cursor moved it to another call, and a list that came
// back from a drill-down unsorted put it on another note, and the next key —
// a deny, a done — acted on that one.
func (m *Model) heldState(id string) (order tableOrder, key string, back bool) {
	key = cursorKey(m.result.view, m.row)
	if m.result.cap.ID == id {
		return m.result.order, key, false
	}
	for i := range m.trail {
		if m.trail[i].cap.ID == m.result.cap.ID {
			m.trail[i].order, m.trail[i].cursor = m.result.order, key
		}
	}
	for _, t := range m.trail {
		if t.cap.ID == id {
			return t.order, t.cursor, t.cursor != ""
		}
	}
	return tableOrder{}, "", false
}

// cursorKey names row i of v's row table by its first cell, which names the
// row on every screen (it is never given up), or is "" where there is no such
// row.
func cursorKey(v view.View, i int) string {
	tbl, _, ok := rowTable(v)
	if !ok || i < 0 || i >= len(tbl.Rows) || len(tbl.Rows[i]) == 0 {
		return ""
	}
	return tbl.Rows[i][0]
}

// rowNamed is the row of tbl whose first cell is key, the one nearest i where
// several are, and i kept inside the table where none is: a row that went is
// replaced by the one that took its place.
func rowNamed(tbl view.Table, key string, i int) int {
	found := -1
	if key != "" {
		for j, row := range tbl.Rows {
			if len(row) > 0 && row[0] == key && (found < 0 || abs(j-i) < abs(found-i)) {
				found = j
			}
		}
	}
	if found >= 0 {
		return found
	}
	return min(max(i, 0), max(len(tbl.Rows)-1, 0))
}

func abs(n int) int { return max(n, -n) }

// ordered is v with its row table (rowTable) sorted by o, and where each row
// now shown came from.
func ordered(v view.View, o tableOrder) (view.View, []int, bool) {
	tbl, at, ok := rowTable(v)
	if !ok {
		return v, nil, false
	}
	col := orderColumn(tbl, o.column)
	if col < 0 {
		return v, nil, false
	}
	sorted, perm := sortTable(tbl, col, o.desc)
	if at < 0 {
		return sorted, perm, true
	}
	page := v.(view.Sections)
	page.Items = slices.Clone(page.Items)
	page.Items[at].View = sorted
	return page, perm, true
}

// sortTable is t with its rows in order by column col, its values moved with
// them, and perm[i] the row that is now row i. Stable, so rows that tie keep
// the order they came in.
func sortTable(t view.Table, col int, desc bool) (view.Table, []int) {
	keys := make([]sortKey, len(t.Rows))
	for i := range t.Rows {
		keys[i] = keyOf(t, i, col)
	}
	perm := make([]int, len(t.Rows))
	for i := range perm {
		perm[i] = i
	}
	slices.SortStableFunc(perm, func(a, b int) int { return keys[a].compare(keys[b], desc) })
	out := t
	out.Rows = make([][]string, len(perm))
	if t.Values != nil {
		out.Values = make([][]string, len(perm))
	}
	for i, p := range perm {
		out.Rows[i] = t.Rows[p]
		if p < len(t.Values) {
			out.Values[i] = t.Values[p]
		}
	}
	return out, perm
}

// sortKey is what one cell is sorted by.
type sortKey struct {
	missing bool
	number  float64
	at      time.Time
	rank    int
	text    string
	by      sortBy
}

type sortBy int

const (
	byText sortBy = iota
	byNumber
	byTime
	byGrade
)

// keyOf is the key of row i's cell in column col.
func keyOf(t view.Table, i, col int) sortKey {
	var text string
	if cells := t.Rows[i]; col < len(cells) {
		text = strings.TrimSpace(cells[col])
	}
	k := sortKey{text: text, missing: text == "" || text == "-" || text == "—"}
	switch t.Columns[col].Kind {
	case view.KindNumber, view.KindBytes, view.KindPercent, view.KindUsage, view.KindDuration:
		n, err := strconv.ParseFloat(t.Value(i, col), 64)
		k.by, k.number, k.missing = byNumber, n, err != nil
	case view.KindTimestamp:
		at, err := time.Parse(time.RFC3339Nano, t.Value(i, col))
		k.by, k.at, k.missing = byTime, at, err != nil
	case view.KindStatus:
		grade := theme.ClassifyStatus(text)
		// What is over and done with (done, closed, disabled) is what neither
		// order is for: last either way, beside what has nothing to sort by.
		// Ranked above good, "best first" opened on the done to-dos.
		k.by, k.rank, k.missing = byGrade, gradeRank(grade), k.missing || grade == theme.StatusMuted
	}
	return k
}

// gradeRank orders the grades worst first: what is failing, then what wants a
// look, then what the theme cannot read, then what is fine.
func gradeRank(kind theme.StatusKind) int {
	switch kind {
	case theme.StatusBad:
		return 0
	case theme.StatusWarn:
		return 1
	case theme.StatusGood:
		return 3
	}
	return 2
}

// compare orders k against o, a cell with nothing to sort by after every cell
// with something whichever way the column is sorted.
func (k sortKey) compare(o sortKey, desc bool) int {
	switch {
	case k.missing && o.missing:
		return 0
	case k.missing:
		return 1
	case o.missing:
		return -1
	}
	var c int
	switch k.by {
	case byNumber:
		c = cmp.Compare(k.number, o.number)
	case byTime:
		c = k.at.Compare(o.at)
	case byGrade:
		// A tie in grade keeps the order the rows came in, which the sort is
		// stable for: broken by the text, OVERDUE · Oct 20 came before
		// OVERDUE · Sep 30, by the month's name.
		c = cmp.Compare(k.rank, o.rank)
	default:
		c = naturalCompare(k.text, o.text)
	}
	if desc {
		return -c
	}
	return c
}

// naturalCompare orders text the way a person reads it: a run of digits as the
// number it spells, so pod-2 comes before pod-10, and letters without regard
// to case.
func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		if isDigit(a[0]) && isDigit(b[0]) {
			da, db := digitsOf(a), digitsOf(b)
			na, nb := strings.TrimLeft(da, "0"), strings.TrimLeft(db, "0")
			if c := cmp.Or(cmp.Compare(len(na), len(nb)), strings.Compare(na, nb)); c != 0 {
				return c
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		ra, sa := utf8.DecodeRuneInString(a)
		rb, sb := utf8.DecodeRuneInString(b)
		if c := cmp.Compare(unicode.ToLower(ra), unicode.ToLower(rb)); c != 0 {
			return c
		}
		a, b = a[sa:], b[sb:]
	}
	return cmp.Compare(len(a), len(b))
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// digitsOf is the run of digits s begins with.
func digitsOf(s string) string {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return s[:i]
}
