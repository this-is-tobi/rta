package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The dashboard's bar and the catalogue's box are two screens asking one
// question, and the answer is one function's: the same items in, the same
// indexes out, whichever of them asks.
func TestRankItemsPutsAnIDThatStartsWithTheQueryFirst(t *testing.T) {
	items := []searchItem{
		{ID: "agent.deny", Summary: "Deny one parked call"},
		{ID: "gen.password", Summary: "Generate a password"},
		{ID: "git.log", Summary: "The commit history, generated newest first"},
		{ID: "gen.uuid", Summary: "A random identifier"},
	}
	got := rankItems("gen", items)
	if len(got) != 4 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("rankItems(gen) = %v, want gen.password and gen.uuid first, then the other two", got)
	}
	if inside := map[int]bool{got[2]: true, got[3]: true}; !inside[0] || !inside[2] {
		t.Errorf("rankItems(gen) ends %v, want agent.deny and git.log, which only have it inside", got[2:])
	}
}

func TestRankItemsNeedsEveryWordAndNothingForABlankQuery(t *testing.T) {
	items := []searchItem{
		{ID: "net.hosts.list", Summary: "The hosts file, one row per entry"},
		{ID: "net.hosts.add", Summary: "Add a line to the hosts file"},
	}
	if got := rankItems("  Hosts   LIST ", items); !reflect.DeepEqual(got, []int{0}) {
		t.Errorf("two words in any case found %v, want only net.hosts.list", got)
	}
	for _, blank := range []string{"", "   "} {
		if got := rankItems(blank, items); len(got) != 0 {
			t.Errorf("%q found %v, want nothing", blank, got)
		}
	}
}

// The bar and the box ask the matcher `rta explain` asks, so a word is a word
// of the ID before it is a few letters of a sentence: `ports` is the scan, not
// the capability whose summary says "exports".
func TestRankItemsAsksTheSharedMatcher(t *testing.T) {
	items := []searchItem{
		{ID: "kv.env", Summary: "Exports the store as environment variables"},
		{ID: "net.port", Summary: "Check which ports are open"},
	}
	if got := rankItems("ports", items); len(got) == 0 || got[0] != 1 {
		t.Errorf("rankItems(ports) = %v, want net.port first", got)
	}
}

// A keyword names a capability in the person's own vocabulary, and the search
// found nothing for `todo` or `ssl` until the items carried them.
func TestRankItemsFindsAKeywordTheIDAndSummaryLack(t *testing.T) {
	items := []searchItem{
		{ID: "sys.cpu", Summary: "Processor load"},
		{ID: "note.list", Summary: "The notes", Keywords: []string{"todo"}},
	}
	if got := rankItems("todo", items); !reflect.DeepEqual(got, []int{1}) {
		t.Errorf("rankItems(todo) = %v, want the note list", got)
	}
}

func TestACapabilitysKeywordsReachTheSearchItems(t *testing.T) {
	c := plugin.Capability{ID: "cert.expiry", Summary: "When a certificate ends", Keywords: []string{"ssl"}}
	if got := itemOf(c); !reflect.DeepEqual(got.Keywords, []string{"ssl"}) {
		t.Errorf("itemOf kept the keywords %v, want ssl", got.Keywords)
	}
	got := itemOfTarget(filterTarget(c))
	if got.ID != c.ID || got.Summary != c.Summary || !reflect.DeepEqual(got.Keywords, c.Keywords) {
		t.Errorf("the catalogue's filter target reads back as %+v", got)
	}
	if got := itemOfTarget(filterTarget(plugin.Capability{ID: "a.b", Summary: "s t"})); got.ID != "a.b" ||
		got.Summary != "s t" || len(got.Keywords) != 0 {
		t.Errorf("a capability with no keywords reads back as %+v", got)
	}
}

// A query that finds nothing is offered what it most likely misspells, as the
// hint for an unknown command does, and a short one is not: `pg` is no typo of
// anything.
func TestRankItemsOffersWhatALongQueryMisspellsAndNothingForAShortOne(t *testing.T) {
	items := []searchItem{
		{ID: "sys.cpu", Summary: "Processor load"},
		{ID: "sys.mem", Summary: "Memory"},
	}
	if got := rankItems("cpuu", items); len(got) == 0 || got[0] != 0 {
		t.Errorf("rankItems(cpuu) = %v, want sys.cpu", got)
	}
	if got := rankItems("cq", items); len(got) != 0 {
		t.Errorf("rankItems(cq) = %v, want nothing", got)
	}
}

// Over the real catalogue, which is what a person types into.
func TestTheDashboardSearchAnswersWhatTheDocsQuote(t *testing.T) {
	m, _ := realModel(t, 100, 40)
	for query, want := range map[string]string{"ports": "net.port", "cpuu": "sys.cpu", "gen": "gen."} {
		m.query = query
		found := m.searchResults()
		if len(found) == 0 || !strings.HasPrefix(found[0].ID, want) {
			ids := make([]string, 0, len(found))
			for _, c := range found {
				ids = append(ids, c.ID)
			}
			t.Errorf("%q finds %v, want %s first", query, ids, want)
		}
	}
}

// A word that names a first-party plugin nobody has installed gets the
// install command, as it does at the command line: for "postgres" the box
// would otherwise say "no matches", and for "pg" it would offer only the
// package upgrade whose name happens to contain the letters.
func TestTheSearchNamesTheInstallForAFirstPartyPluginThatIsNotHere(t *testing.T) {
	m, _ := realModel(t, 100, 40)
	for _, query := range []string{"postgres", "pg"} {
		m.query = query
		box := ansi.Strip(m.renderSearchTile(100, false))
		if !strings.Contains(box, "`rta plugin install pg` installs it") {
			t.Errorf("%q: the search box does not name the install:\n%s", query, box)
		}
	}
	// The plugin is the first word, or what comes before the first dot: a
	// person reaching for a capability types it whole.
	for _, query := range []string{"pg.query", "pg status", "postgres query"} {
		m.query = query
		if m.searchHint() == "" {
			t.Errorf("%q: the query is a capability of a plugin that is not here, and says nothing about installing it", query)
		}
	}
	m.query = "zzzq"
	if box := ansi.Strip(m.renderSearchTile(100, false)); !strings.Contains(box, "no matches") || strings.Contains(box, "install") {
		t.Errorf("a word that is no plugin name changed:\n%s", box)
	}
	m.query = "ports"
	if box := ansi.Strip(m.renderSearchTile(100, false)); strings.Contains(box, "install") {
		t.Errorf("a query for something that is here is told to install:\n%s", box)
	}
}

// A first-party plugin that is on this machine and was not started is not one
// to install: the command line says to approve it or where the reason it failed
// is, and the search told the same person to fetch what they already have.
func TestTheSearchSaysWhatTheCommandLineSaysForAnInstalledPluginThatIsNotRunning(t *testing.T) {
	m, _ := realModel(t, 130, 40)
	m.query = "qdrant"

	m.untrusted = []pluginhost.Untrusted{{Name: "qdrant", Path: "/opt/bin/rta-plugin-qdrant"}}
	box := ansi.Strip(m.renderSearchTile(130, false))
	if !strings.Contains(box, "has not been approved to run") || !strings.Contains(box, "`rta plugin trust qdrant`") ||
		strings.Contains(box, "installs it") {
		t.Errorf("a plugin waiting for approval:\n%s\nwant the approval and not an install", box)
	}

	m.untrusted = nil
	m.failed = []pluginhost.Failed{{Name: "qdrant", Path: "/opt/bin/rta-plugin-qdrant", Reason: "exited 3: boom"}}
	box = ansi.Strip(m.renderSearchTile(130, false))
	if !strings.Contains(box, "installed and failed to start") || !strings.Contains(box, "`rta plugin list` says why") ||
		strings.Contains(box, "installs it") {
		t.Errorf("a plugin that failed to start:\n%s\nwant where the reason is and not an install", box)
	}
}

// A capability ID typed whole is scored against the words after the plugin's
// name, and the other plugins' capabilities that share them filled the window:
// `qdrant.collection.list` showed redis.client.list and left out the only line
// that said why qdrant was not there. The hint keeps a line of its own.
func TestTheSearchKeepsItsHintWhenOtherCapabilitiesFillTheWindow(t *testing.T) {
	m, _ := realModel(t, 130, 40)
	m.untrusted = []pluginhost.Untrusted{{Name: "qdrant", Path: "/opt/bin/rta-plugin-qdrant"}}
	m.query = "qdrant.collection.list"
	if n := len(m.searchResults()); n <= searchMatches {
		t.Fatalf("the query finds %d capabilities; want more than the %d that fit, or this proves nothing", n, searchMatches)
	}
	box := ansi.Strip(m.renderSearchTile(130, false))
	if !strings.Contains(box, "`rta plugin trust qdrant`") {
		t.Errorf("the box drops the hint when results fill it:\n%s", box)
	}
	if lines := strings.Count(box, "\n") + 1; lines != m.searchTileHeight() {
		t.Errorf("the box is %d lines tall with a hint and a full list, want the %d it is always drawn at", lines, m.searchTileHeight())
	}
	if !strings.Contains(box, "1/") {
		t.Errorf("the count of what is not shown is gone:\n%s", box)
	}
}

// Plugins and profiles are panes, not capabilities, so the words a first-run
// person types for them found nothing — or, for "install plugin", a package
// manager's capability that reads as the install feature. The box says where
// they are.
func TestTheSearchSaysWhereThePluginAndProfilePanesAre(t *testing.T) {
	m, _ := realModel(t, 130, 40)
	for query, want := range map[string]string{
		"plugin":         "press p",
		"plugins":        "press p",
		"installs":       "press p",
		"install plugin": "press p",
		"profile":        "press f",
		"profiles":       "press f",
		"environment":    "press f",
		"environments":   "press f",
	} {
		m.query = query
		box := ansi.Strip(m.renderSearchTile(130, false))
		if !strings.Contains(box, want) || strings.Contains(box, "no matches") {
			t.Errorf("%q:\n%s\nwant where the pane is (%s) and not \"no matches\"", query, box, want)
		}
	}
	// `index` is the first word of a search about a database's indexes, and the
	// plugin pane is not what it asks for: it would have answered with a line
	// about plugins and taken one of the few result rows from the matches.
	for _, query := range []string{"zzzq", "index", "index bloat"} {
		m.query = query
		if box := ansi.Strip(m.renderSearchTile(130, false)); strings.Contains(box, "press p") || strings.Contains(box, "press f") {
			t.Errorf("%q: a word that is neither pane named one:\n%s", query, box)
		}
	}
}

// The window was three lines at every size, so a terminal sixty lines high
// showed three matches over forty lines of tiles nobody was looking at. It is a
// fifth of the height, at least three and at most ten, and everything that
// measures the dashboard under it — the box as drawn, the hit test, the room the
// tiles have, the count in the border, the page a key moves by — follows.
func TestTheSearchBoxHoldsAsManyMatchesAsTheTerminalCanSpare(t *testing.T) {
	for _, tc := range []struct{ w, h, slots int }{
		{80, 14, 3}, {80, 24, 4}, {100, 30, 6}, {132, 45, 9}, {200, 60, 10}, {200, 100, 10},
	} {
		m, _ := realModel(t, tc.w, tc.h)
		m.query, m.searchEditing = "list", true
		results := m.searchResults()
		if len(results) <= tc.slots {
			t.Fatalf("%dx%d: the query finds %d capabilities; want more than the %d that fit, or this proves nothing", tc.w, tc.h, len(results), tc.slots)
		}
		if got := m.searchRows(); got != tc.slots {
			t.Errorf("%dx%d: the box shows %d matches, want %d", tc.w, tc.h, got, tc.slots)
		}
		box := ansi.Strip(m.renderSearchTile(tc.w, true))
		if lines := strings.Count(box, "\n") + 1; lines != searchChrome+tc.slots || lines != m.searchHeight() {
			t.Errorf("%dx%d: the box is %d lines, want %d, which is what the layout under it is measured from (%d)", tc.w, tc.h, lines, searchChrome+tc.slots, m.searchHeight())
		}
		for i, c := range results {
			if shown := strings.Contains(box, c.ID+" "); shown != (i < tc.slots) {
				t.Errorf("%dx%d: match %d (%s) is shown: %v, want %v", tc.w, tc.h, i+1, c.ID, shown, i < tc.slots)
			}
		}
		if want := fmt.Sprintf("1/%d", len(results)); !strings.Contains(box, want) {
			t.Errorf("%dx%d: the border does not count the matches it does not show (%s):\n%s", tc.w, tc.h, want, box)
		}

		frame := frameLines(m)
		if len(frame) > tc.h || !strings.HasPrefix(frame[1+m.searchHeight()], "╭") {
			t.Errorf("%dx%d: the first tile is not drawn right under the box (%d lines, frame %d):\n%s", tc.w, tc.h, m.searchHeight(), len(frame), strings.Join(frame, "\n"))
		}
		if got := m.tileAt(5, m.searchHeight()); got != 0 {
			t.Errorf("%dx%d: a click on the box's last line lands on tile %d, want the search", tc.w, tc.h, got)
		}
		if got := m.tileAt(5, 1+m.searchHeight()); got != 1 {
			t.Errorf("%dx%d: a click on the first tile's border lands on tile %d, want 1", tc.w, tc.h, got)
		}

		paged, _ := m.updateSearch(tea.KeyPressMsg{Code: tea.KeyPgDown})
		if got := paged.(Model).searchSel; got != tc.slots {
			t.Errorf("%dx%d: page down moved the selection to %d, want a window's worth, %d", tc.w, tc.h, got, tc.slots)
		}
		m.searchSel = tc.slots
		if window, first := m.searchWindow(results); len(window) != tc.slots || first != 1 {
			t.Errorf("%dx%d: with the match after the window selected it shows %d from %d, want %d from 1", tc.w, tc.h, len(window), first, tc.slots)
		}
	}
}
