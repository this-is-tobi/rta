package tui

import (
	"encoding/json"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Input dispatch: every key press and wheel event, routed to the open pane.
//
// One handler per mode rather than one 380-line switch inside Update — the
// carve deliberately left out of the pure-move commits, because the
// bool these handlers return is new logic: it says whether the pane consumed
// the key. False hands the key to the pane's passthrough at the bottom of
// Update — the embedded list, form or viewport — and **promises m was not
// changed on the way**: the passthrough updates Update's own copy of the
// model, so a mutation made on this one would be silently lost. Every
// handler that returns false anywhere must mutate nothing before doing so.

// keyPress routes one key press to the open pane's handler.
func (m Model) keyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch m.mode {
	case modeDashboard:
		return m.dashboardKeys(msg)
	case modePlugins:
		return m.pluginsKeys(msg)
	case modeProfiles:
		return m.profilesKeys(msg)
	case modeProfilePlugins:
		return m.profilePluginsKeys(msg)
	case modeBrowse:
		return m.browseKeys(msg)
	case modeForm:
		return m.formKeys(msg)
	case modeTheme:
		return m.themeKeys(msg)
	case modeCopyPick:
		return m.copyPickKeys(msg)
	case modeResult:
		return m.resultKeys(msg)
	case modeRunning:
		return m.runningKeys(msg)
	}
	return m, nil, false
}

func (m Model) dashboardKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if m.searchEditing {
		nm, cmd := m.updateSearch(msg)
		return nm, cmd, true
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit, true
	case "b", ":":
		m.mode = modeBrowse
		return m, nil, true
	case "/":
		// Focus the live search bar.
		m.selected = 0
		m.searchEditing = true
		m.clampScroll()
		return m, nil, true
	case "left", "h":
		m.moveSelection(-1, 0)
	case "right", "l", "tab":
		m.moveSelection(1, 0)
	case "up", "k":
		m.moveSelection(0, -1)
	case "down", "j":
		m.moveSelection(0, 1)
	case "enter":
		nm, cmd := m.openTile(m.selected)
		return nm, cmd, true
	case "H":
		// Curating the dashboard belongs on the dashboard, not in a
		// text editor: the arrangement is a visual thing.
		m.flash = m.hideSelected()
		return m, nil, true
	case "p":
		// What is installed, and what it puts on the dashboard —
		// including the way back for anything H took off.
		m.plugins = pluginRows(m.reg, m.dash, m.untrusted)
		m.pluginSel, m.pluginScroll = 0, 0
		m.mode = modePlugins
		return m, nil, true
	case "f":
		// The environments, and which one this machine is in. `f`
		// because `p` is the plugin inventory: a profile is a place,
		// not a plugin, and one key for both would be a lie about what
		// they are.
		m.profiles = m.profileRows()
		// The inventory too, though this pane never shows it: the
		// plugin editor one level in is built from it — its `plugin:`
		// completions and every config field it offers. Opening the
		// profiles pane without passing through `p` first used to reach
		// an editor with no fields and nothing to complete.
		m.plugins = pluginRows(m.reg, m.dash, m.untrusted)
		m.profileSel, m.profileScroll = 0, 0
		m.mode = modeProfiles
		return m, nil, true
	case "t":
		nm, cmd := m.startThemeForm()
		return nm, cmd, true
	case "[", "<":
		m.flash = m.moveSelected(-1)
		return m, nil, true
	case "]", ">":
		m.flash = m.moveSelected(1)
		return m, nil, true
	case "c":
		// A tile's own copySpecs value, straight off its current
		// preview — the same "c" a result already open answers to
		// (resultKeys), reached here without opening the tile first.
		if m.selected > 0 && m.selected < len(m.tiles) {
			t := m.tiles[m.selected]
			if spec, ok := copySpecs[t.cap.ID]; ok {
				nm, cmd := m.copyOrPick(spec, t.cap, t.view, modeDashboard)
				return nm, cmd, true
			}
		}
		return m, nil, true
	default:
		// Selected-tile actions: one key opens a sibling capability
		// (e.g. `a` on the note tile opens the add form). A bare Read
		// action skips open's form and runs on its defaults — openTile's
		// own fast path, for the tile promise that names it: "press w to
		// answer" has to land on the queue, not on a form for the optional
		// remote-flow inputs the local queue never needs.
		if a, ok := m.selectedAction(msg.String()); ok {
			m.origin = modeDashboard
			m.trail = nil
			if a.bare && a.cap.Safety == plugin.Read {
				m.current = a.cap
				m.lastValues, m.lastYes = nil, false
				m.row = 0
				m.refreshPending = false
				return m, m.startRun(a.cap, nil, false), true
			}
			nm, cmd := m.open(a.cap)
			return nm, cmd, true
		}
	}
	return m, nil, true
}

func (m Model) pluginsKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit, true
	case "esc", "p":
		m.mode = modeDashboard
		m.tickGen++
		return m, refreshTiles(m.tiles, m.tickGen, m.pluginCfg, m.profileFor), true
	case "up", "k":
		m.pluginSel = max(m.pluginSel-1, 0)
		m.clampPluginScroll(m.pluginBodyHeight())
		return m, nil, true
	case "down", "j":
		m.pluginSel = min(m.pluginSel+1, len(m.plugins)-1)
		m.clampPluginScroll(m.pluginBodyHeight())
		return m, nil, true
	case " ", "space", "x", "enter":
		if msg.String() != "enter" {
			m.flash = m.toggleShown(m.pluginSel)
			return m, nil, true
		}
		// Enter is about the capabilities rather than the tile: drop
		// the namespace into the search bar, which is where every one
		// of them is a keystroke away.
		if m.pluginSel < len(m.plugins) {
			// An artifact that was never launched declared nothing, so
			// the search would open on a namespace with no rows under
			// it — a dead end that reads as a broken catalogue rather
			// than as a decision waiting to be made.
			if row := m.plugins[m.pluginSel]; !row.usable() {
				m.flash = untrustedNote(row)
				return m, nil, true
			}
			m.mode = modeDashboard
			m.selected = 0
			m.searchEditing = true
			m.query = m.plugins[m.pluginSel].plugin.Name + "."
			m.searchSel = 0
			m.clampScroll()
		}
		m.tickGen++
		return m, refreshTiles(m.tiles, m.tickGen, m.pluginCfg, m.profileFor), true
	case "t":
		// The trust decision, taken where the digest and the artifact
		// path are already on the screen.
		m.flash = m.trustSelected()
		return m, nil, true
	case "a":
		// The credential decision, one permission further along than `t`
		// and deliberately not shaped like it: this opens a form rather
		// than acting, because a plugin declares several locations and a
		// bare key would hand over every one of them from a cursor
		// position. See pluginallow.go.
		if m.pluginSel < len(m.plugins) {
			nm, cmd := m.startAllowForm(m.plugins[m.pluginSel])
			return nm, cmd, true
		}
		return m, nil, true
	case "c":
		if m.pluginSel < len(m.plugins) {
			// The form is built from the inputs a plugin declares, and
			// an untrusted one has declared nothing — so this would
			// open an empty form and write a `plugins:` section that
			// nothing reads.
			if row := m.plugins[m.pluginSel]; !row.usable() {
				m.flash = untrustedNote(row)
				return m, nil, true
			}
			nm, cmd := m.startConfigForm(m.plugins[m.pluginSel])
			return nm, cmd, true
		}
		return m, nil, true
	}
	return m, nil, true
}

func (m Model) profilesKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	// The armed half of the delete gate — see Model.armedDelete. Only `y`
	// fires; every other key disarms and is *spent* doing so, because a
	// cancel that also moved the cursor or opened a form would make the safe
	// answer do something. Swallowing everything is also what freezes the
	// selection between the two presses, so what `y` deletes is exactly what
	// the bar named. ctrl+c stays an exit rather than a cancel: an emergency
	// key that sometimes means "no, the other thing" is not an emergency key.
	if m.armedDelete != "" {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit, true
		}
		fire := msg.String() == "y"
		m.armedDelete = ""
		if fire {
			m.flash = m.deleteSelectedProfile()
			m.profiles = m.profileRows()
			m.profileSel = min(m.profileSel, max(len(m.profiles)-1, 0))
			m.clampProfileScroll(m.profileBodyHeight())
			return m, m.syncActive(), true
		}
		return m, nil, true
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit, true
	case "esc", "f":
		nm, cmd := m.backToDashboard()
		return nm, cmd, true
	case "up", "k":
		m.profileSel = max(m.profileSel-1, 0)
		m.clampProfileScroll(m.profileBodyHeight())
		return m, nil, true
	case "down", "j":
		m.profileSel = min(m.profileSel+1, max(len(m.profiles)-1, 0))
		m.clampProfileScroll(m.profileBodyHeight())
		return m, nil, true
	case "u":
		// The switch is the one action here that changes what every
		// other screen shows, so it re-binds and re-runs the tiles
		// rather than waiting for the next visit to the dashboard.
		m.flash = m.useSelectedProfile()
		m.profiles = m.profileRows()
		return m, m.syncActive(), true
	case "n":
		nm, cmd := m.startProfileForm("")
		return nm, cmd, true
	case "enter", "right", "l":
		if m.profileSel < len(m.profiles) {
			m.profileOpen = m.profiles[m.profileSel].name
			m.connSel, m.connScroll = 0, 0
			m.mode = modeProfilePlugins
		}
		return m, nil, true
	case "c", "e":
		if m.profileSel < len(m.profiles) {
			nm, cmd := m.startProfileForm(m.profiles[m.profileSel].name)
			return nm, cmd, true
		}
		return m, nil, true
	case "d":
		if m.profileSel < len(m.profiles) {
			m.armedDelete = m.profiles[m.profileSel].name
		}
		return m, nil, true
	case "y":
		// The same predicate the footer offers the key on, so the two cannot
		// disagree — a key that answers where nothing advertises it is what
		// TestEveryKeyAScreenAnswersToIsAdvertised exists to catch, and a key
		// whose only answer is "there was nothing to do" is the dead key this
		// app keeps removing. The band above has already said whether this
		// environment needs a credential.
		if !m.selectedProfileHasUnsetCredential() {
			return m, nil, true
		}
		m.flash = m.copyExportLine()
		return m, nil, true
	}
	return m, nil, true
}

func (m Model) profilePluginsKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	// Same two-press gate as profilesKeys, one level down; the reasoning
	// lives there and on Model.armedDelete.
	if m.armedDelete != "" {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit, true
		}
		fire := msg.String() == "y"
		m.armedDelete = ""
		if fire {
			m.flash = m.deleteSelectedConn()
			m.profiles = m.profileRows()
			row, _ := m.openProfile()
			m.connSel = min(m.connSel, max(len(row.conns)-1, 0))
			m.clampConnScroll(m.profileBodyHeight())
			return m, m.syncActive(), true
		}
		return m, nil, true
	}
	row, open := m.openProfile()
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit, true
	case "esc", "left", "h":
		m.profileOpen = ""
		m.mode = modeProfiles
		return m, nil, true
	case "up", "k":
		m.connSel = max(m.connSel-1, 0)
		m.clampConnScroll(m.profileBodyHeight())
		return m, nil, true
	case "down", "j":
		m.connSel = min(m.connSel+1, max(len(row.conns)-1, 0))
		m.clampConnScroll(m.profileBodyHeight())
		return m, nil, true
	case "n":
		nm, cmd := m.startConnForm("")
		return nm, cmd, true
	case "c", "e", "enter":
		if open && m.connSel < len(row.conns) {
			nm, cmd := m.startConnForm(row.conns[m.connSel].key)
			return nm, cmd, true
		}
		return m, nil, true
	case "s":
		nm, cmd := m.startCredentialForm()
		return nm, cmd, true
	case "d":
		if open && m.connSel < len(row.conns) {
			m.armedDelete = row.conns[m.connSel].key
		}
		return m, nil, true
	}
	return m, nil, true
}

func (m Model) browseKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	// While filtering, every key belongs to the filter input.
	if m.list.FilterState() == list.Filtering {
		return m, nil, false
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit, true
	case "esc":
		// Back to the dashboard; restart its refresh loop. With a filter
		// applied, esc belongs to the list, which clears it.
		if m.list.FilterState() == list.Unfiltered {
			m.mode = modeDashboard
			m.tickGen++
			return m, refreshTiles(m.tiles, m.tickGen, m.pluginCfg, m.profileFor), true
		}
	case "enter":
		if item, ok := m.list.SelectedItem().(capItem); ok {
			m.origin = modeBrowse
			nm, cmd := m.open(item.c)
			return nm, cmd, true
		}
	}
	return m, nil, false
}

func (m Model) formKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		nm, cmd := m.closeForm()
		return nm, cmd, true
	case "ctrl+c":
		return m, tea.Quit, true
	case "shift+enter", "alt+enter":
		nm, cmd := m.fastSubmitForm()
		return nm, cmd, true
	case "tab":
		// Tab is the app's one completion key. On the completing fields it
		// may open a socket — the one place a form may, and only because
		// there was nothing left to accept. Everywhere else
		// this hands the key straight to the form, which is what happened
		// before the case existed.
		nm, cmd := m.completeFromCluster(msg)
		return nm, cmd, true
	}
	return m, nil, false
}

func (m Model) themeKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		m.themeForm = nil
		m.mode = modeDashboard
		return m, nil, true
	case "ctrl+c":
		return m, tea.Quit, true
	case "shift+enter", "alt+enter":
		nm, cmd := m.fastSubmitThemeForm()
		return nm, cmd, true
	case "tab":
		// Same key, same rule as the capability form (tabOn): complete what
		// there is to complete, say what is on offer to a box too empty to
		// show a ghost, and otherwise move on.
		nm, cmd := m.tabInTheme(msg)
		return nm, cmd, true
	}
	return m, nil, false
}

func (m Model) copyPickKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		nm, cmd := m.closeCopyPick()
		return nm, cmd, true
	case "ctrl+c":
		return m, tea.Quit, true
	case "shift+enter", "alt+enter":
		nm, cmd := m.fastSubmitCopyPick()
		return nm, cmd, true
	}
	return m, nil, false
}

func (m Model) resultKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
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
				return m, nil, true
			case "down", "j":
				m.row = min(m.row+1, max(len(tbl.Rows)-1, 0))
				m.renderResult()
				return m, nil, true
			}
		}
		for _, a := range capActions(m.reg, m.current.ID) {
			if a.key == msg.String() {
				nm, cmd := m.runAction(a, tbl)
				return nm, cmd, true
			}
		}
		if t, ok := toggleFor(m.current.ID, msg.String()); ok {
			nm, cmd := m.toggleView(t)
			return nm, cmd, true
		}
	}
	switch msg.String() {
	case "q":
		return m, tea.Quit, true
	case "esc":
		if !m.atTop() && len(m.trail) > 0 {
			// A leaf result: back to the view it was opened from.
			nm, cmd := m.reopenTop()
			return nm, cmd, true
		}
		if len(m.trail) > 1 {
			m.trail = m.trail[:len(m.trail)-1]
			nm, cmd := m.reopenTop()
			return nm, cmd, true
		}
		m.trail = nil
		nm, cmd := m.closeToOrigin()
		return nm, cmd, true
	case "r":
		if m.current.Run != nil && (m.result.view != nil || m.result.err != nil) {
			// Re-run with the same inputs; destructive confirmation
			// carries over from the original explicit approval.
			return m, m.startRun(m.current, m.lastValues, m.lastYes), true
		}
	case "e":
		// Edit inputs and run again: the boxes open on what this
		// result was produced with, and the view toggles the form has
		// no field for travel beside them.
		if m.current.Run != nil && hasInputs(m.current) {
			nm, cmd := m.startForm(m.current, m.lastValues)
			return nm, cmd, true
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
				return m, tea.SetClipboard(string(raw)), true
			}
		}
	case "c":
		// Unlike "y", this is not available everywhere — only a
		// capability named in copySpecs, with a result shaped the way
		// it declares, has a value to copy at all.
		if spec, ok := copySpecs[m.current.ID]; ok {
			nm, cmd := m.copyOrPick(spec, m.current, m.result.view, modeResult)
			return nm, cmd, true
		}
	case "ctrl+c":
		return m, tea.Quit, true
	}
	return m, nil, false
}

func (m Model) runningKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit, true
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
			nm, cmd := m.reopenTop()
			return nm, cmd, true
		}
		nm, cmd := m.closeToOrigin()
		return nm, cmd, true
	}
	return m, nil, true
}

// wheel scrolls whatever is under it. People try it before they look for a
// key, and a pane that ignores it reads as stuck. Every mode that sets
// MouseMode in View needs a case here, or it has claimed the events
// precisely so it can throw them away.
func (m Model) wheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
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
	case modeProfiles:
		switch msg.Button {
		case tea.MouseWheelUp:
			m.profileSel = max(m.profileSel-1, 0)
		case tea.MouseWheelDown:
			m.profileSel = min(m.profileSel+1, max(len(m.profiles)-1, 0))
		}
		m.clampProfileScroll(m.profileBodyHeight())
		return m, nil
	case modeProfilePlugins:
		row, _ := m.openProfile()
		switch msg.Button {
		case tea.MouseWheelUp:
			m.connSel = max(m.connSel-1, 0)
		case tea.MouseWheelDown:
			m.connSel = min(m.connSel+1, max(len(row.conns)-1, 0))
		}
		m.clampConnScroll(m.profileBodyHeight())
		return m, nil
	}
	return m, nil
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
	case "alt+backspace", "ctrl+w":
		// Option+Delete on macOS arrives as alt+backspace, and ctrl+w is the
		// same stroke at every readline prompt. A query box that ignores both
		// makes somebody erase a mistyped capability name one letter at a
		// time. Deleting stops at the dots IDs are built from, not only at
		// spaces, so one press on "kube.metrics.pre" leaves "kube.metrics."
		// rather than nothing — the way word-delete walks any path-shaped
		// value.
		if m.query == "" {
			m.searchEditing = false
			return m, nil
		}
		m.query = trimLastWord(m.query)
		m.searchSel = 0
		return m, nil
	case "ctrl+u":
		m.query = ""
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

// trimLastWord removes the query's trailing word and the separators that led
// to it, treating the dot and dash of a capability ID as separators alongside
// the space a summary match may carry.
func trimLastWord(s string) string {
	sep := func(r rune) bool { return r == ' ' || r == '.' || r == '-' }
	r := []rune(s)
	i := len(r)
	for i > 0 && sep(r[i-1]) {
		i--
	}
	for i > 0 && !sep(r[i-1]) {
		i--
	}
	return string(r[:i])
}
