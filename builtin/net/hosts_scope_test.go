package net

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// net.hosts.toggle declares Scope "hostname", which is the promise that a
// grant naming one name authorizes that name and no other. It flipped whole
// lines.
//
// A hosts line carries as many names as somebody put on it:
//
//	10.0.0.5  api.example.com  metrics.example.com
//
// so `toggle api.example.com` also parked metrics.example.com — a name the
// grant never mentioned, whose resolution changes for every process on the
// machine. The enabling direction is the worse of the two: a parked line reads
// as one disabled entry in `hosts list`, and re-enabling the one name an agent
// was granted brings every other name on that line back with it.
//
// net.hosts.rm, one capability along, already had the right shape — "removes
// the names, and the line only if nothing is left on it" — so this was toggle
// disagreeing with its own sibling rather than an unanswered design question.

const sharedLine = `127.0.0.1	localhost
10.0.0.5 api.example.com metrics.example.com  # the staging box
`

func TestTogglingOneNameLeavesTheOthersOnItsLineAlone(t *testing.T) {
	path := hostsFixture(t, sharedLine)
	run(t, runHostsToggle, map[string]any{"hostname": "api.example.com"})

	after := hostsContent(t, path)
	state := hostStates(t)
	if state["api.example.com"] != "disabled" {
		t.Errorf("api.example.com = %q, want disabled — the name that was named:\n%s",
			state["api.example.com"], after)
	}
	if state["metrics.example.com"] != "active" {
		t.Errorf("metrics.example.com = %q, want active — it was never named, and a grant "+
			"for one hostname must not reach another:\n%s", state["metrics.example.com"], after)
	}
	// The rest of the file is untouched, which is this plugin's whole manner.
	if state["localhost"] != "active" {
		t.Errorf("localhost = %q:\n%s", state["localhost"], after)
	}
	if !strings.Contains(after, "the staging box") {
		t.Errorf("somebody's note was lost:\n%s", after)
	}
}

// The direction that matters more: bringing a name back must not bring its
// line-mates with it.
func TestEnablingOneNameDoesNotEnableTheRestOfItsLine(t *testing.T) {
	path := hostsFixture(t, `127.0.0.1	localhost
# 10.0.0.5 api.example.com bank.internal
`)
	run(t, runHostsToggle, map[string]any{"hostname": "api.example.com"})

	after := hostsContent(t, path)
	state := hostStates(t)
	if state["api.example.com"] != "active" {
		t.Errorf("api.example.com = %q, want active:\n%s", state["api.example.com"], after)
	}
	if state["bank.internal"] != "disabled" {
		t.Errorf("bank.internal = %q, want it left parked — every process on the machine "+
			"resolves it to 10.0.0.5 the moment it comes back:\n%s", state["bank.internal"], after)
	}
}

// A line with one name on it flips in place, which is the ordinary case and
// must not grow a second line.
func TestTogglingTheOnlyNameOnALineRewritesThatLine(t *testing.T) {
	path := hostsFixture(t, `127.0.0.1	localhost
10.0.0.9 api.example.com  # while the staging box is down
`)
	run(t, runHostsToggle, map[string]any{"hostname": "api.example.com"})

	after := hostsContent(t, path)
	if n := strings.Count(after, "api.example.com"); n != 1 {
		t.Errorf("api.example.com appears %d times, want 1:\n%s", n, after)
	}
	if !strings.Contains(after, "# 10.0.0.9 api.example.com") {
		t.Errorf("the entry was not parked in place:\n%s", after)
	}
	if !strings.Contains(after, "while the staging box is down") {
		t.Errorf("the trailing note was lost:\n%s", after)
	}
}

// Toggling back and forth returns to a file that means the same thing. A split
// is a rewrite, and a rewrite that drifts each time is a rewrite nobody can
// trust to be reversible — which is the entire premise of parking an entry.
func TestTogglingTwiceMeansWhatItStartedWith(t *testing.T) {
	hostsFixture(t, sharedLine)
	before := hostStates(t)
	run(t, runHostsToggle, map[string]any{"hostname": "api.example.com"})
	run(t, runHostsToggle, map[string]any{"hostname": "api.example.com"})

	after := hostStates(t)
	for name, state := range before {
		if after[name] != state {
			t.Errorf("%s = %q after a round trip, want %q", name, after[name], state)
		}
	}
	if len(after) != len(before) {
		t.Errorf("names = %v, want %v", after, before)
	}
}

// hostStates reads the file back through the plugin's own list capability, so
// these assertions are about what rta reports rather than about a particular
// spelling on disk.
func hostStates(t *testing.T) map[string]string {
	t.Helper()
	tbl := run(t, runHostsList, nil).(view.Table)
	out := map[string]string{}
	for _, row := range tbl.Rows {
		out[row[0]] = row[2]
	}
	return out
}
