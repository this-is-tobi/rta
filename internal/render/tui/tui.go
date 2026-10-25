// Package tui is the interactive shell over the capability registry: one app
// that hosts every plugin's views. Capabilities never own the screen — the
// shell browses the registry, runs capabilities, and renders their Views.
//
// What it hosts: a dashboard of tiles refreshed on a timer with a live
// search bar over the catalogue, the catalogue itself, forms built from each
// capability's declared inputs with completion and an environment picker,
// results with row actions and detail pages composed from other views, the
// dry-run confirmation every destructive run passes through, and the panes
// for plugins, profiles and the theme. One key vocabulary (keys.go) and one
// footer teach all of it.
package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	"github.com/charmbracelet/colorprofile"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/stdio"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// refreshTimeout bounds a run nobody asked for: a dashboard tile, or a live
// view re-running under the reader.
//
// This is the whole of what used to be a deadline on every run, kept here
// because here it is the only terminator. A tile's cancel is deferred inside
// tileCmd's own closure, never stored on the Model and bound to no key, so
// nothing a person can press ends a tile that has stopped answering — and a
// tunnelled tile holds a kubectl port-forward for the length of its call.
// The dashboard's tick does not wait for the previous round either (tickMsg
// → refreshTiles): it fires every tile every tileRefreshInterval regardless,
// so a tile stuck for the whole of this deadline has six of itself in flight
// and six forwards open. That is the pre-existing shape; this deadline is
// what stops it growing without limit. For a live view the deadline does a
// second job: the next tick is armed only from the completed run's own
// resultMsg, so a refresh that never lands is a live view that silently
// stops being live.
//
// A var only for the tests, which move it — see completeTimeout for the
// same reason spelled out at length.
var refreshTimeout = 30 * time.Second

// bindTimeout bounds resolving what an environment contributes, which since
// `kube:` secrets can mean a cluster read rather than only a local store.
//
// Shorter than refreshTimeout because nobody asked for it: this runs while
// painting a switch, and an environment that cannot be resolved should leave
// the operator at a dashboard reporting so rather than at one that never
// finishes switching. Failures here are already dropped — the tile that needs
// the credential reports it — so the deadline only bounds how long that takes.
const bindTimeout = 10 * time.Second

type mode int

const (
	modeDashboard mode = iota
	modeBrowse
	modeForm
	modeRunning
	modeResult
	modePlugins
	modeProfiles
	modeProfilePlugins
	modeTheme
	modeCopyPick
	// modeAddPick asks which connection a tile being added is about
	// (addpick.go).
	modeAddPick
	// modeConfirm is the destructive gate: a dry run on screen, and enter is
	// the consent (confirm.go).
	modeConfirm
)

// capItem adapts a capability to the bubbles list.
type capItem struct{ c plugin.Capability }

func (i capItem) FilterValue() string { return filterTarget(i.c) }

// runRef remembers one actionable view — a list, or the page of a single
// record — so actions launched from it can hop out (show/edit/done/remove)
// and land back on it, refreshed.
type runRef struct {
	cap    plugin.Capability
	values map[string]any
}

// PluginConfig is the operator's per-plugin configuration as the shell reads
// it, already matched to the artifact by internal/pluginconf, whose Resolver
// is the implementation: the values stated for a namespace, and the heading
// they were stated under — `pg@1a2b3c4d` for an installed plugin, the bare
// namespace for a built-in — which a refusal of one of them names
// (plugin.Inputs.ConfigSection).
//
// One value answering both rather than two functions, because the two are
// one resolution's answer and must not come from different ones. The plugin
// config editor re-resolves after it saves, and it saves by moving a stale
// pin to the installed digest: a heading kept from the resolution before
// would name the pin the save just removed, beside values read under the
// new one.
type PluginConfig interface {
	For(namespace string) map[string]any
	Section(namespace string) string
}

// Model is the TUI shell.
type Model struct {
	reg *registry.Registry
	// pluginCfg answers what the operator stated for a namespace, and under
	// which heading. nil means nothing is configured, which is the ordinary
	// state.
	pluginCfg PluginConfig
	list      list.Model
	// cols carries the catalogue column widths, measured once so the
	// header above the list and the rows inside it agree.
	cols      capDelegate
	viewport  viewport.Model
	spinner   spinner.Model
	form      *capForm
	themeForm *themeForm
	// themePick is the choice of whose palette the theme editor is on, which
	// it opens on when there is more than one; nil once it has been made.
	themePick *themePicker
	copyPick  *copyPickForm
	addPick   *addPickForm
	// dashOnDisk fingerprints the dashboard block as the file last stated
	// it (dashStamp): what this session read at launch, or wrote itself.
	// The tick compares the file against it, so a tile added from another
	// terminal is adopted and this session's own writes are not re-read
	// as somebody else's (syncDashboard).
	dashOnDisk string
	tiles      []tile
	// tileRaw is what each tile's capability last returned, by rawKey,
	// beside the cleaned copy the tile itself holds — see asReturned.
	tileRaw map[string]returned
	dash    config.Dashboard // the arrangement, edited in place and saved
	// view is the dashboard view dash is: "" for the dashboard: block, else
	// one of its views. Every write of the arrangement goes to it, so what a
	// person hides, moves or adds is what they are looking at.
	view string
	// viewPick is the view this session chose with V or v, or "" to follow the
	// switched-on profile's. A switch clears it: switching is asking for that
	// environment's screen.
	viewPick string
	// selected is the dashboard tile that holds the selection: 0 for the search
	// bar and -1 while the header of a section does, which header names. A header
	// is not a tile, so everything that reads a tile off the selection finds none
	// rather than the first one of the section, which is what a header stored as
	// that tile's index would have handed it.
	selected int
	header   string
	// folds is what this session folded or unfolded, by screen and section name,
	// on top of how the file starts each one (sections.go says why it is not
	// written).
	folds      map[foldKey]fold
	scroll     int  // first visible dashboard tile row
	origin     mode // where the current result/form was opened from
	mode       mode
	current    plugin.Capability // capability being viewed/run
	lastValues map[string]any    // inputs of the last run, reused by re-run
	lastYes    bool
	// submitted is the form the run in flight was sent from, until the run
	// answers, so that a refusal of one of its boxes is answered on the form
	// (reopenOn).
	submitted  *submission
	result     resultMsg
	flash      string // one-shot footer notice (e.g. "copied"), cleared on next key
	flashBad   string // the flash when it reports what did not happen (see refuse)
	flashGuide string // the flash when it says what to do next and reports nothing done (see guide)
	// armedDelete is the two-press gate on the profile panes' `d`: the first
	// press names what would be removed, the next `y` removes it, and any
	// other key disarms. It exists because `d` sat one mispress from the
	// navigation keys and deleted a whole environment — config entry, active
	// switch and every grant naming it — with less ceremony than the TUI
	// gives a Destructive capability's form, which stops for an explicit
	// confirmation stage. Holds the profile name in modeProfiles and the
	// plugin key in modeProfilePlugins; "" when nothing is armed.
	armedDelete string
	// armed is a one-key answer that has said what it would do and waits for
	// the key that confirms it (answers.go); nil when nothing waits.
	armed *armedAnswer
	// dialog is a question over the screen, modal while it is up (dialog.go); nil
	// when none is asked.
	dialog *dialog
	// said is what the footer says when the answer that was just given
	// finishes, in place of the capability's own "done" (answers.go).
	said string
	// help is the key overlay (help.go), open over whatever m.mode is.
	help bool
	// width and height are the room a screen has, not the terminal's: the
	// height is less the row the waiting line takes (banner), and the rows of
	// the terminal are height+banner.
	width  int
	height int
	// waiting is the calls parked for an answer, as of the last look at the
	// queue (waiting.go), and banner the rows the line above every screen takes
	// from the height because of them.
	waiting []waitingCall
	banner  int
	// readWaiting looks at the queue; nil reads the real one, which is what
	// everything but a test wants.
	readWaiting func() []waitingCall

	// Live search bar state (dashboard tile 0).
	searchEditing bool
	query         string
	searchSel     int
	searchInfo    string // idle prompt: plugin/capability inventory

	// noColor is whether the terminal reports no colour at all (NO_COLOR,
	// TERM=dumb, a pipe), told once by tea.ColorProfileMsg. Focus is a border
	// colour, so it is what makes the focused tile heavy-framed instead.
	noColor bool
	// noColorFlag is --no-color: the person asked for no colour whatever the
	// terminal could show (WithNoColor).
	noColorFlag bool

	// Profiles panes: the operator's environments, which one is switched on,
	// and — one level in — the plugins each one covers.
	profiles      []profileRow
	profileSel    int
	profileScroll int
	// profileOpen is the environment whose plugin list is showing, empty on the
	// outer pane. Kept as the name rather than an index because the rows are
	// rebuilt from disk after every edit, and an index would survive a rename
	// pointing at whatever sorted into that slot.
	profileOpen string
	connSel     int
	connScroll  int

	// adHoc is the connection each plugin was last given by a run form's "ad hoc
	// connection…", keyed by namespace and held until the TUI quits, so one
	// stated once serves the next capability of that plugin and the re-run of
	// this one (adhoc.go). Never read from or written to a file: it is a
	// coordinate and references, and what keeps it from being a profile is that
	// nothing outlives the session. Replaced whole, never edited in place, since
	// a Model is copied on every message.
	adHoc map[string]config.Connection

	// allEnvironments is whether a run form's picker lists the connections of
	// every environment, and not only those of the one that is switched on. Set by
	// choosing the picker's "other environments" entry and cleared by "only", held
	// until the TUI quits like the ad hoc connection above and shared by every form
	// (profilePicker).
	allEnvironments bool

	// active is the environment switched on, refreshed with the tiles rather
	// than read at paint time. The dashboard has to show it — "am I in
	// production" is the question a header answers — and paint runs on every
	// keystroke, which is no place for a file read.
	active      string
	activeUntil *time.Time
	// activeColor is that environment's own colour, or "" for one nobody
	// marked. Cached beside the name and for the same reason: paint runs on
	// every keystroke and must not read a file.
	activeColor string
	// bound is the active environment's contribution to each capability, keyed
	// by capability ID.
	//
	// Resolved when the environment changes rather than per refresh, because
	// resolving it can mean unlocking the encrypted store: age's scrypt work
	// factor is about a second, and the dashboard refreshes every five. A
	// credential decrypted once and held for as long as the environment stands
	// is the same exposure the process already has while a capability runs, and
	// the alternative is a dashboard that spends a fifth of its life deriving a
	// key it already derived.
	bound map[string]envBind
	// boundStamp is what bound was resolved from — see environmentStamp. It is
	// the cache key, and it describes the environment rather than naming it,
	// because a profile edited in place keeps its name and changes its meaning.
	boundStamp string
	// pins are the bindings of the profiles tiles are pinned to, keyed by
	// the reference a tile names — see pinBind. The environment's binding
	// above is one per session; these are one per connection on screen.
	pins map[string]pinBind

	// Plugins pane: the inventory, and where a hidden tile comes back from.
	plugins []pluginRow
	// untrusted is what discovery found on $PATH and refused to launch. Held
	// on the Model rather than read from a package variable so two TUIs in one
	// test do not share it.
	untrusted []pluginhost.Untrusted
	// failed is what discovery approved and could not start, held the same way.
	failed    []pluginhost.Failed
	pluginSel int
	// pluginScroll is the first plugin drawn. The pane used to clip instead
	// of scroll, so at 80x24 the last plugin was invisible while `j` still
	// selected it.
	pluginScroll int

	// In-flight run: its cancel func and the sequence number that tells its
	// result apart from one the user has already walked away from.
	cancelRun context.CancelFunc
	runSeq    int
	// previewing says the run in flight is a dry run for the confirmation
	// screen, so the running screen can say so rather than "running".
	previewing bool

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
	// into (note.list → note.show); actions run against its last entry and
	// esc pops back up it.
	trail          []runRef
	row            int  // selected table row of the current list
	goalCol        int  // dashboard column vertical movement aims for
	refreshPending bool // a mutating action ran: reload the view it came from
	subjectGone    bool // …and that action destroyed that view's subject
}

// Option adjusts a Model at construction. Variadic so that the many call
// sites which need none of them say nothing.
type Option func(*Model)

// WithView says which dashboard view dash is, so a session that opens on one
// writes to it and not to the dashboard: block.
func WithView(view string) Option {
	return func(m *Model) {
		m.view = view
		m.dashOnDisk = dashStamp(view, m.dash)
	}
}

// WithNoColor is --no-color. The program reads NO_COLOR and TERM from the
// environment and nothing else, and the flag is not in the environment, so it
// is handed over here and reaches the program as the colour profile NO_COLOR
// gives it (colorOptions). The model is told at once as well, so that nothing it
// draws before the program's own word on the profile is coloured.
func WithNoColor() Option {
	return func(m *Model) { m.noColor, m.noColorFlag = true, true }
}

// WithUntrusted supplies the plugin artifacts discovery found on $PATH and
// refused to launch, so the inventory can show them instead of leaving a
// person to wonder where a plugin they installed went.
func WithUntrusted(us []pluginhost.Untrusted) Option {
	return func(m *Model) { m.untrusted = us }
}

// WithFailed supplies the plugins discovery launched and could not start, so
// the inventory shows them with the reason instead of leaving the one line
// that said so under the screen the dashboard opened on.
func WithFailed(fs []pluginhost.Failed) Option {
	return func(m *Model) { m.failed = fs }
}

// New builds the shell over a registry. dash configures the dashboard; its
// zero value is the automatic one-tile-per-plugin arrangement. pluginCfg
// answers what the operator stated for a namespace and under which heading;
// nil is a decision the caller has to type, which is the point.
//
// A parameter rather than a setter, for the same reason as
// plugin.Resolve's third argument: Run used to take this and New could not,
// so the value had nowhere to go and was dropped on the floor. Every surface
// that reads it — the form seed, the run, the dashboard refresh — then saw
// nil, and the operator's configuration reached the CLI and no part of the
// TUI. A constructor that can still be called the old way is the same defect
// waiting to be reintroduced. The heading came in later, and inside the same
// parameter for that reason too: taking the values alone, every refusal of
// a pinned plugin's value here named `plugins.<ns>.<key>`, a line its file
// cannot have, where the CLI and mcp serve named the heading.
func New(reg *registry.Registry, dash config.Dashboard,
	pluginCfg PluginConfig, opts ...Option) Model {
	items := catalogueItems(reg)
	cols := newCapDelegate(items)
	l := list.New(items, cols, 0, 0)
	// Strip the list's own chrome: its help line speaks a different
	// vocabulary from the rest of the app ("↑/k up", "/ filter"), and its
	// title and status bar put four lines between the column header and the
	// first row. browseView draws all three in the app's own terms.
	// Filtering is untouched — the filter input renders on its own row
	// whether or not the title does (bubbles list.titleView).
	l.Filter = catalogueFilter
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
		spinner.WithStyle(spinnerStyle()),
	)
	m := Model{
		reg:        reg,
		pluginCfg:  pluginCfg,
		list:       l,
		cols:       cols,
		viewport:   viewport.New(),
		spinner:    sp,
		tiles:      buildTiles(reg, dash),
		dash:       dash,
		dashOnDisk: dashStamp("", dash),
		mode:       modeDashboard,
		searchInfo: fmt.Sprintf("%d plugins · %d capabilities",
			len(reg.Plugins()), len(reg.Capabilities())),
	}
	for _, opt := range opts {
		opt(&m)
	}
	// The name, before the first paint, so the header says where this session is
	// from the frame it opens on. The *binding* is Init's job, off the update
	// loop: resolving a `secrets:` reference is a key derivation, and a shell
	// that takes a second to appear is a shell people stop opening.
	m.active = profile.Active()
	m.activeColor = profileColor(m.active)
	m.boundStamp = environmentStamp(m.active)
	// Resolved again now that the environment is known, since a tile that
	// follows it may expand into its connections; the first build above
	// had no environment in hand.
	m.rebuildTiles()
	deferSlowLaunches(m.tiles, time.Now())
	m.notePins(readStamps())
	return m
}

// wheelStep is how many rows one notch of the wheel moves. Three is what
// every terminal pager uses, and a list that moves one row per notch reads
// as a list that is resisting.
const wheelStep = 3

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{refreshTiles(m.tiles, m.tickGen, m.pluginCfg, m.connFor), m.pollWaiting(0)}
	if m.active != "" {
		cmds = append(cmds, bindCmd(m.reg, m.active, m.boundStamp))
	}
	// The pins New noted, bound here: Init gets a copy of the model, so
	// noting them here would be noting them nowhere, and the refresh above
	// already treats a noted, unbound pin as pending rather than unbound.
	// One to the switched-on environment rides on the bind started above.
	for ref, pin := range m.pins {
		if !pin.ready && !m.servedByEnvironment(ref, pin.stamp) {
			cmds = append(cmds, bindCmd(m.reg, ref, pin.stamp))
		}
	}
	return tea.Batch(cmds...)
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
	tbl, _, ok := rowTable(m.result.view)
	return ok && len(tbl.Rows) > 0
}

// rowTable is the table whose rows v is walked and acted on by: v itself, or
// the one table on a page (view.Sections) whose other sections are what is
// said around it. at is the table's place among the page's sections, -1 for
// v itself, which is what the pane scrolls a row into view by.
//
// **A page, because grant list answers one whenever a role stands.** The
// roles in force lead the roster, and a note follows it when the team's
// policy holds grants back; only a bare table was a list to walk, so the
// roster lost x and n on its rows the moment somebody issued a role — on the
// one screen the docs send people to before they walk away from a machine.
//
// One table and no other anywhere on the page, nested pages included: the
// row under the cursor has to be a row of one table, and the highlight a
// render draws (cli.Options.Highlight) reaches every table it renders.
func rowTable(v view.View) (tbl view.Table, at int, ok bool) {
	switch t := v.(type) {
	case view.Table:
		return t, -1, true
	case view.Sections:
		at = -1
		for i, it := range t.Items {
			switch inner := it.View.(type) {
			case view.Table:
				if at >= 0 {
					return view.Table{}, -1, false
				}
				tbl, at = inner, i
			case view.Sections:
				if holdsTable(inner) {
					return view.Table{}, -1, false
				}
			}
		}
		return tbl, at, at >= 0
	}
	return view.Table{}, -1, false
}

// holdsTable reports whether a page has a table anywhere on it.
func holdsTable(s view.Sections) bool {
	for _, it := range s.Items {
		switch inner := it.View.(type) {
		case view.Table:
			return true
		case view.Sections:
			if holdsTable(inner) {
				return true
			}
		}
	}
	return false
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

// releaseRun lets go of a run that has landed. Called only after the
// sequence check — a stale result must not cancel the run the person moved
// on to. An asked-for run's context carries no deadline (startRun), so
// nothing else would ever end it: cancelling here is what releases the
// forward-ceiling timer a tunnelled run armed, and what keeps a finished
// run from being held until the next one replaces it.
func (m *Model) releaseRun() {
	if m.cancelRun != nil {
		m.cancelRun()
		m.cancelRun = nil
	}
}

// reopenTop re-runs the actionable view the trail points at, so it reflects
// whatever the action that just ran changed.
func (m Model) reopenTop() (tea.Model, tea.Cmd) {
	t := m.trail[len(m.trail)-1]
	m.current = t.cap
	m.lastValues, m.lastYes = t.values, false
	return m, m.startRun(t.cap, t.values, false)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	if nm, ok := next.(Model); ok {
		return nm.fitCatalogue().fitResult(), cmd
	}
	return next, cmd
}

// fitResult sizes the pane that shows a result, or a confirmation's preview, to
// the room the bar under it leaves. The pane is the window less the panel's two
// borders and the bar, and the bar is one line or two: at two it was one line
// taller than the room, the panel cut the pane's last line, and the final line of
// every answer longer than the screen was out of reach, wherever the pane said it
// was at the bottom. Wrapped at 80 columns for a result with an action of its own
// and for every one at 44.
//
// After every message, for fitCatalogue's reason, and measured without the flash:
// that is gone with the next key, and a pane that grew and shrank by a line with
// it would move what the reader was looking at each time. The size is only set
// when it changed, which leaves the scroll position alone on the many messages
// that change nothing, and a pane at the bottom stays there.
func (m Model) fitResult() Model {
	if m.height <= 0 || (m.mode != modeResult && m.mode != modeConfirm) {
		return m
	}
	quiet := m
	quiet.flash = ""
	room := max(m.height-2-lipgloss.Height(quiet.footerFor(m.mode)), 1)
	if m.viewport.Height() == room {
		return m
	}
	atBottom := m.viewport.AtBottom()
	m.viewport.SetHeight(room)
	if atBottom {
		m.viewport.GotoBottom()
	}
	if m.mode == modeResult {
		m.renderResult()
	}
	return m
}

// fitCatalogue sizes the catalogue's list to what is left of the window once
// its own column header, the line for the selected row and the hint bar are
// drawn, which is the room it has — otherwise its last rows land under the
// footer and the scroll position lies about what is reachable.
//
// **After every message, because the footer is not one height.** It was done
// once, when the window's size arrived, with whatever the footer said then; a
// message in it, which `+` on the dashboard puts there, makes it two lines,
// and the frame was a line taller than the terminal, so the renderer cut off
// the bottom: the line that had just been put there. The size is only set when
// it changed, which leaves the list's page and cursor alone on the many
// messages that change nothing.
func (m Model) fitCatalogue() Model {
	if m.height <= 0 {
		return m
	}
	h, _ := m.catalogueRows(m.browseFooter())
	w := m.width
	if m.list.Width() != w || m.list.Height() != h {
		m.list.SetSize(w, h)
	}
	return m
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height, m.banner = msg.Width, msg.Height, 0
		return m.settleBanner().fitToWindow(), nil

	case waitingMsg:
		m.waiting = msg.calls
		if settled := m.settleBanner(); settled.banner != m.banner {
			m = settled.fitToWindow()
		}
		return m, m.pollWaiting(waitingPoll)

	case tea.ColorProfileMsg:
		m.noColor = msg.Profile <= colorprofile.ASCII
		return m, nil
	case tea.MouseWheelMsg:
		return m.wheel(msg)
	case spinner.TickMsg:
		if m.mode == modeRunning {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case completeMsg:
		return m.applyCompletion(msg)

	case copiedMsg:
		if msg.refusal != "" {
			m.refuse(msg.refusal)
		} else {
			m.flash = msg.flash
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
			//
			// And what the capability returned is kept beside it, for the
			// reasons resultMsg.cleaned gives: cli.Render, which draws the
			// panel and cleans its own copy, and the copy key, which hands
			// over the record's value rather than its display spelling.
			// Rebuilt rather than written into, so a copy of the model
			// taken before this answer does not see it — the rest of Model
			// is a value too — and so a tile that has left the dashboard
			// does not keep its last answer in memory for the session.
			m.tiles[idx].inFlight = false
			m.tiles[idx].refused = refusedAfter(m.tiles[idx].refused, msg.err)
			m.tiles[idx].view = view.MapStrings(msg.v, textclean.Terminal)
			m.tiles[idx].err = view.MapErrorStrings(msg.err, textclean.Terminal)
			raw := make(map[string]returned, len(m.tiles))
			for _, t := range m.tiles {
				if r, ok := m.tileRaw[rawKey(t)]; ok {
					raw[rawKey(t)] = r
				}
			}
			raw[rawKey(m.tiles[idx])] = answeredWith(m.tileRaw[rawKey(m.tiles[idx])], msg.v, msg.err, time.Now())
			m.tileRaw = raw
			// An answer sets its row's height, which sets how many rows fit —
			// the one thing clampScroll exists to keep honest, and the one
			// mutation of it that used to skip the call. Unconditional:
			// clampScroll is idempotent, and asking first whether the height
			// moved costs a cli.Render per tile on the row, in the update
			// loop, on a five-second timer.
			m.clampScroll()
		}
		return m, nil

	case tea.MouseClickMsg:
		if m.dialog != nil {
			return m.dialogClick(msg)
		}
		if m.mode == modeDashboard && msg.Button == tea.MouseLeft {
			// A click on a header folds the section, anywhere along its line, and
			// leaves the selection there: the header is what was pressed.
			if name, ok := m.headerAt(msg.Y - m.banner); ok {
				m.searchEditing, m.query, m.searchSel = false, "", 0
				m.selectStop(stop{header: name})
				m.setCollapsed(name, !m.collapsed(name))
				m.clampScroll()
				return m, nil
			}
			if idx := m.tileAt(msg.X, msg.Y-m.banner); idx >= 0 {
				// A click moves focus off the search box as much as a launch
				// from it does, so its query is spent the same way. Left
				// standing, esc back to the dashboard found the box still
				// holding the keyboard with the old query in it, and `q`
				// typed a letter instead of quitting.
				if !m.tiles[idx].search {
					m.searchEditing, m.query, m.searchSel = false, "", 0
				}
				m.selected = idx
				return m.openTile(idx)
			}
		}
		return m, nil

	case boundMsg:
		// Dropped when the environment has moved on while this was resolving: an
		// operator who flipped twice in a second must not end up with the first
		// environment's values under the second one's name.
		//
		// By stamp, not by name, for the reason the stamp exists at all: an
		// edit to the environment already switched on leaves the name identical
		// and changes the answer, so a bind started before the edit would
		// otherwise be accepted over the one started after it.
		//
		// A pinned profile's bind lands here too, told apart by the stamp
		// it carries — and a profile can be both the switched-on
		// environment and a tile's pin, in which case one landing serves
		// both.
		landed := false
		if msg.stamp == m.boundStamp {
			m.bound = msg.bound
			// Every tile following the environment, whatever pace it
			// declared: the environment just changed, and a tile's answer
			// for the previous one is wrong however recently it was
			// computed.
			m.connectionChanged("")
			landed = true
		}
		if pin, ok := m.pins[msg.name]; ok && pin.stamp == msg.stamp {
			pin.bound, pin.ready = msg.bound, true
			m.pins[msg.name] = pin
			m.connectionChanged(msg.name)
			landed = true
		}
		if !landed || m.mode != modeDashboard {
			return m, nil
		}
		m.tickGen++
		return m, refreshTiles(m.tiles, m.tickGen, m.pluginCfg, m.connFor)

	case tickMsg:
		// A live view under the reader is re-run in place — no spinner, the
		// old rows stay until the new ones land — on the same chain rule as
		// the dashboard's below.
		if m.mode == modeResult && msg.gen == m.tickGen && m.current.Live {
			m.tickGen++
			return m, m.refreshInPlace(m.current, m.lastValues, m.lastYes)
		}
		// Refresh only while the dashboard is visible, and only the chain
		// currently in force — anything else is a return trip's predecessor,
		// left ticking after a later visit already restarted it, and letting
		// it re-arm here is exactly how the count of live timers used to
		// grow with every trip through browse and back.
		if m.mode == modeDashboard && msg.gen == m.tickGen {
			// Re-read the switch on the way past. A deadline lapses without
			// anybody touching the keyboard, and `rta use` in another terminal
			// is somebody changing environments without leaving this one — both
			// have to reach the screen, and this is the only thing that runs
			// while nothing is happening. Cheap unless the name actually moved.
			bind := tea.Batch(m.syncActive(), m.syncTiles())
			m.tickGen++
			return m, tea.Batch(bind, refreshTiles(m.tiles, m.tickGen, m.pluginCfg, m.connFor))
		}
		return m, nil

	case previewMsg:
		// Dropped on the same rule as a result: a dry run the user walked
		// away from must not become a confirmation over what they walked to.
		if msg.seq != 0 && msg.seq != m.runSeq {
			return m, nil
		}
		m.releaseRun()
		// A dry run that refuses one of the boxes the form sent is answered on
		// the form, as the run's own refusal is (reopenOn).
		if sub := m.submitted; sub != nil && msg.err != nil && msg.seq == sub.seq {
			if nm, cmd, ok := m.reopenOn(sub, msg.cap, msg.err); ok {
				return nm, cmd
			}
		}
		m.showConfirm(resultMsg{cap: msg.cap, view: msg.view, err: msg.err, elapsed: msg.elapsed, seq: msg.seq})
		return m, nil

	case resultMsg:
		// A result the user walked away from must not paint over what they
		// walked to.
		if msg.seq != 0 && msg.seq != m.runSeq {
			return m, nil
		}
		m.releaseRun()
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
		// The view the capability returned is kept beside the cleaned one,
		// for everything that acts on the record rather than drawing it —
		// see resultMsg.cleaned. The error is not cleaned here: the one
		// thing that draws it is cli.RenderError, which cleans its own copy.
		msg = msg.cleaned()
		if msg.err == nil {
			staleAfter(m.tiles, msg.cap)
		}
		// The form this run was sent from, taken off the model with the answer
		// to it, whatever that answer is and whichever branch below takes it:
		// it holds what the form sent, a credential typed into it included.
		sub := m.submitted
		if sub != nil && msg.seq != 0 && msg.seq == sub.seq {
			m.submitted = nil
		} else {
			sub = nil
		}
		// The unlock form answered: back to the dashboard it was opened from,
		// with the tiles that were failing run again at once, and the answer as
		// a footer line. The result page that every other Read gets would hold
		// the tiles still until it was closed.
		if msg.cap.ID == unlockID && msg.err == nil {
			if t, ok := msg.view.(view.Text); ok {
				m.flash = t.Body
			}
			m.origin = modeDashboard
			return m.closeToOrigin()
		}
		// A refusal of the call a form sent, naming one of its boxes, is
		// answered on that form (reopenOn): a passphrase the store refused,
		// on the unlock form, among them.
		if nm, cmd, ok := m.reopenOn(sub, msg.cap, msg.err); ok {
			return nm, cmd
		}
		// A mutating action finished cleanly: flash its outcome and return
		// to the view it was launched from, reloaded. If it destroyed that
		// view's subject (removing the very task whose page we were on),
		// come back one level further instead of reloading a record that no
		// longer exists.
		if m.refreshPending && msg.err == nil && !m.isTop(msg.cap) {
			m.refreshPending = false
			m.flash = flashText(msg)
			if m.said != "" {
				m.flash = m.said
			}
			m.said = ""
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
		m.said = ""
		m.mode = modeResult
		m.current = msg.cap
		m.result = msg
		m.enterTrail(msg.cap, m.lastValues)
		tail := false
		if tbl, _, ok := rowTable(msg.view); ok {
			m.row = min(m.row, max(len(tbl.Rows)-1, 0))
			// A log opens where things are now — its last row — and is
			// walked back from there. On a refresh too: the newest row is
			// the one that changed, so the cursor goes to it rather than
			// staying on whatever row it was.
			if tbl.Tail {
				m.row, tail = max(len(tbl.Rows)-1, 0), true
			}
		}
		m.renderResult()
		if tail {
			m.viewport.GotoBottom()
		} else {
			m.viewport.GotoTop()
		}
		if msg.cap.Live {
			return m, m.liveTick()
		}
		return m, nil

	case tea.PasteMsg:
		if m.dialog != nil {
			return m, nil
		}
		if nm, ok := m.pasteIntoSearch(msg); ok {
			return nm, nil
		}

	case tea.KeyPressMsg:
		m.flash = ""
		// Routed per pane (dispatch.go). A key the pane did not consume falls
		// through to its passthrough below — the embedded list, form or
		// viewport — exactly as the unmatched switch arms used to.
		if nm, cmd, done := m.keyPress(msg); done {
			return nm, cmd
		}
	}

	var cmd tea.Cmd
	switch m.mode {
	case modeBrowse:
		before := m.list.Index()
		m.list, cmd = m.list.Update(msg)
		// New matches mean a new list: the cursor starts at its top, because
		// the index it was left at belonged to the catalogue with its section
		// headers in, and in a filtered list that index is the second match.
		if _, matched := msg.(list.FilterMatchesMsg); matched {
			m.list.Select(0)
			before = 0
		}
		// Put the cursor somewhere enter can act on: not a section label, and
		// not past the end of a list a filter just shortened.
		m.settleCursor(m.list.Index() >= before)
	case modeForm:
		return m.updateForm(msg)
	case modeTheme:
		if m.themePick != nil {
			return m.updateThemePick(msg)
		}
		return m.updateThemeForm(msg)
	case modeCopyPick:
		return m.updateCopyPick(msg)
	case modeAddPick:
		return m.updateAddPick(msg)
	case modeResult, modeConfirm:
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}

// fitToWindow sizes every pane to the room the screen has, which is the
// window less the row a waiting call's line takes.
func (m Model) fitToWindow() Model {
	m.viewport.SetWidth(m.width - 4)   // result panel: borders + padding
	m.viewport.SetHeight(m.height - 3) // panel top/bottom + footer
	m.clampScroll()                    // dashboard rows per screen changed
	if m.mode == modeResult {
		m.renderResult() // reflow to the new width
	}
	// A form open across a resize has to be re-fitted too, or it keeps the
	// height of a window that no longer exists.
	m.fitForm()
	m.fitThemeForm()
	m.fitCopyPick()
	m.fitAddPick()
	return m
}

// closeToOrigin returns to wherever the current form/result was opened from,
// restarting the tile refresh loop when that is the dashboard.
func (m Model) closeToOrigin() (tea.Model, tea.Cmd) {
	// The environment may have moved while this screen was open — often
	// *because* it was open, since every profile editor closes through here.
	// Correctness does not rest on this line (the refresh tick re-reads the
	// stamp, and a run checks it at the point of use), but the window between
	// saving an edit and the screen behind the form agreeing with it does.
	bind := tea.Batch(m.syncActive(), m.syncTiles())
	m.mode = m.origin
	if m.origin == modeDashboard {
		m.tickGen++
		return m, tea.Batch(bind, refreshTiles(m.tiles, m.tickGen, m.pluginCfg, m.connFor))
	}
	return m, bind
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
	// The store session answers a kv tile's unlock, as it answers any kv action
	// opened from a list (withStoreSession). A tile does not use it on its timer,
	// and says the store is unlocked in this session instead; this is the use that
	// line promises, a person asking to see, and the one that keeps it warm.
	values := withStoreSession(t.cap, t.runValues())
	m.current = t.cap
	m.lastValues, m.lastYes = values, false
	m.trail = nil
	m.row = 0
	return m, m.startRun(t.cap, values, false)
}

// open decides what Enter does for a capability: a read with nothing it must
// be told runs at once (quick.go), a form opens when there is anything to ask
// (inputs or a destructive confirmation), and the rest runs directly.
func (m Model) open(c plugin.Capability) (tea.Model, tea.Cmd) {
	m.current = c
	m.trail = nil
	m.row = 0
	m.refreshPending = false
	if values, ok := m.quickRun(c); ok {
		m.lastValues, m.lastYes = values, false
		return m, m.startRun(c, values, false)
	}
	if hasInputs(c) {
		return m.startForm(c, nil)
	}
	if c.Safety == plugin.Destructive {
		// Nothing to ask, and still nothing runs unseen: the confirmation
		// screen is the gate, with the dry run on it.
		return m, m.startConfirm(c, nil)
	}
	m.lastValues, m.lastYes = nil, false
	return m, m.startRun(c, nil, false)
}

func (m Model) View() tea.View {
	v := m.screen()
	if m.banner > 0 && v.Content != "" {
		v.SetContent(m.bannerLine() + "\n" + v.Content)
	}
	return v
}

// screen is whatever the current mode draws, in the height it was given.
func (m Model) screen() tea.View {
	// Bubble Tea paints the initial model before it delivers the first
	// WindowSizeMsg — the size goes out from a goroutine and the first view
	// is rendered synchronously right after — so every run has at least one
	// frame where this model does not know how big the terminal is. Nothing
	// below degrades gracefully at zero: panel hands the body back unframed
	// under ten cells, cli.Render reads a non-positive width as "no limit",
	// and dashRowsVisible answers "everything" when the height is unknown, so
	// the dashboard's zero-size frame is an unframed, unwrapped,
	// screen-height-ignoring dump of every tile, one per row.
	//
	// Emitting it costs more than the frame itself. The renderer paints each
	// frame as a difference against the one before, and in the alt screen it
	// may realise a vertical move as an insert-line or a scroll region and
	// then record the moved lines as needing no repaint. A garbage first
	// frame is a near-miss vertical match for the real one — exactly what
	// that optimisation looks for — so one tile's body can land inside
	// another tile's border and stay there until something repaints the
	// screen whole.
	//
	// An empty frame has the opposite effect: the renderer sizes its buffer
	// to the content, so the first frame that does have a size is a different
	// shape and forces a full repaint from an erased screen. Nothing can be
	// laid out honestly without a size, so nothing is.
	if m.width <= 0 || m.height <= 0 {
		v := tea.NewView("")
		v.AltScreen = true
		return v
	}
	var v tea.View
	if m.help {
		v = tea.NewView(m.helpView())
		v.AltScreen = true
		return v
	}
	switch m.mode {
	case modeDashboard:
		v = tea.NewView(m.dashboardView())
		v.MouseMode = tea.MouseModeCellMotion // clickable tiles
	case modeForm:
		v = tea.NewView(m.formView())
	case modeRunning:
		verb := "running"
		if m.previewing {
			verb = "previewing"
		}
		body := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			fmt.Sprintf("%s %s %s …\n\n%s",
				m.spinner.View(), verb, theme.Key.Render(m.current.ID), m.footerFor(modeRunning)))
		v = tea.NewView(body)
	case modePlugins:
		v = tea.NewView(m.pluginsView())
	case modeProfiles:
		v = tea.NewView(m.profilesView())
	case modeProfilePlugins:
		v = tea.NewView(m.connsView())
	case modeTheme:
		v = tea.NewView(m.themeView())
	case modeCopyPick:
		v = tea.NewView(m.copyPickView())
	case modeAddPick:
		v = tea.NewView(m.addPickView())
	case modeResult:
		v = tea.NewView(m.resultView())
		v.MouseMode = tea.MouseModeCellMotion // wheel scrolls long output
	case modeConfirm:
		v = tea.NewView(m.confirmView())
		v.MouseMode = tea.MouseModeCellMotion
	case modeBrowse:
		v = tea.NewView(m.browseView())
		v.MouseMode = tea.MouseModeCellMotion
	default:
		v = tea.NewView(m.list.View())
		v.MouseMode = tea.MouseModeCellMotion
	}
	if m.dialog != nil {
		v.SetContent(m.overlayDialog(v.Content))
		v.MouseMode = tea.MouseModeCellMotion // a button can be clicked
	}
	v.AltScreen = true
	return v
}

// Run starts the TUI program.
func Run(ctx context.Context, reg *registry.Registry, dash config.Dashboard,
	pluginCfg PluginConfig, opts ...Option) error {
	// Explicit input: main() has pointed os.Stdin at /dev/null so that no
	// plugin inherits the user's keyboard, and bubbletea's default is
	// os.Stdin — without this the TUI would open and answer no key at all.
	m := New(reg, dash, pluginCfg, opts...)
	// The colours of the environment this session opens in, before the first
	// frame. The CLI has painted them already, from the same file and the same
	// switch, and this is a no-op then; it is here because a session must not
	// depend on its caller having done so, and so that what the model believes
	// about the palette is true from its first paint on.
	m.syncPalette(readStamps())
	program := []tea.ProgramOption{tea.WithContext(ctx), tea.WithInput(stdio.Real())}
	_, err := tea.NewProgram(m, append(program, colorOptions(m)...)...).Run()
	return err
}

// colorOptions say to the program what --no-color said. ASCII is the profile
// NO_COLOR gives on a terminal: the renderer drops every colour from every frame
// and keeps the text attributes (bold, reverse) that stand in for it, which is
// what the model's heavy borders and bracketed answers rely on. It is set on the
// program and not through the process environment: NO_COLOR set here would reach
// every plugin launched afterwards, which the flag does not ask for.
func colorOptions(m Model) []tea.ProgramOption {
	if !m.noColorFlag {
		return nil
	}
	return []tea.ProgramOption{tea.WithColorProfile(colorprofile.ASCII)}
}
