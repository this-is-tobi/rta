package tui

import (
	"reflect"
	"testing"
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
