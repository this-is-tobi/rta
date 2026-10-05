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
	if got, want := rankItems("gen", items), []int{1, 3, 0, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("rankItems(gen) = %v, want %v: what starts with it, then what only has it inside", got, want)
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
