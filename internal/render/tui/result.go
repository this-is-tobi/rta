package tui

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	// err is the capability's own, never cleaned here: the one thing that
	// draws it is cli.RenderError, which cleans its own copy.
	err *view.Error
	// seq identifies the run this result belongs to. A result from a run the
	// user walked away from must not appear over whatever they walked to.
	seq int
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
	// Fill: this is a bordered pane of a fixed size, so a table narrower than
	// the frame is not restraint, it is a gap where a title could have been.
	opts := cli.Options{Format: cli.Pretty, Width: max(m.width-4, 20), Fill: true}
	if m.interactive() {
		opts.Highlight = m.row + 1
	}
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
		// Keep the selected row in view: meta(2) + table chrome(2) + row.
		line := m.row + 4
		top, h := m.viewport.YOffset(), m.viewport.Height()
		if line < top {
			m.viewport.SetYOffset(line)
		} else if line >= top+h-1 {
			m.viewport.SetYOffset(line - h + 2)
		}
	}
}

// resultMeta is the context line under the panel title: safety class in the
// shared status colors, idempotency, and the shape of what came back. It
// carries real information and keeps sparse results from floating in space.
func (m Model) resultMeta() string {
	if m.result.err != nil {
		return ""
	}
	sep := theme.Subtle.Render(" · ")
	parts := []string{theme.StatusStyle(string(m.current.Safety)).Render(string(m.current.Safety))}
	if m.current.Idempotent {
		parts = append(parts, theme.Subtle.Render("idempotent"))
	}
	switch v := m.result.view.(type) {
	case view.Table:
		n := len(v.Rows)
		total := max(v.Total, n)
		parts = append(parts, theme.Subtle.Render(fmt.Sprintf("%d of %s", n, format.CountOf(total, "row"))))
		if m.interactive() && n > 0 {
			parts = append(parts, theme.Subtle.Render(fmt.Sprintf("row %d/%d", m.row+1, n)))
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
		parts = append(parts, theme.Subtle.Render(strings.Join(titles, " › ")))
		// The title list is the page's table of contents, and a page that
		// lost three of its sections lists the survivors exactly as
		// confidently as a whole one does. This line is above the fold, so
		// it is where "you are not looking at all of it" has to be said.
		// Which parts and why is the renderer's job: the pane draws through
		// cli.Render, which already prints the warnings under the sections,
		// and a second copy here drew every one of them twice — in a
		// narrower block that truncated the messages the first copy wrapped.
		if n := len(pageWarnings(v)); n > 0 {
			parts = append(parts, theme.WarnText.Render(
				fmt.Sprintf("⚠ partial (%d %s)", n, format.PluralOf(n, "warning"))))
		}
	}
	return " " + strings.Join(parts, sep)
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
	}
	return msg.cap.ID + " done"
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
	base, ok := m.actionSeed(a, tbl)
	if !ok {
		return m, nil
	}
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
	// agent.allow/agent.deny from agent.pending. The `profile` case just
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
	prev := m.continuing(cap)
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
	if !a.bare && len(fieldsAfter(cap, base)) > 0 {
		return m.startFormWith(cap, base, prev)
	}
	if cap.Safety == plugin.Destructive {
		return m, m.startConfirm(cap, base)
	}
	m.lastValues, m.lastYes = base, false
	return m, m.startRun(cap, base, false)
}

// hereOnly drops the inputs that point a call at another machine, and the
// ones only read beside them, from a form an action opens: the queue under
// the cursor is this machine's, so a box for the server it might instead be
// parked on is a box for a different call. Typing the capability's name in
// the catalogue still offers every input.
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
		for _, f := range a.cap.Inputs {
			if _, done := base[f.Name]; done || f.Local || (f.Type != plugin.String && f.Type != plugin.Int) {
				continue
			}
			raw, found := cellNamed(tbl, row, f.Name)
			if !found {
				raw, found = cellNamed(tbl, row, columnAlias[f.Name])
			}
			if !found || strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "—" {
				continue
			}
			if v, err := rowKey(f, raw); err == nil {
				base[f.Name] = v
			}
		}
	case srcSelf:
		// The page already knows its subject: a seeded key from the line the
		// page shows under that name, the rest from the identity it ran with.
		if kv, ok := m.result.raw.(view.KeyValue); ok {
			for _, f := range keys {
				if key := a.seed[f.Name]; key != "" {
					if raw := pairNamed(kv, key); raw != "" {
						if v, err := rowKey(f, raw); err == nil {
							base[f.Name] = v
						}
					}
				}
			}
		}
		for _, f := range keys {
			if _, done := base[f.Name]; done {
				continue
			}
			if v, ok := m.lastValues[f.Name]; ok {
				base[f.Name] = v
			}
		}
		if len(base) != len(keys) {
			return nil, false
		}
	}
	return base, true
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
	rerunnable := m.current.Run != nil && (m.result.view != nil || m.result.err != nil)
	if m.atTop() {
		if m.interactive() {
			keys = append(keys, labelled(bindColumn, "row"))
		}
		for _, a := range capActions(m.reg, m.current.ID) {
			if a.src == srcRow && !m.interactive() {
				continue
			}
			keys = append(keys, action(a.key, a.label))
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
		keys = append(keys, item(bindScroll))
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
	return append(keys, item(bindBack), item(bindQuit))
}

// resultView frames the result in a titled panel: identity in the top
// border, cost on the right, contextual keys below.
func (m Model) resultView() string {
	head := capHead(m.current)
	if m.result.elapsed > 0 {
		head.Right = m.result.elapsed.Round(time.Millisecond).String()
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
