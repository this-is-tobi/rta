package app

import (
	"strings"
	"testing"
)

// `rta --help` used to be one alphabetical list of thirty-odd commands,
// `agent` beside `audit` beside `cert`, with nothing saying that a third of
// them are the boundary — what an agent may reach — and another third are
// setup. Every root command now sits under one of three headings, and a
// command left out of them lands back in cobra's unnamed group, which is the
// old list coming back one entry at a time.
func TestEveryRootCommandIsGrouped(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	groups := map[string][]string{}
	for _, c := range root.Commands() {
		if c.Hidden {
			continue
		}
		groups[c.GroupID] = append(groups[c.GroupID], c.Name())
	}
	if orphans := groups[""]; len(orphans) > 0 {
		t.Errorf("commands under no heading: %v", orphans)
	}
	agents := strings.Join(groups[groupAgents], " ")
	for _, want := range []string{"mcp", "grant", "agent", "lock", "operator", "policy"} {
		if !strings.Contains(" "+agents+" ", " "+want+" ") {
			t.Errorf("%s is not under the agents heading: %v", want, groups[groupAgents])
		}
	}
	for _, want := range []string{"sys", "net", "kv", "audit", "git"} {
		if !strings.Contains(" "+strings.Join(groups[groupCapabilities], " ")+" ", " "+want+" ") {
			t.Errorf("%s is not under the capabilities heading: %v", want, groups[groupCapabilities])
		}
	}
	for _, want := range []string{"init", "config", "plugin", "doctor", "explain", "profile", "use", "completion", "help"} {
		if !strings.Contains(" "+strings.Join(groups[groupSetup], " ")+" ", " "+want+" ") {
			t.Errorf("%s is not under the setup heading: %v", want, groups[groupSetup])
		}
	}
}

// And the headings actually reach the screen, in the order the product tells
// its story: what it can do, what it lets an agent do, how to set it up.
func TestTheRootHelpShowsTheThreeHeadings(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "--help")
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(out)
	last := -1
	for _, heading := range []string{"capabilities", "agents and consent", "setup"} {
		at := strings.Index(lower, heading)
		if at < 0 {
			t.Errorf("--help has no %q heading:\n%s", heading, out)
			continue
		}
		if at < last {
			t.Errorf("%q comes before the heading that should precede it", heading)
		}
		last = at
	}
}
