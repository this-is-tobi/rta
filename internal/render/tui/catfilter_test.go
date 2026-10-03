package tui

import (
	"strings"
	"testing"
)

// The catalogue's filter answers the question the dashboard's search does, the
// same way. It used the list's fuzzy matcher, which found "gen" scattered
// through "git.log" and its summary and ranked agent.deny above gen.password:
// sixty-two of a hundred and twenty-eight rows for a three-letter query, the
// one asked for fifth.
func TestTheCatalogueFilterPutsWhatStartsWithTheQueryFirst(t *testing.T) {
	m, _ := realModel(t, 100, 40)
	var targets []string
	for _, it := range m.list.Items() {
		targets = append(targets, it.FilterValue())
	}

	ranks := catalogueFilter("gen", targets)
	if len(ranks) == 0 || len(ranks) > len(targets)/4 {
		t.Fatalf("%q matched %d of %d rows", "gen", len(ranks), len(targets))
	}
	for i, r := range ranks[:3] {
		if id, _, _ := strings.Cut(targets[r.Index], " "); !strings.HasPrefix(id, "gen.") {
			t.Errorf("match %d is %q, not a gen capability", i+1, id)
		}
	}
	for _, r := range ranks {
		if targets[r.Index] == "" {
			t.Error("a section header matched a query")
		}
		if strings.HasPrefix(targets[r.Index], "git.log ") {
			t.Error("git.log matched \"gen\" on scattered letters")
		}
	}

	got := catalogueFilter("hosts list", targets)
	found := false
	for _, r := range got {
		found = found || strings.HasPrefix(targets[r.Index], "net.hosts.list ")
	}
	if !found || len(got) > 3 {
		t.Errorf("two words matched %d rows, and net.hosts.list among them is %v", len(got), found)
	}
	if got := catalogueFilter("  ", targets); len(got) != 0 {
		t.Errorf("a blank query matched %d rows", len(got))
	}
}
