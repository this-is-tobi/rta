package tui

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The result pane: rendering what a run returned, the actions and view
// toggles a capability offers over it, and the footer that says so.

// resultMsg carries a finished capability run back into the update loop.
// Rendering happens at paint time so results adapt to the current width.
type resultMsg struct {
	cap plugin.Capability
	// view is what the screen shows, nil on error: once the result has
	// arrived (cleaned), every string in it has been through
	// textclean.Terminal, and it is what anything the TUI draws itself reads.
	view view.View
	// raw is the view as the capability returned it — see cleaned.
	raw     view.View
	elapsed time.Duration
	// via is the forward the run opened, TunnelNone when it opened none — which
	// is not what the connection said: a box typed over a coordinate is a call
	// straight to what was typed. The result pane names where a run went from
	// this, and not from the environment the picker named (adHocNote).
	via plugin.Tunnel
	// err is the capability's own, never cleaned here: the one thing that
	// draws it is cli.RenderError, which cleans its own copy.
	err *view.Error
	// seq identifies the run this result belongs to. A result from a run the
	// user walked away from must not appear over whatever they walked to.
	seq int
	// live marks the answer of a live view's refresh (refreshInPlace), which
	// nobody asked for by pressing anything: it lands only on the view it
	// refreshes, where seq alone let it land wherever the reader had gone.
	live bool
	// unfilled marks a run that never started because its connection's
	// references could not be fetched (fillRefs): where the call goes failed,
	// and not the call. A live view's refresh flashes it and keeps its rows,
	// as refreshInPlace does a resolve failure, which this was until the fetch
	// moved off the update loop into the run (runCmd).
	unfilled bool
}

// cleaned is r as it arrives on screen: view cleaned once, for everything the
// TUI draws itself, and the view the capability returned kept beside it as
// raw.
//
// Two copies, because two different readers. Cleaning at ingress made the
// cell on screen and the value an action used one string, which fixed a row
// action acting on something other than what was shown — by acting on the
// display spelling instead. A key holding a bidi isolate, which i18n stacks
// insert on their own, reached kv.get as its backslash-u spelling, a key
// that does not exist; an edit form prefilled from a note handed its title
// back escaped, so saving without touching it rewrote the note. So a row
// action, a copy and `y` read raw — the record's own value — and the pane
// reads raw too, because cli.Render cleans its own copy and a string is
// cleaned once on its way to the screen, not once per layer. What is left
// for view is the TUI's own drawing around the pane: the meta line's section
// titles, a flashed one-liner.
func (r resultMsg) cleaned() resultMsg {
	r.raw = r.view
	r.view = view.MapStrings(r.view, textclean.Terminal)
	return r
}

// warningsBlock renders a page's degradations beneath its content, in the
// same heading-plus-rule grammar the sections themselves use.
//
// renderResult produces the viewport content for the current result at the
// current width; interactive lists get their selected row accented.
func (m *Model) renderResult() {
	var buf bytes.Buffer
	opts := m.resultOptions()
	switch {
	case m.result.err != nil:
		_ = cli.RenderError(&buf, m.result.err, opts)
	case m.result.raw != nil:
		if err := cli.Render(&buf, m.result.raw, opts); err != nil {
			_ = cli.RenderError(&buf, view.AsError(err, "core.render.failed"), opts)
		}
	}
	content := strings.TrimRight(buf.String(), "\n")
	if meta := m.resultMeta(); meta != "" {
		content = meta + "\n\n" + content
	}
	m.viewport.SetContent(content)
	if m.interactive() {
		// Keep the selected row in view.
		line, top, h := m.rowLine(opts), m.viewport.YOffset(), m.viewport.Height()
		if line < top {
			m.viewport.SetYOffset(line)
		} else if line >= top+h-1 {
			m.viewport.SetYOffset(line - h + 2)
		}
	}
}

// resultOptions is how the pane draws the current result.
func (m Model) resultOptions() cli.Options {
	// Fill: this is a bordered pane of a fixed size, so a table narrower than
	// the frame is not restraint, it is a gap where a title could have been.
	opts := cli.Options{Format: cli.Pretty, Width: max(m.width-4, 20), Fill: true, Screen: true}
	if m.interactive() {
		opts.Highlight = m.row + 1
	}
	return opts
}

// rowLine is the line of the answer the cursor's row is drawn on: meta(2) +
// table chrome(2) + row, below whatever a page draws above its table.
func (m Model) rowLine(opts cli.Options) int {
	line := m.row + 4
	if page, isPage := m.result.raw.(view.Sections); isPage {
		_, at, _ := rowTable(page)
		line += linesAbove(page, at, opts)
	}
	return line
}

// rowShown says whether the cursor's row is on the pane, by the rule the render
// keeps it there by.
func (m Model) rowShown() bool {
	line, top := m.rowLine(m.resultOptions()), m.viewport.YOffset()
	return line >= top && line < top+m.viewport.Height()-1
}

// linesAbove is how many lines a page draws above its section at: the
// sections before it, the blank line parting them from it, and its heading.
// Drawn rather than counted, since a section's height is whatever its view
// wraps to at this width.
func linesAbove(page view.Sections, at int, opts cli.Options) int {
	if at <= 0 {
		return 1
	}
	var buf bytes.Buffer
	opts.Highlight = 0
	if err := cli.Render(&buf, view.Sections{Items: page.Items[:at]}, opts); err != nil {
		return 1
	}
	return strings.Count(strings.TrimRight(buf.String(), "\n"), "\n") + 1 + 2
}

// resultMeta is the context line under the panel title: safety class in the
// shared status colors, idempotency, and the shape of what came back. It
// carries real information and keeps sparse results from floating in space.
//
// The safety class is a cue (theme.Cue) and no grade: it says what the call may
// do, so it keeps its colour under every setting of how status is drawn.
func (m Model) resultMeta() string {
	if m.result.err != nil {
		return ""
	}
	safety := string(m.current.Safety)
	class, _ := theme.Cue(safety)
	parts := []string{class.Render(safety)}
	warn := ""
	if m.current.Idempotent {
		parts = append(parts, theme.Subtle.Render("idempotent"))
	}
	switch v := m.result.view.(type) {
	case view.Table:
		n := len(v.Rows)
		// Not over an empty listing's sentence, which the pane draws in
		// place of the grid: "0 of 0 rows" above "nothing is locked" says
		// the same thing twice, the second time as a figure. The count stays
		// over an empty grid, where it is what says nothing came back.
		if n > 0 || v.Empty == "" {
			parts = append(parts, theme.Subtle.Render(rowCount(v)))
		}
		if m.interactive() && n > 0 {
			parts = append(parts, theme.Subtle.Render(fmt.Sprintf("row %d/%d", m.row+1, n)))
		}
		// A table's own warnings, headed as a page's are below and for their
		// reason: a table carries the field a page does (view.Table.Warnings),
		// and a listing short of what it could not read was headed as whole
		// on its own and as partial inside a page — the grant roster among
		// them, flat until a role stood.
		if head := warningsHead(v.Warnings); head != "" {
			warn = theme.WarnText.Render(head)
		}
	case view.KeyValue:
		parts = append(parts, theme.Subtle.Render(format.CountOf(len(v.Pairs), "field")))
	case view.Chart:
		parts = append(parts, theme.Subtle.Render(fmt.Sprintf("%d series", len(v.Series))))
	case view.Text:
		if v.Markdown {
			parts = append(parts, theme.Subtle.Render("markdown"))
		}
	case view.Sections:
		titles := make([]string, 0, len(v.Items))
		for _, it := range v.Items {
			titles = append(titles, it.Title)
		}
		// None for a page with no sections, which the pane says in words
		// (view.Sections.Empty): an empty list of them left a separator
		// pointing at nothing.
		if len(titles) > 0 {
			parts = append(parts, theme.Subtle.Render(strings.Join(titles, " › ")))
		}
		// Which row the cursor is on, for a page walked by its table
		// (rowTable), as a table's own line says.
		if tbl, _, ok := rowTable(v); ok && m.interactive() {
			parts = append(parts, theme.Subtle.Render(fmt.Sprintf("row %d/%d", m.row+1, len(tbl.Rows))))
		}
		// The title list is the page's table of contents, and a page that
		// lost three of its sections lists the survivors exactly as
		// confidently as a whole one does. This line is above the fold, so
		// it is where "you are not looking at all of it" has to be said.
		// Which parts and why is the renderer's job: the pane draws through
		// cli.Render, which already prints the warnings under the sections,
		// and a second copy here drew every one of them twice — in a
		// narrower block that truncated the messages the first copy wrapped.
		if head := warningsHead(pageWarnings(v)); head != "" {
			warn = theme.WarnText.Render(head)
		}
	}
	return " " + fitMeta(parts, warn, m.viewport.Width())
}

// rowCount is the figure for what a table came back with: "N of M rows", M
// being what its producer counted.
//
// A table with rows, no total and a warning is one that could not count what it
// left out, which is how a git log stopped at its limit says the history goes
// on. "20 of 20 rows" beside that warning claims the whole of what the warning
// says is not there, so the figure is "20 rows" and the warning, which the line
// keeps whatever the width, carries the rest. A table with no total and no
// warning is the many that did not count and are whole, and keeps "N of N".
func rowCount(t view.Table) string {
	n := len(t.Rows)
	if t.Total == 0 && n > 0 && len(t.Warnings) > 0 {
		return format.CountOf(n, "row")
	}
	return fmt.Sprintf("%d of %s", n, format.CountOf(max(t.Total, n), "row"))
}

// fitMeta joins the context line's parts, and when they are wider than the
// pane drops them from the right, the safety class last, so a long line loses
// its counts rather than being cut mid-word. The warning is held back and
// always kept: it says "you are not looking at all of it", and at forty
// columns it was the part the pane clipped, in favour of "read · idempotent".
func fitMeta(parts []string, warn string, width int) string {
	sep := theme.Subtle.Render(" · ")
	for {
		all := parts
		if warn != "" {
			all = append(append([]string{}, parts...), warn)
		}
		line := strings.Join(all, sep)
		if width <= 0 || lipgloss.Width(line)+1 <= width || len(parts) == 0 {
			return line
		}
		parts = parts[:len(parts)-1]
	}
}

// warningsHead is what the line above the fold says of the warnings under
// an answer: "⚠ partial (2 warnings)" when any of them accounts for a part
// that is missing, and "⚠ 2 warnings" when every one sits beside a whole
// answer (view.Error.Advisory). Every warning headed "partial" once, and
// the roster read partial above rows missing nothing whenever a grant was
// bound to a replaced plugin build or a server ran another build of rta —
// the heading a person reads as "you are not looking at all of it", over
// the one screen that answers what an agent may do.
//
// Counted whole either way: the number is how many warnings are under the
// answer, and partial is whether any of them says something is missing.
func warningsHead(warnings []view.Error) string {
	n := len(warnings)
	if n == 0 {
		return ""
	}
	for _, w := range warnings {
		if !w.Advisory {
			return fmt.Sprintf("⚠ partial (%s)", format.CountOf(n, "warning"))
		}
	}
	return "⚠ " + format.CountOf(n, "warning")
}

// pageWarnings collects what a composite page could not produce, recursing
// into nested pages: a detail view is sections of sections, so a sensor that
// failed two levels down is exactly as absent as one that failed at the top,
// and just as worth saying out loud.
func pageWarnings(v view.View) []view.Error {
	switch t := v.(type) {
	case view.Sections:
		out := append([]view.Error(nil), t.Warnings...)
		for _, item := range t.Items {
			out = append(out, pageWarnings(item.View)...)
		}
		return out
	case view.Table:
		// A table carries the same field for the same reason, and a table
		// nested in a section is exactly as able to be partial as the page
		// around it.
		return t.Warnings
	}
	return nil
}

// flashText condenses an action result into a one-line footer notice.
//
// Only a capability that declares Flash, with a genuine one-liner Text, is
// drawn as itself; anything else folds to "<capability> done". The
// declaration is checked first, because the shape alone is the wrong test:
// a revealed secret is a short Text too, and gating on the shape would have
// painted it onto the list the moment a reveal happened to fit. Flash is a
// claim about the capability — "my result is a confirmation, never the
// value acted on" — and a Read capability cannot make it (Validate refuses),
// which is what keeps kv.get's answer on a page of its own.
func flashText(msg resultMsg) string {
	if msg.cap.Flash {
		if t, ok := msg.view.(view.Text); ok && !strings.Contains(t.Body, "\n") && len(t.Body) <= maxFlashLen {
			return t.Body
		}
		if line, ok := lockReceipt(msg); ok {
			return line
		}
	}
	return msg.cap.ID + " done"
}

// lockReceipt is the line a placed lock leaves in the footer: the capability's
// own receipt, "locked agent claude", where "lock.add done" left a person who
// had pressed the key on a row to find out from `rta lock list` whom it was.
// Taken from what the capability answered and not from what the form meant to
// ask, so a lock on every agent, or one placed on a server, says that.
func lockReceipt(msg resultMsg) (string, bool) {
	if msg.cap.ID != lockCapability {
		return "", false
	}
	kv, ok := msg.view.(view.KeyValue)
	if !ok {
		return "", false
	}
	for _, p := range kv.Pairs {
		if line := "locked " + p.Value; p.Key == "locked" && !strings.Contains(line, "\n") && len(line) <= maxFlashLen {
			return line, true
		}
	}
	return "", false
}

// maxFlashLen bounds a flashed one-liner. Generous for an ordinary
// confirmation ("copied gh-token to the clipboard"), tight enough that a
// capability whose Text result turns out to hold something longer — a
// value, not a verdict — cannot fill the footer with it.
const maxFlashLen = 120

// toggleOn reports what a toggle is currently showing, which is not always
// what its field holds: runCmd turns `detail` on for a Detailed capability
// when nobody said otherwise, so an unset field is already on screen as on.
// Reading the raw map instead put an unticked box under a page that was
// plainly detailed, and spent the first press of D setting true on something
// that was already true — a keystroke whose entire effect was the tick mark
// appearing.
func (m Model) toggleOn(t viewToggle) bool {
	if on, given := m.lastValues[t.field].(bool); given {
		return on
	}
	return t.field == "detail" && m.current.Detailed
}

// toggleView re-runs the current view with one input flipped. The trail
// remembers the new values, so an action launched from here comes back to the
// list as the user left it rather than as it was first opened.
func (m Model) toggleView(t viewToggle) (tea.Model, tea.Cmd) {
	values := map[string]any{}
	for k, v := range m.lastValues {
		values[k] = v
	}
	values[t.field] = !m.toggleOn(t)
	m.lastValues = values
	if len(m.trail) > 0 {
		m.trail[len(m.trail)-1].values = values
	}
	m.row = 0
	return m, m.startRun(m.current, values, m.lastYes)
}

// selectedAction finds a tile action of the selected tile bound to key.
// "enter" specs are excluded: enter opens the tile itself.
func (m Model) selectedAction(key string) (capAction, bool) {
	if m.selected < 0 || m.selected >= len(m.tiles) || key == "enter" {
		return capAction{}, false
	}
	for _, a := range m.tiles[m.selected].actions {
		if a.key == key {
			return a, true
		}
	}
	return capAction{}, false
}

// runAction executes an action of the current view. The identity of the
// record it acts on comes from the selected row, from the record the page is
// already about, or from nowhere at all (add). Mutations reload the view
// afterwards; anything still missing — edit content, a destructive
// confirmation — opens a form first.
func (m Model) runAction(a capAction, tbl view.Table) (tea.Model, tea.Cmd) {
	// The row an action acts on is one on screen. On a page with more than a
	// pane beyond its list the pager carries the pane past the list (walks),
	// and the cursor's row off it; an action pressed there acted on a row
	// nobody could see, which on the grant roster is x revoking a grant at
	// once. So the press brings the row back, and the next one acts on it.
	if a.src == srcRow && m.mode == modeResult && m.interactive() && !m.rowShown() {
		m.renderResult()
		m.guide(fmt.Sprintf("%s %s acts on the highlighted row, back on screen now — press %s again", a.key, a.label, a.key))
		return m, nil
	}
	if where, ok := m.follows(a); !ok {
		m.refuse(a.cap.ID + " acts on this machine only, and these rows are " + where + "'s")
		return m, nil
	}
	base, ok := m.actionSeed(a, tbl)
	if !ok {
		return m, nil
	}
	if nm, cmd, answered := m.answer(a, base); answered {
		return nm, cmd
	}
	return m.runSeeded(a, base)
}

// runSeeded is the rest of an action once the record it acts on is known: what
// the form opens with, whether it opens at all, and the run. Split from
// runAction so that a one-key answer can arm itself on the record it was
// pressed on and run on that one later, whatever the list under it has become
// by then.
func (m Model) runSeeded(a capAction, base map[string]any) (tea.Model, tea.Cmd) {
	// What the action opens is the capability without the inputs that point
	// it at another machine — see hereOnly — unless the view it was pressed
	// in was itself pointed at one.
	//
	// That exception is the whole of a real bug. A listing run with
	// `--server prod` has rows that are prod's state, and hereOnly used to
	// drop the aim from the action launched off one of them: `x` on a row of
	// `lock list --server prod` ran `lock rm` *here*, on the kind and name
	// the row seeded, with no form and no confirmation — lock.rm is Write
	// rather than Destructive, and the row supplies both inputs hereOnly
	// left, so fieldsAfter came back empty and startRun fired on one
	// keypress. So a remote server chose which local principal an operator
	// unlocked. The same shape reached grant.revoke from grant.list, and
	// agent.approve/agent.deny from agent.pending. The `profile` case just
	// below is this bug already found once and fixed for one input: "the row
	// identity from one connection, the call aimed at another".
	//
	// The aim travels, and the passphrase deliberately does not. Leaving the
	// secret unseeded keeps it in fieldsAfter, so the form opens and a signed
	// write to another machine is something the operator authorises, rather
	// than something one keypress does with a passphrase that was typed to
	// read a list.
	cap := a.cap
	aim := m.aimedElsewhere(cap)
	if len(aim) == 0 {
		cap.Inputs = hereOnly(cap.Inputs)
	}
	cap.Inputs = guardIdle(cap)
	for name, v := range aim {
		base[name] = v
	}
	// Safety != Read is the wrong proxy on its own: kv.get is Write for what
	// it discloses, not because it changes anything, and refreshPending
	// existing at all is "a mutation happened, reload the list it came
	// from" — which kv.get is not. Left as Safety alone, its result took the
	// flash-and-reload branch in tui.go's resultMsg handler: the value
	// became the flash text, painted onto the list pane instead of arriving
	// on its own result page the way kv.list's own comment promises. Flash
	// is the declaration's word for it: a mutation that answers with a
	// confirmation flashes and reloads, and anything else — a reveal, a
	// result somebody has to read — gets its own page.
	m.refreshPending = cap.Safety != plugin.Read && cap.Flash
	// Removing the very record this page is about destroys the page: the
	// reload afterwards has to land one level further back.
	m.subjectGone = a.src == srcSelf && cap.Safety == plugin.Destructive
	// Read before m.current moves: an action carries on from the view it was
	// pressed in, so the form has to open on that view's inputs and not on
	// the declared defaults of the capability replacing it.
	prev := over(m.suggested(cap), m.continuing(cap))
	// The environment travels with it, and it is not one of those inputs:
	// `profile` is reserved on a capability a profile can fill, so `asked`
	// filters it out by construction. Without this, a row from a listing run
	// against prod opened its removal form on whatever happened to be switched
	// on — the row identity from one connection, the call aimed at another —
	// and the non-form branch below erased the answer from lastValues as well,
	// re-aiming the next `r` too.
	if plugin.Profilable(cap) {
		if picked, ok := m.lastValues[profileInput]; ok {
			base[profileInput] = picked
		}
	}
	m.current = cap
	// The store session answers the unlock pair before the form question is
	// asked, so a kv action in an already-unlocked TUI runs where it used
	// to open a form holding nothing but the passphrase box. Disclosing
	// capabilities are left out inside — see kvsession.go.
	base = withStoreSession(cap, base)
	// A bare action waives only the optional-field form — never the
	// destructive gate, which is the confirmation screen and is reached
	// whether or not a form came first: with inputs still to ask, the form
	// collects them and hands over; with nothing left to ask, the screen
	// opens directly on what the call would do.
	if !a.bare {
		if values, quick := m.quickRunFrom(cap, base); quick {
			base, a.bare = values, true
		}
	}
	if !a.bare && len(fieldsAfter(cap, base)) > 0 {
		return m.startFormWith(cap, base, prev)
	}
	if cap.Safety == plugin.Destructive {
		return m, m.startConfirm(cap, base)
	}
	m.lastValues, m.lastYes = base, false
	return m, m.startRun(cap, base, false)
}

// aimedElsewhere are the values that point the view on screen at another
// machine, read off the call that produced it — `server` on the lock, grant
// and agent listings, which all spell it the same way.
//
// Empty for a listing of this machine's own state, which is what keeps
// hereOnly's shape for the common case: an action off a local queue does not
// grow a box for a server nobody named.
func (m Model) aimedElsewhere(cap plugin.Capability) map[string]any {
	aim := map[string]any{}
	for _, f := range cap.Inputs {
		if !f.Remote {
			continue
		}
		v, ok := m.lastValues[f.Name]
		if !ok {
			continue
		}
		if s, isString := v.(string); isString && s == "" {
			continue
		}
		aim[f.Name] = v
	}
	return aim
}

// follows reports whether a can go where the view it is pressed in was
// aimed, and names that place when it cannot: the aim travels to an action
// only through an input of its own (aimedElsewhere), and one without that
// input runs here. The rows of `grant list --server lab` are lab's, and n
// on one opened grant.renew, which takes no server, on this machine's
// grants of the same name — an extension, on a machine the row was never
// about. Such an action is not offered on a view aimed elsewhere
// (resultFooterItems), and pressed anyway it says why and opens nothing.
func (m Model) follows(a capAction) (string, bool) {
	carried := m.aimedElsewhere(a.cap)
	for name, v := range m.aimedElsewhere(m.current) {
		if _, ok := carried[name]; !ok {
			return fmt.Sprint(v), false
		}
	}
	return "", true
}

// hereOnly drops the inputs that point a call at another machine, and the
// ones only read beside them, from a form an action opens: the queue under
// the cursor is this machine's, so a box for the server it might instead be
// parked on is a box for a different call. Typing the capability's name in
// the catalogue still offers every input.
func hereOnly(fields []plugin.Field) []plugin.Field {
	remote := map[string]bool{}
	for _, f := range fields {
		if f.Remote {
			remote[f.Name] = true
		}
	}
	if len(remote) == 0 {
		return fields
	}
	out := make([]plugin.Field, 0, len(fields))
	for _, f := range fields {
		if remote[f.Name] || remote[f.With] {
			continue
		}
		out = append(out, f)
	}
	return out
}

// actionSeed is the identity an action acts on, read from the selected row,
// from the record the page is already about, or from nowhere at all (add).
// ok is false when the screen has nothing to act on.
func (m Model) actionSeed(a capAction, tbl view.Table) (map[string]any, bool) {
	base := map[string]any{}
	keys, _ := keyFields(a.cap)
	switch a.src {
	case srcRow:
		// A row names one record, so the first positional identifies it even
		// when the capability does not insist on one: `grant revoke` accepts
		// --all instead of a target, which does not make the target any less
		// the thing a row is about.
		if len(keys) == 0 {
			keys = firstPositional(a.cap)
		}
		if len(tbl.Rows) == 0 || len(keys) == 0 {
			return nil, false
		}
		row := tbl.Rows[min(m.row, len(tbl.Rows)-1)]
		if len(row) == 0 {
			return nil, false
		}
		// Every key input a column is named for, and the first column for
		// the first key otherwise. A record is not always one value: a lock
		// is a kind and a name, and a row that carries both should not open
		// a form asking for the half it is already showing. A seeded key
		// reads its own column, and is left for the form when the row has
		// none: the first column is an id, and an id is the wrong lock name.
		for i, f := range keys {
			var raw string
			var found bool
			if col := a.seed[f.Name]; col != "" {
				if raw, found = cellNamed(tbl, row, col); !found || strings.TrimSpace(raw) == "" {
					continue
				}
			} else if raw, found = cellNamed(tbl, row, f.Name); !found {
				if i != 0 {
					continue
				}
				raw = row[0]
			}
			v, err := rowKey(f, raw)
			if err != nil {
				return nil, false
			}
			base[f.Name] = v
		}
		// And every other input the row shows a column for: a grant row
		// carries its record, agent and profile, and `x` on one of two
		// note.rm rows used to revoke both because only the target seeded.
		// The boxes still open for anything a person would change; these
		// fill them with what the row already says.
		unread := false
		for _, f := range a.cap.Inputs {
			if _, done := base[f.Name]; done || f.Local || (f.Type != plugin.String && f.Type != plugin.Int) {
				continue
			}
			column := f.Name
			raw, found := cellNamed(tbl, row, column)
			if !found {
				column = columnAlias[f.Name]
				raw, found = cellNamed(tbl, row, column)
			}
			if !found {
				continue
			}
			if read := cellReader[a.from][strings.ToLower(column)]; read != nil {
				// The value the cell is drawn from, exactly, or nothing: a
				// cell that reads back as no value leaves its box for the
				// form, like the dash below.
				value, ok := read(raw)
				if !ok {
					unread = true
					continue
				}
				if value == "" {
					continue
				}
				base[f.Name] = value
				continue
			}
			if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "—" {
				continue
			}
			if v, err := rowKey(f, raw); err == nil {
				base[f.Name] = v
			}
		}
		// A row on the roster is one grant, and the cells it leaves empty —
		// a record drawn "any", a profile or an agent drawn as a dash —
		// mean the grant naming none. Left out of a revoke or a renew, they
		// meant every one: x and n on a row naming no record acted on every
		// grant for its target and agent, and a renewal extended grants the
		// row never pointed at. The target's exact switch makes the row's
		// cells name it alone. Not set when a cell could not be read back:
		// its box is left for the form, and exact would read an empty box
		// as none rather than as the grant the row shows.
		if in := rowNamesOne[a.from]; in != "" && !unread && hasBoolInput(a.cap, in) {
			base[in] = true
		}
	case srcSelf:
		// The page already knows its subject: a seeded key from the line the
		// page shows under that name, the rest from the identity it ran with.
		// The first positional stands in when none is required, as on a row:
		// `lock add` takes its agent or --all, and the agent's own page still
		// names the agent. It seeds what the page knows and requires nothing.
		seedKeys := keys
		if len(seedKeys) == 0 {
			seedKeys = firstPositional(a.cap)
		}
		if kv, ok := m.result.raw.(view.KeyValue); ok {
			for _, f := range seedKeys {
				if key := a.seed[f.Name]; key != "" {
					if raw := pairNamed(kv, key); raw != "" {
						if v, err := rowKey(f, raw); err == nil {
							base[f.Name] = v
						}
					}
				}
			}
		}
		for _, f := range seedKeys {
			if _, done := base[f.Name]; done {
				continue
			}
			if v, ok := m.lastValues[f.Name]; ok {
				base[f.Name] = carried(f, v)
			}
		}
		for _, f := range keys {
			if _, done := base[f.Name]; !done {
				return nil, false
			}
		}
	}
	return base, true
}

// carried is a value the page ran with, in the type of the input it fills on
// the capability a key opens. A note's page takes its id as a number, and the
// verbs on that note take one id or several as text, which is what a row's cell
// already gives them (rowKey): the number would be refused as no list.
func carried(f plugin.Field, v any) any {
	if f.Type != plugin.StringSlice {
		return v
	}
	switch v.(type) {
	case string, []string:
		return v
	}
	return fmt.Sprint(v)
}

func pairNamed(kv view.KeyValue, key string) string {
	for _, p := range kv.Pairs {
		if strings.EqualFold(p.Key, key) {
			return strings.TrimSpace(p.Value)
		}
	}
	return ""
}

// rowKey parses the selected row's identity cell to the key field's type.
// cellNamed returns the row's cell under the column whose header matches an
// input's name, case-insensitively — the convention that lets a producer
// mark which column is which identity without a second declaration.
// columnAlias maps an input to the column that shows it under another
// word: a grant's scope is its "Record" on screen, because that is what a
// person reads it as.
var columnAlias = map[string]string{"scope": "record"}

// cellReader reads a cell drawn for a person back into the value it shows,
// for the columns whose cells are not the value: a grant row's Record says
// "any" for a grant naming no record, "prod/ (all)" for a folder and a
// padded record quoted, and its Profile marks a connection repointed since
// issue. Seeded as drawn, x and n on such a row named a record or a
// connection no grant holds — or a grant on a key literally named any — so
// the revoke or renew they opened took back or extended another grant, or
// none. The readings are internal/grant's, beside the drawing they invert.
//
// Keyed by the capability that drew the table, because each reading is that
// drawing's inverse and nobody else's. A plugin's table may carry a Record
// or a Profile column of its own, where "any" is a record and a dash means
// none; read as the roster, its dash would seed a record named with a dash
// and its "any" no record at all.
var cellReader = map[string]map[string]func(string) (string, bool){
	"grant.list": {
		"record":  grant.RecordOfRoster,
		"profile": grant.ProfileOfRoster,
	},
}

// rowNamesOne is, by the capability that drew the table, the switch of an
// action's target that makes a row's cells name one record exactly — the
// roster's cellReader's companion, keyed the same way and for its reason: a
// row of another capability's table names what its own cells say.
var rowNamesOne = map[string]string{"grant.list": "exact"}

// hasBoolInput reports whether c declares a Bool input named name.
func hasBoolInput(c plugin.Capability, name string) bool {
	for _, f := range c.Inputs {
		if f.Name == name && f.Type == plugin.Bool {
			return true
		}
	}
	return false
}

func cellNamed(tbl view.Table, row []string, name string) (string, bool) {
	if name == "" {
		return "", false
	}
	for i, c := range tbl.Columns {
		if i < len(row) && strings.EqualFold(c.Name, name) {
			return row[i], true
		}
	}
	return "", false
}

func rowKey(f plugin.Field, raw string) (any, error) {
	switch f.Type {
	case plugin.Int:
		return strconv.Atoi(strings.TrimSpace(raw))
	case plugin.Float:
		return strconv.ParseFloat(strings.TrimSpace(raw), 64)
	default:
		return raw, nil
	}
}

// canRerun says whether `r` does anything on this result, for the key and for
// the footer that advertises it, which have to agree.
//
// A result that came back is refreshed by running it again, and a call that
// failed is retried by it. What it is not is a way to do a thing twice: a
// write that is not idempotent and succeeded, `note.add` among them, was
// repeated by a second press of the key the footer calls "refresh" — two
// notes, with nothing asked. Doing it again is `e`, which opens the boxes on
// what the result was made with and runs on a submit, so it is a decision.
//
// Nor is a destructive call that succeeded, idempotent or not. `r` runs on the
// yes given for the first run, and an idempotent DELETE promises that a second
// one changes nothing more, which is the server's promise and not one rta can
// check: the key the footer calls "refresh" would send it again, asking
// nothing. A failed one is still retried on that yes, as any failure is.
func (m Model) canRerun() bool {
	if m.current.Run == nil || (m.result.view == nil && m.result.err == nil) {
		return false
	}
	if m.result.err == nil && m.current.Safety != plugin.Read &&
		(!m.current.Idempotent || m.current.Safety == plugin.Destructive) {
		return false
	}
	return true
}

// resultFooterItems is the result pane's bar: only the keys that apply right
// now. Actionable views lead with their actions; row actions stay hidden until
// there is a row to act on.
//
// The conditions here mirror the switch in Update exactly, and that is the
// whole job. `e` used to be advertised only on a scrolled-past-the-top result
// while the key itself worked at any scroll position — so on the pane where an
// operator most wants to change an input and run it again, nothing said they
// could.
func (m Model) resultFooterItems() []hintItem {
	var keys []hintItem
	rerunnable := m.canRerun()
	scrolls := m.scrolls()
	if m.atTop() {
		// home and end are the first row and the last on a list, where the
		// cursor goes with them, and the ends of the pane on a page that scrolls;
		// aliases of the arrows, which a hand reaches for and a bar need not say.
		switch {
		case m.interactive():
			keys = append(keys, alias(labelled(bindColumn, "row"), "home", "end"))
		case scrolls:
			keys = append(keys, alias(item(bindScroll), "home", "end"))
		}
		for _, a := range capActions(m.reg, m.current.ID) {
			if a.src == srcRow && !m.interactive() {
				continue
			}
			if _, ok := m.follows(a); !ok {
				continue
			}
			keys = append(keys, action(a.key, a.label))
			keys = append(keys, m.answerHints(a)...)
		}
		if m.current.ID == rosterCapability && len(m.aimedElsewhere(m.current)) == 0 {
			keys = append(keys, action("X", "revoke all"))
		}
		for _, t := range m.toggles() {
			// A toggle says which way it is pointing, or half the time it
			// reads as a thing you already did.
			label := t.label
			if m.toggleOn(t) {
				label = theme.GoodText.Render("✓") + " " + label
			}
			keys = append(keys, action(t.key, label))
		}
		if rerunnable {
			keys = append(keys, labelled(bindRerun, "refresh"))
		}
	} else {
		if rerunnable {
			keys = append(keys, item(bindRerun))
		}
		keys = append(keys, alias(item(bindScroll), "home", "end"))
	}
	// Outside the branch: `e` answers at any scroll position, and a pane that
	// only advertises it halfway down is advertising it where nobody is.
	if m.current.Run != nil && hasInputs(m.current) {
		keys = append(keys, item(bindEdit))
	}
	if m.result.view != nil {
		keys = append(keys, item(bindCopy))
	}
	if hint, ok := copyHint(m.current, m.result.view); ok {
		keys = append(keys, hint)
	}
	// Last of the extras, so it is the first the bar gives up: on a narrow
	// terminal `e edit inputs` and `y copy json` were there before it, and the
	// overlay still lists the paging keys when the bar has no room for them.
	if scrolls {
		keys = append(keys, m.pagingHint(keys))
	}
	return append(keys, item(bindBack), item(bindQuit))
}

// toEnd is home and end on a result: the pane to its top or its bottom, and on
// a list the cursor to the first row or the last. The cursor goes with the pane
// because the row an action acts on is the one under it, and a pane sent to the
// bottom with the cursor left at the top acts on a row nobody can see. The render
// keeps the row in view, which on a page with more under its table than the pane
// holds is the one case where the bottom is not where the last row is.
func (m Model) toEnd(last bool) Model {
	tbl, _, _ := rowTable(m.result.raw)
	walked := m.interactive()
	if walked {
		m.row = 0
		if last {
			m.row = max(len(tbl.Rows)-1, 0)
		}
	}
	if last {
		m.viewport.GotoBottom()
	} else {
		m.viewport.GotoTop()
	}
	if walked {
		m.renderResult()
	}
	return m
}

// pageStep is how many rows a pager's key moves the cursor of a list: a page
// or half of one, as the pane under it turns, and false for a key that is not
// one of them. The pane's own bindings say which keys those are, so a list
// answers to exactly the keys a long answer pages on.
func (m Model) pageStep(msg tea.KeyPressMsg) (int, bool) {
	keys, page := m.viewport.KeyMap, max(m.viewport.Height(), 1)
	switch {
	case key.Matches(msg, keys.PageDown):
		return page, true
	case key.Matches(msg, keys.PageUp):
		return -page, true
	case key.Matches(msg, keys.HalfPageDown):
		return max(page/2, 1), true
	case key.Matches(msg, keys.HalfPageUp):
		return -max(page/2, 1), true
	}
	return 0, false
}

// walks says whether a page key or the wheel moves the cursor of a list by step
// (walkRows), where it otherwise moves the pane alone, as on any long answer.
//
// On a list in an answer taller than the pane, which is where the bar teaches
// the keys: one that fits has every row on screen and its pane does not move.
// And only while the cursor's row is on the pane and has rows left that way,
// because a list can be one part of a page — the grant roster draws the guard
// and the roles in force above its table and a note below it, and a page can
// hold more than a pane of text on either side of one. Walked at its first or
// last row, the cursor stayed put and the render pulled the pane back onto it,
// so nothing beyond the table could be paged or wheeled to. Walked from off the
// pane, a step would jump the pane to the row from wherever the person had read
// to. So at either end of the list the keys turn the pane, as they always did,
// and the walk starts again once the cursor's row comes back on screen.
func (m Model) walks(step int) bool {
	if !m.interactive() || !m.scrolls() || !m.rowShown() {
		return false
	}
	tbl, _, _ := rowTable(m.result.raw)
	if step < 0 {
		return m.row > 0
	}
	return m.row < len(tbl.Rows)-1
}

// walkRows moves the cursor of a list by step rows and the pane by as many
// lines as the cursor moved, so a page turned keeps the cursor where it was on
// the screen, on the row that is there now. Both, for toEnd's reason: the pager
// under the pane moved only the pane, and a cursor left above it was the row the
// next action acted on, out of sight (x on the grant roster revoked it) — and
// the next j pulled the pane back to it, so a page could not be turned at all.
// A step cut short by the end of the list moves the pane no further than the
// cursor, which leaves the last row where the cursor was and the next step to
// the pager (walks). The render keeps the row in view where the pane could not
// move as far.
func (m Model) walkRows(step int) Model {
	tbl, _, _ := rowTable(m.result.raw)
	from := m.row
	m.row = min(max(m.row+step, 0), max(len(tbl.Rows)-1, 0))
	m.viewport.SetYOffset(m.viewport.YOffset() + m.row - from)
	m.renderResult()
	return m
}

// scrolls says whether the answer is taller than the pane at its tallest, which
// is the window less the panel's two borders and a bar of one line. Asked of the
// answer and not of the pane's height at the moment, which the bar decides
// (fitResult): a hint that lengthened the bar would shrink the pane, and the
// shorter pane would be what made the hint needed.
func (m Model) scrolls() bool {
	return m.height > 3 && m.viewport.TotalLineCount() > m.height-3
}

// pagingHint is the entry for the pager's keys, without the ones the other
// entries of the screen answer to or one of the view's own actions does. Those
// are handled first (resultKeys), so on a list of notes `d` checks one off and
// does not page.
func (m Model) pagingHint(others []hintItem) hintItem {
	var taken []string
	for _, it := range others {
		taken = append(taken, it.keys...)
	}
	for _, a := range capActions(m.reg, m.current.ID) {
		taken = append(taken, a.key)
	}
	return without(item(bindPageView), taken)
}

// scrollMark is how far down the pane is in an answer taller than it, as the
// percentage of the way, for the top border beside the time. A percentage, and
// not the words "↓ more": it is three or four cells where the words are six, in
// a border that at forty-four columns already holds the capability's id, its
// summary and the time, and it says where in the answer the pane is as well as
// that there is more of it. Nothing for an answer that fits, which has nowhere
// to be.
//
// 0% is the top and 100% the bottom and neither is anything else: a line down
// from the top is 1% however long the answer, and a line up from the bottom 99%,
// where rounding would have said the ends of a long answer when the pane was a
// line from them.
func (m Model) scrollMark() string {
	v := m.viewport
	if v.TotalLineCount() <= v.Height() {
		return ""
	}
	switch {
	case v.AtTop():
		return "0%"
	case v.AtBottom():
		return "100%"
	}
	return strconv.Itoa(min(max(int(v.ScrollPercent()*100), 1), 99)) + "%"
}

// resultView frames the result in a titled panel: identity in the top
// border, cost on the right, contextual keys below.
func (m Model) resultView() string {
	head := capHead(m.current)
	// How far down the answer the pane is, and what the run cost; the ad hoc
	// marker joins them below and outlasts both on a narrow terminal.
	var right []string
	if mark := m.scrollMark(); mark != "" {
		right = append(right, mark)
	}
	if m.result.elapsed > 0 {
		right = append(right, m.result.elapsed.Round(time.Millisecond).String())
	}
	head.Right = strings.Join(right, " · ")
	if where := m.adHocNote(); where != "" {
		head.Right = m.markedRight(head, where, head.Right)
	}

	footer := m.footerFor(modeResult)
	return panel(head, m.viewport.View(), m.width, m.height-lipgloss.Height(footer), false) + "\n" + footer
}

// capTitle renders a capability identity for a panel top border, with the
// same safety glyphs the browse list uses.
func capHead(c plugin.Capability) panelHead {
	id := c.ID
	switch c.Safety {
	case plugin.Write:
		id += " ✎"
	case plugin.Destructive:
		id += " ⚠"
	}
	return panelHead{Title: id, Note: c.Summary}
}
