// Package tui is the interactive shell over the capability registry: one app
// that hosts every plugin's views. Capabilities never own the screen — the
// shell browses the registry, runs capabilities, and renders their Views
// (PROJECT.md §5.2).
//
// v0 scope: filterable capability browser (the proto command palette),
// direct execution of capabilities without required inputs, results in a
// scrollable pane with re-run and copy-as-JSON. Forms for required inputs
// (huh) and the dashboard land in the next M1 iteration.
package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
	"github.com/this-is-tobi/rule-them-all/internal/stdio"
	"github.com/this-is-tobi/rule-them-all/internal/textclean"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// runTimeout bounds a single capability execution inside the TUI.
const runTimeout = 30 * time.Second

type mode int

const (
	modeDashboard mode = iota
	modeBrowse
	modeForm
	modeRunning
	modeResult
	modePlugins
	modeTheme
	modeCopyPick
)

// capItem adapts a capability to the bubbles list.
type capItem struct{ c plugin.Capability }

func (i capItem) FilterValue() string { return i.c.ID + " " + i.c.Summary }

// resultMsg carries a finished capability run back into the update loop.
// Rendering happens at paint time so results adapt to the current width.
type resultMsg struct {
	cap     plugin.Capability
	view    view.View // for copy-as-JSON and re-rendering; nil on error
	elapsed time.Duration
	err     *view.Error
	// seq identifies the run this result belongs to. A result from a run the
	// user walked away from must not appear over whatever they walked to.
	seq int
}

// runRef remembers one actionable view — a list, or the page of a single
// record — so actions launched from it can hop out (show/edit/done/remove)
// and land back on it, refreshed.
type runRef struct {
	cap    plugin.Capability
	values map[string]any
}

// Model is the TUI shell.
type Model struct {
	reg *registry.Registry
	// pluginCfg answers what the operator stated for a namespace, already
	// matched to the artifact by internal/pluginconf. nil means nothing is
	// configured, which is the ordinary state.
	pluginCfg func(namespace string) map[string]any
	list      list.Model
	// cols carries the catalogue column widths, measured once so the
	// header above the list and the rows inside it agree.
	cols       capDelegate
	viewport   viewport.Model
	spinner    spinner.Model
	form       *capForm
	themeForm  *themeForm
	copyPick   *copyPickForm
	tiles      []tile
	dash       config.Dashboard // the arrangement, edited in place and saved
	selected   int              // selected dashboard tile
	scroll     int              // first visible dashboard tile row
	origin     mode             // where the current result/form was opened from
	mode       mode
	current    plugin.Capability // capability being viewed/run
	lastValues map[string]any    // inputs of the last run, reused by re-run
	lastYes    bool
	result     resultMsg
	flash      string // one-shot footer notice (e.g. "copied"), cleared on next key
	width      int
	height     int

	// Live search bar state (dashboard tile 0).
	searchEditing bool
	query         string
	searchSel     int
	searchInfo    string // idle prompt: plugin/capability inventory

	// Plugins pane: the inventory, and where a hidden tile comes back from.
	plugins   []pluginRow
	pluginSel int
	// pluginScroll is the first plugin drawn. The pane used to clip instead
	// of scroll, so at 80x24 the last plugin was invisible while `j` still
	// selected it.
	pluginScroll int

	// In-flight run: its cancel func and the sequence number that tells its
	// result apart from one the user has already walked away from.
	cancelRun context.CancelFunc
	runSeq    int

	// tickGen names the current dashboard refresh chain. Every return to the
	// dashboard restarts refreshing (a tile can be stale after any amount of
	// time away) — which, without a generation to check, meant every return
	// left its predecessor's tea.Tick armed and about to re-arm itself
	// forever: nothing about `tickMsg{}` said which visit it belonged to, so
	// a stale chain looked exactly as valid as the current one. Ten trips
	// through browse and back left ten timers alive, each firing every tile
	// on every tick. Bumped by every call that (re)starts refreshing;
	// checked by the tick handler, which drops anything but the newest.
	tickGen int

	// Actionable-view state. trail is the chain of views the user walked
	// into (todo.list → todo.show); actions run against its last entry and
	// esc pops back up it.
	trail          []runRef
	row            int  // selected table row of the current list
	goalCol        int  // dashboard column vertical movement aims for
	refreshPending bool // a mutating action ran: reload the view it came from
	subjectGone    bool // …and that action destroyed that view's subject
}

// New builds the shell over a registry. dash configures the dashboard; its
// zero value is the automatic one-tile-per-plugin arrangement.
// New builds the shell. pluginCfg answers what the operator stated for a
// namespace, already matched to the artifact by internal/pluginconf; nil is a
// decision the caller has to type, which is the point.
//
// A parameter rather than a setter, for the reason ADR 0016 gives for
// plugin.Resolve's third argument: Run used to take this and New could not,
// so the value had nowhere to go and was dropped on the floor. Every surface
// that reads it — the form seed, the run, the dashboard refresh — then saw
// nil, and the operator's configuration reached the CLI and no part of the
// TUI. A constructor that can still be called the old way is the same defect
// waiting to be reintroduced.
func New(reg *registry.Registry, dash config.Dashboard,
	pluginCfg func(namespace string) map[string]any) Model {
	items := catalogueItems(reg)
	cols := newCapDelegate(items)
	l := list.New(items, cols, 0, 0)
	// Strip the list's own chrome: its help line speaks a different
	// vocabulary from the rest of the app ("↑/k up", "/ filter"), and its
	// title and status bar put four lines between the column header and the
	// first row. browseView draws all three in the app's own terms.
	// Filtering is untouched — the filter input renders on its own row
	// whether or not the title does (bubbles list.titleView).
	l.SetShowHelp(false)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetStatusBarItemName("capability", "capabilities")
	// The first item is a section header, and the cursor must not start on a
	// label: enter would do nothing and the list would look broken.
	for i, item := range l.Items() {
		if _, isHeader := item.(pluginHeader); !isHeader {
			l.Select(i)
			break
		}
	}

	sp := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(theme.Primary)),
	)
	return Model{
		reg:       reg,
		pluginCfg: pluginCfg,
		list:      l,
		cols:      cols,
		viewport:  viewport.New(),
		spinner:   sp,
		tiles:     buildTiles(reg, dash),
		dash:      dash,
		mode:      modeDashboard,
		searchInfo: fmt.Sprintf("%d plugins · %d capabilities — press / to search",
			len(reg.Plugins()), len(reg.Capabilities())),
	}
}

// wheelStep is how many rows one notch of the wheel moves. Three is what
// every terminal pager uses, and a list that moves one row per notch reads
// as a list that is resisting.
const wheelStep = 3

func (m Model) Init() tea.Cmd { return refreshTiles(m.tiles, m.tickGen, m.pluginCfg) }

// formSeed is what a form opens showing: declared defaults, the operator's
// configuration over them, and whatever the caller already had on top.
//
// plugin.Resolve rather than a second precedence rule written out here — it
// is the same three-layer question every surface asks, and two implementations
// of it would disagree on the day one of them was corrected. Seeding rather
// than substituting at submit time is also the better screen: an operator who
// stated a host sees db.internal in the box and can edit it, instead of an
// empty box that silently fills in with something else.
func (m Model) formSeed(c plugin.Capability, defaults map[string]any) map[string]any {
	cfg := m.configFor(c)
	if cfg == nil {
		return defaults
	}
	return plugin.Resolve(c, defaults, cfg)
}

// configFor is the operator's stated values for the plugin a capability
// belongs to, by namespace off the ID — which the registry guarantees is the
// plugin that declared it.
func (m Model) configFor(c plugin.Capability) map[string]any {
	if m.pluginCfg == nil {
		return nil
	}
	words := c.Words()
	if len(words) == 0 {
		return nil
	}
	return m.pluginCfg(words[0])
}

// runCmd executes a capability off the update loop. yes reflects an explicit
// in-TUI confirmation for destructive capabilities. The result pane owns the
// whole screen, so detail-capable capabilities are asked for their full view
// unless somebody asked for the other one.
func runCmd(ctx context.Context, seq int, c plugin.Capability, values map[string]any, yes bool, cfg map[string]any) tea.Cmd {
	return func() tea.Msg {
		// Resolve rather than "fill defaults only when nothing was given":
		// a caller who supplies one value must not lose the other defaults.
		values = plugin.Resolve(c, values, cfg)
		// A default, not an override. Forcing detail on unconditionally made
		// the D toggle on kv.list dead: toggleView set detail=false, this put
		// it back to true one line later, and the handler only ever saw true.
		// The footer checkmark flipped on every press and the pane below it
		// re-rendered the identical detailed page.
		if _, given := values["detail"]; c.Detailed && !given {
			// Copy: tile values are reused by refreshes, which stay compact.
			full := make(map[string]any, len(values)+1)
			for k, v := range values {
				full[k] = v
			}
			full["detail"] = true
			values = full
		}
		start := time.Now()
		v, err := c.Run(ctx, plugin.NewRequest(values, false, yes).WithSurface(plugin.SurfaceTUI))
		elapsed := time.Since(start)
		if err != nil {
			return resultMsg{cap: c, elapsed: elapsed, err: view.AsError(err, c.ID+".failed"), seq: seq}
		}
		return resultMsg{cap: c, view: v, elapsed: elapsed, seq: seq}
	}
}

// startRun launches a capability and puts the shell in its running state.
//
// The run gets a cancellable context and a sequence number, which is what
// makes "esc" mean something while it is in flight: a traceroute is thirty
// hops of two seconds, and a shell you cannot leave until it finishes is a
// shell that has taken your terminal hostage. Cancelling bumps the sequence,
// so the result that eventually arrives is recognised as belonging to a run
// nobody is waiting for any more, and is dropped instead of painted over
// whatever the user moved on to.
func (m *Model) startRun(c plugin.Capability, values map[string]any, yes bool) tea.Cmd {
	if m.cancelRun != nil {
		m.cancelRun()
	}
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	m.cancelRun = cancel
	m.runSeq++
	m.mode = modeRunning
	return tea.Batch(m.spinner.Tick, runCmd(ctx, m.runSeq, c, values, yes, m.configFor(c)))
}

// isTop reports whether c is the actionable view the trail points at.
func (m Model) isTop(c plugin.Capability) bool {
	return len(m.trail) > 0 && m.trail[len(m.trail)-1].cap.ID == c.ID
}

// atTop reports whether the result on screen is the actionable view the
// trail currently points at — as opposed to a leaf result opened from it.
func (m Model) atTop() bool { return m.isTop(m.current) }

// interactive reports whether the current result is a row-navigable list.
// An actionable view with no rows is still actionable (you can add to an
// empty list) — it just has no row to select.
func (m Model) interactive() bool {
	if !m.atTop() {
		return false
	}
	tbl, ok := m.result.view.(view.Table)
	return ok && len(tbl.Rows) > 0
}

// enterTrail records the result on screen when it is a view you can act
// from: refreshing the current level, returning to an earlier one, or
// descending into a new one (a list into one record's page).
func (m *Model) enterTrail(c plugin.Capability, values map[string]any) {
	if len(capActions(m.reg, c.ID)) == 0 {
		return // a leaf result: esc simply returns to where it came from
	}
	for i := range m.trail {
		if m.trail[i].cap.ID == c.ID {
			m.trail = m.trail[:i+1]
			m.trail[i].values = values
			return
		}
	}
	m.trail = append(m.trail, runRef{cap: c, values: values})
}

// reopenTop re-runs the actionable view the trail points at, so it reflects
// whatever the action that just ran changed.
func (m Model) reopenTop() (tea.Model, tea.Cmd) {
	t := m.trail[len(m.trail)-1]
	m.current = t.cap
	m.lastValues, m.lastYes = t.values, false
	return m, m.startRun(t.cap, t.values, false)
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
		parts = append(parts, theme.Subtle.Render(fmt.Sprintf("%d of %d rows", n, total)))
		if m.interactive() && n > 0 {
			parts = append(parts, theme.Subtle.Render(fmt.Sprintf("row %d/%d", m.row+1, n)))
		}
	case view.KeyValue:
		parts = append(parts, theme.Subtle.Render(fmt.Sprintf("%d fields", len(v.Pairs))))
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
				fmt.Sprintf("⚠ partial (%d %s)", n, pluralNoun(n, "warning"))))
		}
	}
	return " " + strings.Join(parts, sep)
}

// pluralNoun picks the noun form for n, and returns only the noun — the
// count is already in the caller's format string. Named apart from
// builtin/audit's plural, which returns "2 advisories" with the number
// folded in: two helpers, one name and two contracts is how "2 2 warnings"
// gets written by somebody reading the wrong one.
func pluralNoun(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// pageWarnings collects what a composite page could not produce, recursing
// into nested pages: a detail view is sections of sections, so a sensor that
// failed two levels down is exactly as absent as one that failed at the top,
// and just as worth saying out loud.
func pageWarnings(v view.View) []view.Error {
	s, ok := v.(view.Sections)
	if !ok {
		return nil
	}
	out := append([]view.Error(nil), s.Warnings...)
	for _, item := range s.Items {
		out = append(out, pageWarnings(item.View)...)
	}
	return out
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
	case m.result.view != nil:
		if err := cli.Render(&buf, m.result.view, opts); err != nil {
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

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// The catalogue draws its own column header above the list and the
		// shared hint bar below it, so the list gets what is left rather
		// than the whole window — otherwise its last rows land under the
		// footer and the scroll position lies about what is reachable.
		m.list.SetSize(msg.Width, max(msg.Height-1-lipgloss.Height(m.browseFooter()), 3))
		m.viewport.SetWidth(msg.Width - 4)   // result panel: borders + padding
		m.viewport.SetHeight(msg.Height - 3) // panel top/bottom + footer
		m.clampScroll()                      // dashboard rows per screen changed
		if m.mode == modeResult {
			m.renderResult() // reflow to the new width
		}
		// A form open across a resize has to be re-fitted too, or it keeps the
		// height of a window that no longer exists.
		m.fitForm()
		m.fitThemeForm()
		m.fitCopyPick()
		return m, nil

	case tea.MouseWheelMsg:
		// The wheel scrolls whatever is under it. People try it before they
		// look for a key, and a pane that ignores it reads as stuck. Every
		// mode that sets MouseMode in View needs a case here, or it has
		// claimed the events precisely so it can throw them away.
		switch m.mode {
		case modeDashboard:
			switch msg.Button {
			case tea.MouseWheelUp:
				m.scroll = max(m.scroll-1, 0)
			case tea.MouseWheelDown:
				m.scroll = min(m.scroll+1, max(0, m.dashRows()-m.dashRowsVisible()))
			}
			return m, nil
		case modeResult:
			switch msg.Button {
			case tea.MouseWheelUp:
				m.viewport.ScrollUp(3)
			case tea.MouseWheelDown:
				m.viewport.ScrollDown(3)
			}
			return m, nil
		case modeBrowse:
			// The catalogue was the one pane that turned mouse reporting on
			// and then had no case here, which is the worst of both: the
			// terminal hands the wheel to us because we asked for it, we drop
			// it, and the terminal will not scroll its own scrollback either
			// because we took the events. Eighty-five rows, four pages, and a
			// wheel that does nothing at all.
			for range wheelStep {
				switch msg.Button {
				case tea.MouseWheelUp:
					m.list.CursorUp()
				case tea.MouseWheelDown:
					m.list.CursorDown()
				}
			}
			m.settleCursor(msg.Button == tea.MouseWheelDown)
			return m, nil
		case modePlugins:
			switch msg.Button {
			case tea.MouseWheelUp:
				m.pluginSel = max(m.pluginSel-1, 0)
			case tea.MouseWheelDown:
				m.pluginSel = min(m.pluginSel+1, max(len(m.plugins)-1, 0))
			}
			m.clampPluginScroll(m.pluginBodyHeight())
			return m, nil
		}
		return m, nil

	case spinner.TickMsg:
		if m.mode == modeRunning {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case tileMsg:
		if idx := m.tileIndexFor(msg); idx >= 0 {
			// Cleaned on arrival, for the same reason resultMsg is: what the
			// model stores and what the screen shows must be one string.
			//
			// Nothing today reads a tile's view except cli.Render, which
			// sanitises its own local copy — so this was already safe, and
			// safe only because no second reader exists. That is exactly the
			// arrangement that produced the runAction bug, where the cell on
			// screen came from the sanitised copy and the row's identity came
			// from the raw one. A tile is a view with a dashboard action
			// attached; the second reader is a matter of time.
			m.tiles[idx].view = view.MapStrings(msg.v, textclean.Terminal)
			m.tiles[idx].err = view.MapErrorStrings(msg.err, textclean.Terminal)
		}
		return m, nil

	case tea.MouseClickMsg:
		if m.mode == modeDashboard && msg.Button == tea.MouseLeft {
			if idx := m.tileAt(msg.X, msg.Y); idx >= 0 {
				m.selected = idx
				return m.openTile(idx)
			}
		}
		return m, nil

	case tickMsg:
		// Refresh only while the dashboard is visible, and only the chain
		// currently in force — anything else is a return trip's predecessor,
		// left ticking after a later visit already restarted it, and letting
		// it re-arm here is exactly how the count of live timers used to
		// grow with every trip through browse and back.
		if m.mode == modeDashboard && msg.gen == m.tickGen {
			m.tickGen++
			return m, refreshTiles(m.tiles, m.tickGen, m.pluginCfg)
		}
		return m, nil

	case resultMsg:
		// A result the user walked away from must not paint over what they
		// walked to.
		if msg.seq != 0 && msg.seq != m.runSeq {
			return m, nil
		}
		// Cleaned once, at the top, rather than at each place a string is
		// drawn — and above every branch below, because the flash path returns
		// early and would otherwise draw a raw one-liner.
		//
		// cli.Render sanitises its own local copy, so everything the TUI draws
		// *around* that copy was raw: resultMeta prepends Sections item titles
		// in rta's own styling, and a title carrying an OSC 8 hyperlink went to
		// the terminal verbatim — a live link with attacker-chosen text and
		// target, inside rta's panel border, in rta's voice.
		//
		// Worse than a display bug: runAction takes a row's identity from
		// m.result.view while the cell on screen came from the sanitised copy,
		// so what was shown and what was acted on were different strings by
		// construction. Cleaning at ingress makes them the same string instead
		// of making two readers agree, which is the version that stays true
		// when a third is added.
		msg.view = view.MapStrings(msg.view, textclean.Terminal)
		msg.err = view.MapErrorStrings(msg.err, textclean.Terminal)
		// A mutating action finished cleanly: flash its outcome and return
		// to the view it was launched from, reloaded. If it destroyed that
		// view's subject (removing the very task whose page we were on),
		// come back one level further instead of reloading a record that no
		// longer exists.
		if m.refreshPending && msg.err == nil && !m.isTop(msg.cap) {
			m.refreshPending = false
			m.flash = flashText(msg)
			if m.subjectGone {
				m.subjectGone = false
				m.trail = m.trail[:len(m.trail)-1]
			}
			if len(m.trail) > 0 {
				return m.reopenTop()
			}
			return m.closeToOrigin()
		}
		m.refreshPending, m.subjectGone = false, false
		m.mode = modeResult
		m.current = msg.cap
		m.result = msg
		m.enterTrail(msg.cap, m.lastValues)
		if tbl, ok := msg.view.(view.Table); ok {
			m.row = min(m.row, max(len(tbl.Rows)-1, 0))
		}
		m.renderResult()
		m.viewport.GotoTop()
		return m, nil

	case tea.KeyPressMsg:
		m.flash = ""
		switch m.mode {
		case modeDashboard:
			if m.searchEditing {
				return m.updateSearch(msg)
			}
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "b", ":":
				m.mode = modeBrowse
				return m, nil
			case "/":
				// Focus the live search bar.
				m.selected = 0
				m.searchEditing = true
				m.clampScroll()
				return m, nil
			case "left", "h":
				m.moveSelection(-1, 0)
			case "right", "l", "tab":
				m.moveSelection(1, 0)
			case "up", "k":
				m.moveSelection(0, -1)
			case "down", "j":
				m.moveSelection(0, 1)
			case "enter":
				return m.openTile(m.selected)
			case "H":
				// Curating the dashboard belongs on the dashboard, not in a
				// text editor: the arrangement is a visual thing.
				m.flash = m.hideSelected()
				return m, nil
			case "p":
				// What is installed, and what it puts on the dashboard —
				// including the way back for anything H took off.
				m.plugins = pluginRows(m.reg, m.dash)
				m.pluginSel, m.pluginScroll = 0, 0
				m.mode = modePlugins
				return m, nil
			case "t":
				return m.startThemeForm()
			case "[", "<":
				m.flash = m.moveSelected(-1)
				return m, nil
			case "]", ">":
				m.flash = m.moveSelected(1)
				return m, nil
			case "c":
				// A tile's own copySpecs value, straight off its current
				// preview — the same "c" a result already open answers to
				// (below), reached here without opening the tile first.
				if m.selected > 0 && m.selected < len(m.tiles) {
					t := m.tiles[m.selected]
					if spec, ok := copySpecs[t.cap.ID]; ok {
						return m.copyOrPick(spec, t.cap, t.view, modeDashboard)
					}
				}
				return m, nil
			default:
				// Selected-tile actions: one key opens a sibling capability
				// (e.g. `a` on the todo tile opens the add form).
				if a, ok := m.selectedAction(msg.String()); ok {
					m.origin = modeDashboard
					m.trail = nil
					return m.open(a.cap)
				}
			}
			return m, nil
		case modePlugins:
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "esc", "p":
				m.mode = modeDashboard
				m.tickGen++
				return m, refreshTiles(m.tiles, m.tickGen, m.pluginCfg)
			case "up", "k":
				m.pluginSel = max(m.pluginSel-1, 0)
				m.clampPluginScroll(m.pluginBodyHeight())
				return m, nil
			case "down", "j":
				m.pluginSel = min(m.pluginSel+1, len(m.plugins)-1)
				m.clampPluginScroll(m.pluginBodyHeight())
				return m, nil
			case " ", "space", "x", "enter":
				if msg.String() != "enter" {
					m.flash = m.toggleShown(m.pluginSel)
					return m, nil
				}
				// Enter is about the capabilities rather than the tile: drop
				// the namespace into the search bar, which is where every one
				// of them is a keystroke away.
				if m.pluginSel < len(m.plugins) {
					m.mode = modeDashboard
					m.selected = 0
					m.searchEditing = true
					m.query = m.plugins[m.pluginSel].plugin.Name + "."
					m.searchSel = 0
					m.clampScroll()
				}
				m.tickGen++
				return m, refreshTiles(m.tiles, m.tickGen, m.pluginCfg)
			case "c":
				if m.pluginSel < len(m.plugins) {
					return m.startConfigForm(m.plugins[m.pluginSel])
				}
				return m, nil
			}
			return m, nil
		case modeBrowse:
			// While filtering, every key belongs to the filter input.
			if m.list.FilterState() == list.Filtering {
				break
			}
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "esc":
				// Back to the dashboard; restart its refresh loop.
				if m.list.FilterState() == list.Unfiltered {
					m.mode = modeDashboard
					m.tickGen++
					return m, refreshTiles(m.tiles, m.tickGen, m.pluginCfg)
				}
			case "enter":
				if item, ok := m.list.SelectedItem().(capItem); ok {
					m.origin = modeBrowse
					return m.open(item.c)
				}
			}
		case modeForm:
			switch msg.String() {
			case "esc":
				return m.closeForm()
			case "ctrl+c":
				return m, tea.Quit
			case "shift+enter", "alt+enter":
				return m.fastSubmitForm()
			}
		case modeTheme:
			switch msg.String() {
			case "esc":
				m.themeForm = nil
				m.mode = modeDashboard
				return m, nil
			case "ctrl+c":
				return m, tea.Quit
			case "shift+enter", "alt+enter":
				return m.fastSubmitThemeForm()
			}
		case modeCopyPick:
			switch msg.String() {
			case "esc":
				return m.closeCopyPick()
			case "ctrl+c":
				return m, tea.Quit
			case "shift+enter", "alt+enter":
				return m.fastSubmitCopyPick()
			}
		case modeResult:
			// An actionable view: rows navigable when there are rows, and
			// its actions one key away either way — adding to an empty list
			// must not require finding a non-empty one first.
			if m.atTop() {
				tbl, _ := m.result.view.(view.Table)
				if m.interactive() {
					switch msg.String() {
					case "up", "k":
						m.row = max(m.row-1, 0)
						m.renderResult()
						return m, nil
					case "down", "j":
						m.row = min(m.row+1, max(len(tbl.Rows)-1, 0))
						m.renderResult()
						return m, nil
					}
				}
				for _, a := range capActions(m.reg, m.current.ID) {
					if a.key == msg.String() {
						return m.runAction(a, tbl)
					}
				}
				if t, ok := toggleFor(m.current.ID, msg.String()); ok {
					return m.toggleView(t)
				}
			}
			switch msg.String() {
			case "q":
				return m, tea.Quit
			case "esc":
				if !m.atTop() && len(m.trail) > 0 {
					// A leaf result: back to the view it was opened from.
					return m.reopenTop()
				}
				if len(m.trail) > 1 {
					m.trail = m.trail[:len(m.trail)-1]
					return m.reopenTop()
				}
				m.trail = nil
				return m.closeToOrigin()
			case "r":
				if m.current.Run != nil && (m.result.view != nil || m.result.err != nil) {
					// Re-run with the same inputs; destructive confirmation
					// carries over from the original explicit approval.
					return m, m.startRun(m.current, m.lastValues, m.lastYes)
				}
			case "e":
				// Edit inputs and run again, keeping the view toggles the
				// form has no field for.
				if m.current.Run != nil && hasInputs(m.current) {
					return m.startForm(m.current, unasked(m.current, m.lastValues))
				}
			case "y":
				if m.result.view != nil {
					// Redact, like every other path that turns a view into
					// bytes somebody can read. This one wrote the raw view,
					// so a field the screen was masking went to the system
					// clipboard in full — and a clipboard is read by more
					// things than a terminal is, and outlives the session.
					raw, err := json.MarshalIndent(
						view.Envelope{View: view.Redact(m.result.view)}, "", "  ")
					if err == nil {
						m.flash = "copied as JSON"
						return m, tea.SetClipboard(string(raw))
					}
				}
			case "c":
				// Unlike "y", this is not available everywhere — only a
				// capability named in copySpecs, with a result shaped the way
				// it declares, has a value to copy at all.
				if spec, ok := copySpecs[m.current.ID]; ok {
					return m.copyOrPick(spec, m.current, m.result.view, modeResult)
				}
			case "ctrl+c":
				return m, tea.Quit
			}
		case modeRunning:
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc", "q":
				// Leaving a slow run has to be possible without leaving the
				// app: a traceroute is thirty hops of two seconds.
				if m.cancelRun != nil {
					m.cancelRun()
					m.cancelRun = nil
				}
				m.runSeq++ // whatever arrives now belongs to nobody
				m.refreshPending, m.subjectGone = false, false
				m.flash = "cancelled"
				if len(m.trail) > 0 {
					return m.reopenTop()
				}
				return m.closeToOrigin()
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	switch m.mode {
	case modeBrowse:
		before := m.list.Index()
		m.list, cmd = m.list.Update(msg)
		// Put the cursor somewhere enter can act on: not a section label, and
		// not past the end of a list a filter just shortened.
		m.settleCursor(m.list.Index() >= before)
	case modeForm:
		return m.updateForm(msg)
	case modeTheme:
		return m.updateThemeForm(msg)
	case modeCopyPick:
		return m.updateCopyPick(msg)
	case modeResult:
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}

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

// closeToOrigin returns to wherever the current form/result was opened from,
// restarting the tile refresh loop when that is the dashboard.
func (m Model) closeToOrigin() (tea.Model, tea.Cmd) {
	m.mode = m.origin
	if m.origin == modeDashboard {
		m.tickGen++
		return m, refreshTiles(m.tiles, m.tickGen, m.pluginCfg)
	}
	return m, nil
}

// closeForm dismisses the current form: back to the actionable view it was
// opened from, or to the origin.
func (m Model) closeForm() (tea.Model, tea.Cmd) {
	m.form = nil
	m.refreshPending, m.subjectGone = false, false
	if len(m.trail) > 0 {
		return m.reopenTop()
	}
	return m.closeToOrigin()
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

// updateSearch handles keys while the live search bar is focused: printable
// input builds the query, matches update on every keystroke, enter opens the
// selected match.
func (m Model) updateSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.searchEditing = false
		m.query = ""
		m.searchSel = 0
		return m, nil
	case "enter":
		results := m.searchResults()
		if len(results) == 0 {
			return m, nil
		}
		c := results[min(m.searchSel, len(results)-1)]
		m.searchEditing = false
		m.origin = modeDashboard
		m.trail = nil
		return m.open(c)
	case "up", "ctrl+p":
		m.searchSel = max(m.searchSel-1, 0)
		return m, nil
	case "down", "ctrl+n", "tab":
		// Past the third match the window scrolls rather than stopping: the
		// bar shows three lines, not three results.
		if n := len(m.searchResults()); n > 0 {
			m.searchSel = min(m.searchSel+1, n-1)
		}
		return m, nil
	case "pgup":
		m.searchSel = max(m.searchSel-searchMatches, 0)
		return m, nil
	case "pgdown":
		if n := len(m.searchResults()); n > 0 {
			m.searchSel = min(m.searchSel+searchMatches, n-1)
		}
		return m, nil
	case "backspace":
		if m.query == "" {
			m.searchEditing = false
			return m, nil
		}
		r := []rune(m.query)
		m.query = string(r[:len(r)-1])
		m.searchSel = 0
		return m, nil
	default:
		if msg.Text != "" {
			m.query += msg.Text
			m.searchSel = 0
		}
		return m, nil
	}
}

// openTile opens the selected dashboard tile: the search bar takes focus,
// capability tiles open their full result with the tile's own inputs.
func (m Model) openTile(idx int) (tea.Model, tea.Cmd) {
	if idx < 0 || idx >= len(m.tiles) {
		return m, nil
	}
	t := m.tiles[idx]
	if t.search {
		m.searchEditing = true
		return m, nil
	}
	m.origin = modeDashboard
	// A tile is Read by construction (buildTiles refuses anything else), so
	// this is the fast path rather than the gate. Anything that is not gets
	// the same form-and-confirm every other surface gives it.
	if t.cap.Safety != plugin.Read {
		return m.open(t.cap)
	}
	m.current = t.cap
	m.lastValues, m.lastYes = t.values, false
	m.trail = nil
	m.row = 0
	return m, m.startRun(t.cap, t.values, false)
}

// open decides what Enter does for a capability: form when there is anything
// to ask (inputs or a destructive confirmation), direct run otherwise.
func (m Model) open(c plugin.Capability) (tea.Model, tea.Cmd) {
	m.current = c
	m.trail = nil
	m.row = 0
	m.refreshPending = false
	if hasInputs(c) || c.Safety == plugin.Destructive {
		return m.startForm(c, nil)
	}
	m.lastValues, m.lastYes = nil, false
	return m, m.startRun(c, nil, false)
}

// flashText condenses an action result into a one-line footer notice.
func flashText(msg resultMsg) string {
	if t, ok := msg.view.(view.Text); ok {
		return t.Body
	}
	return msg.cap.ID + " done"
}

// fieldsAfter returns the capability inputs not already covered by base.
func fieldsAfter(c plugin.Capability, base map[string]any) []plugin.Field {
	var out []plugin.Field
	for _, f := range c.Inputs {
		if _, ok := base[f.Name]; !ok {
			out = append(out, f)
		}
	}
	return out
}

// rowKey parses the selected row's identity cell to the key field's type.
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

// runAction executes an action of the current view. The identity of the
// record it acts on comes from the selected row, from the record the page is
// already about, or from nowhere at all (add). Mutations reload the view
// afterwards; anything still missing — edit content, a destructive
// confirmation — opens a form first.
func (m Model) runAction(a capAction, tbl view.Table) (tea.Model, tea.Cmd) {
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
			return m, nil
		}
		row := tbl.Rows[min(m.row, len(tbl.Rows)-1)]
		if len(row) == 0 {
			return m, nil
		}
		v, err := rowKey(keys[0], row[0])
		if err != nil {
			return m, nil
		}
		base[keys[0].Name] = v
	case srcSelf:
		// The page already knows its subject: reuse the identity it ran with.
		for _, f := range keys {
			if v, ok := m.lastValues[f.Name]; ok {
				base[f.Name] = v
			}
		}
		if len(base) != len(keys) {
			return m, nil
		}
	}
	m.refreshPending = a.cap.Safety != plugin.Read
	// Removing the very record this page is about destroys the page: the
	// reload afterwards has to land one level further back.
	m.subjectGone = a.src == srcSelf && a.cap.Safety == plugin.Destructive
	m.current = a.cap
	if a.cap.Safety == plugin.Destructive || len(fieldsAfter(a.cap, base)) > 0 {
		return m.startFormWith(a.cap, base)
	}
	m.lastValues, m.lastYes = base, false
	return m, m.startRun(a.cap, base, false)
}

// startFormWith opens a form for the fields base does not cover, seeded by
// Prefill when the capability offers it — the row-action edit experience.
func (m Model) startFormWith(c plugin.Capability, base map[string]any) (tea.Model, tea.Cmd) {
	var defaults map[string]any
	if c.Prefill != nil && len(base) > 0 {
		d, err := prefill(c, base)
		if err != nil {
			m.refreshPending = false
			return m.formError(c, err)
		}
		defaults = d
	}
	m.current = c
	m.form = newCapForm(c, fieldsAfter(c, base), m.formSeed(c, defaults), true, base)
	m.fitForm()
	m.mode = modeForm
	return m, m.form.form.Init()
}

// firstPositional returns the leading positional input, if there is one.
func firstPositional(c plugin.Capability) []plugin.Field {
	for _, f := range c.Inputs {
		if f.Positional {
			return []plugin.Field{f}
		}
	}
	return nil
}

// keyFields returns the required positional inputs — the identity of the
// record a Prefill-capable capability works on.
func keyFields(c plugin.Capability) (keys, rest []plugin.Field) {
	for _, f := range c.Inputs {
		if f.Positional && f.Required {
			keys = append(keys, f)
		} else {
			rest = append(rest, f)
		}
	}
	return keys, rest
}

// prefill fetches current values for a capability's editable fields.
func prefill(c plugin.Capability, base map[string]any) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Prefill(ctx, plugin.NewRequest(base, false, false).WithSurface(plugin.SurfaceTUI))
}

// unasked returns the request values no field of c can carry back — the
// reserved inputs, which every surface sets by some means other than asking.
// "detail" is the only one today; the rule is written against the property
// rather than the name, because a second reserved input would otherwise
// reintroduce the same bug silently.
func unasked(c plugin.Capability, values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	declared := make(map[string]bool, len(c.Inputs))
	for _, f := range c.Inputs {
		declared[f.Name] = true
	}
	var out map[string]any
	for k, v := range values {
		if declared[k] {
			continue
		}
		if out == nil {
			out = map[string]any{}
		}
		out[k] = v
	}
	return out
}

// startForm opens the input form. Prefill-capable capabilities stage it:
// identity fields first, then the remaining fields seeded with the record's
// current values — editing in place, not typing blind.
//
// carry holds request values the form will not ask about and must not drop.
// A form rebuilds the value map from its own bindings, so editing inputs with
// `e` rebuilt it without them: pressing `D` to turn detail off and then `e` to
// change a filter came back detailed, because runCmd's "detailed unless
// somebody said otherwise" default saw nothing and put it back. Only the `e`
// path carries anything — opening a fresh capability starts from its own
// defaults rather than inheriting a toggle from whatever ran last.
func (m Model) startForm(c plugin.Capability, carry map[string]any) (tea.Model, tea.Cmd) {
	keys, rest := keyFields(c)
	switch {
	case c.Prefill != nil && len(keys) > 0 && len(rest) > 0:
		// Stage one's values become stage two's base, so carrying here is
		// enough for both.
		m.form = newCapForm(c, keys, m.formSeed(c, nil), false, carry)
	case c.Prefill != nil && len(keys) == 0:
		defaults, err := prefill(c, nil)
		if err != nil {
			return m.formError(c, err)
		}
		m.form = newCapForm(c, c.Inputs, m.formSeed(c, defaults), true, carry)
	default:
		m.form = newCapForm(c, c.Inputs, m.formSeed(c, nil), true, carry)
	}
	m.fitForm()
	m.mode = modeForm
	return m, m.form.form.Init()
}

// fitForm sizes the embedded huh form to the panel frame.
//
// The height is the important half. A form is as tall as the capability has
// inputs — `kv set` asks eight questions — and a terminal is as tall as it
// is; without a bound the last fields render off-screen, including the
// destructive confirmation, which is the one nobody may miss. Given a height,
// huh scrolls its group instead, so every field stays reachable on a laptop
// with a split terminal.
func (m *Model) fitForm() {
	if m.form == nil {
		return
	}
	if m.width > 0 {
		// 80 is plenty on wide screens.
		m.form.form = m.form.form.WithWidth(min(m.width-6, 80))
	}
	if m.height > 0 {
		// Panel border (2) + the blank lead-in line (1) + footer (1) + a row
		// of slack, since huh sizes its own help line. The floor keeps a very
		// short terminal usable rather than empty.
		m.form.form = m.form.form.WithHeight(max(m.height-5, 6))
	}
}

// formError surfaces a staging failure (e.g. unknown id) as a result pane.
func (m Model) formError(c plugin.Capability, err error) (tea.Model, tea.Cmd) {
	m.form = nil
	m.current = c
	m.result = resultMsg{cap: c, err: view.AsError(err, c.ID+".failed")}
	m.mode = modeResult
	m.renderResult()
	m.viewport.GotoTop()
	return m, nil
}

// updateForm drives the embedded huh form and dispatches on completion.
func (m Model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.form == nil {
		m.mode = modeBrowse
		return m, nil
	}
	model, cmd := m.form.form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		m.form.form = f
	}
	return m.afterFormUpdate(cmd)
}

// afterFormUpdate dispatches on the form's state after it was driven
// forward by one message — a real keypress from updateForm, or several
// synthetic ones from fastSubmitForm. Split out so both land on the same
// completion logic rather than the fast path needing its own copy of it.
func (m Model) afterFormUpdate(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	switch m.form.form.State {
	case huh.StateCompleted:
		if m.form.configTarget != "" {
			return m.saveConfigForm()
		}
		if !m.form.final {
			// Identity collected: fetch current values and open the edit
			// stage seeded with them. A fast-submitted first stage stops
			// here rather than driving the second stage too — its fields
			// are not on screen yet to have current values worth accepting.
			base := m.form.values()
			defaults, err := prefill(m.current, base)
			if err != nil {
				return m.formError(m.current, err)
			}
			_, rest := keyFields(m.current)
			m.form = newCapForm(m.current, rest, m.formSeed(m.current, defaults), true, base)
			m.fitForm()
			return m, m.form.form.Init()
		}
		if !m.form.confirmed() {
			// Destructive run declined: back to where we came from, nothing
			// happened. Fast-submitting a destructive form lands here too,
			// not on the run below — a Confirm field's own default is the
			// negative, so racing through it without touching it declines,
			// the same as leaving it alone and pressing enter would.
			m.flash = ""
			return m.closeForm()
		}
		m.lastValues = m.form.values()
		m.lastYes = m.form.confirmed() && m.current.Safety == plugin.Destructive
		m.form = nil
		return m, m.startRun(m.current, m.lastValues, m.lastYes)
	case huh.StateAborted:
		return m.closeForm()
	}
	return m, cmd
}

// fastSubmitForm accepts whatever is currently bound across every
// remaining field, exactly as pressing enter on each in turn would.
func (m Model) fastSubmitForm() (tea.Model, tea.Cmd) {
	if m.form == nil {
		return m, nil
	}
	m.form.form = advanceFormBySyntheticEnter(m.form.form)
	return m.afterFormUpdate(nil)
}

func (m Model) View() tea.View {
	var v tea.View
	switch m.mode {
	case modeDashboard:
		v = tea.NewView(m.dashboardView())
		v.MouseMode = tea.MouseModeCellMotion // clickable tiles
	case modeForm:
		v = tea.NewView(m.formView())
	case modeRunning:
		body := fmt.Sprintf("%s running %s …\n\n%s",
			m.spinner.View(), theme.Key.Render(m.current.ID),
			fitHintBar(m.width, footerMaxLines, labelled(bindBack, "leave it running"), action("ctrl+c", "quit")))
		if m.width > 0 && m.height > 0 {
			body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
		}
		v = tea.NewView(body)
	case modePlugins:
		v = tea.NewView(m.pluginsView())
	case modeTheme:
		v = tea.NewView(m.themeView())
	case modeCopyPick:
		v = tea.NewView(m.copyPickView())
	case modeResult:
		v = tea.NewView(m.resultView())
		v.MouseMode = tea.MouseModeCellMotion // wheel scrolls long output
	case modeBrowse:
		v = tea.NewView(m.browseView())
		v.MouseMode = tea.MouseModeCellMotion
	default:
		v = tea.NewView(m.list.View())
		v.MouseMode = tea.MouseModeCellMotion
	}
	v.AltScreen = true
	return v
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

// formView frames the input form in an accent panel.
func (m Model) formView() string {
	if m.form == nil {
		return ""
	}
	footer := fitHintBar(m.width, footerMaxLines,
		labelled(bindOpen, "next"), item(bindFastSubmit), labelled(bindBack, "cancel"))
	// The panel takes the whole screen minus the footer, so the frame is a
	// guarantee and not an estimate: huh scrolls its fields inside it, and
	// nothing can render past the last row whatever the field mix turns out
	// to cost.
	return panel(capHead(m.current), "\n"+m.form.form.View(), m.width, m.height-lipgloss.Height(footer), true) + "\n" + footer
}

// resultView frames the result in a titled panel: identity in the top
// border, cost on the right, contextual keys below.
func (m Model) resultView() string {
	head := capHead(m.current)
	if m.result.elapsed > 0 {
		head.Right = m.result.elapsed.Round(time.Millisecond).String()
	}

	// Footer: only the keys that apply right now. Actionable views lead with
	// their actions; row actions stay hidden until there is a row to act on.
	var keys []hintItem
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
		for _, t := range viewToggleSpecs[m.current.ID] {
			// A toggle says which way it is pointing, or half the time it
			// reads as a thing you already did.
			label := t.label
			if m.toggleOn(t) {
				label = theme.GoodText.Render("✓") + " " + label
			}
			keys = append(keys, action(t.key, label))
		}
		keys = append(keys, labelled(bindRerun, "refresh"))
	} else {
		if m.result.view != nil || m.result.err != nil {
			keys = append(keys, item(bindRerun))
			if hasInputs(m.current) {
				keys = append(keys, item(bindEdit))
			}
		}
		keys = append(keys, item(bindScroll))
	}
	if m.result.view != nil {
		keys = append(keys, item(bindCopy))
	}
	if hint, ok := copyHint(m.current.ID, m.result.view); ok {
		keys = append(keys, hint)
	}
	keys = append(keys, item(bindBack), item(bindQuit))
	footer := fitHintBar(m.width, footerMaxLines, keys...)
	if m.flash != "" {
		footer += theme.GoodText.Render("  ✓ " + m.flash)
	}
	return panel(head, m.viewport.View(), m.width, m.height-lipgloss.Height(footer), false) + "\n" + footer
}

// Run starts the TUI program.
func Run(ctx context.Context, reg *registry.Registry, dash config.Dashboard, pluginCfg func(namespace string) map[string]any) error {
	// Explicit input: main() has pointed os.Stdin at /dev/null so that no
	// plugin inherits the user's keyboard, and bubbletea's default is
	// os.Stdin — without this the TUI would open and answer no key at all.
	_, err := tea.NewProgram(New(reg, dash, pluginCfg),
		tea.WithContext(ctx), tea.WithInput(stdio.Real())).Run()
	return err
}
