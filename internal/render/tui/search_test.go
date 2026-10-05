package tui

import (
	"reflect"
	"strings"
	"testing"

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
