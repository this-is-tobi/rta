package agent

import (
	"testing"

	"github.com/this-is-tobi/rta/internal/agentlog"
)

// A call that handed the agent a stored value says so where the eye is, and a
// refusal of the same capability does not: the phrase is for what happened.
func TestACallThatRevealedAValueSaysSoInItsResult(t *testing.T) {
	ran := agentlog.Entry{Cap: "kv.get", Outcome: agentlog.Ran, Auth: agentlog.Standing, Revealed: true}
	if got, want := resultPhrase(ran), "ran · by grant · revealed"; got != want {
		t.Errorf("a revealing call reads %q, want %q", got, want)
	}
	plain := ran
	plain.Revealed = false
	if got := resultPhrase(plain); got != "ran · by grant" {
		t.Errorf("a call that revealed nothing reads %q", got)
	}
}

// The filter keeps what handed an agent a value and nothing else, and it keeps
// a scattered subset of the record, so the read is widened for it as for a
// refusal filter.
func TestTheRevealedFilterKeepsOnlyTheCallsThatRevealed(t *testing.T) {
	f := logFilter{revealed: true}
	if !f.picks() || !f.any() {
		t.Fatal("the revealed filter is not a pick of the record")
	}
	if !f.keeps(agentlog.Entry{Seq: 1, Outcome: agentlog.Ran, Revealed: true}) {
		t.Error("a call that revealed was dropped")
	}
	if f.keeps(agentlog.Entry{Seq: 2, Outcome: agentlog.Ran}) {
		t.Error("a call that revealed nothing was kept")
	}
}

// The column is there when a row has something to say in it, as role and code
// are, and absent from a record with no reveal in it.
func TestTheRevealedColumnAppearsOnlyWhenARowRevealed(t *testing.T) {
	names := func(rows []agentlog.Entry) []string {
		var out []string
		for _, c := range logColumns(rows, false, true) {
			out = append(out, c.Name)
		}
		return out
	}
	has := func(cols []string) bool {
		for _, c := range cols {
			if c == "revealed" {
				return true
			}
		}
		return false
	}
	quiet := []agentlog.Entry{{Cap: "sys.cpu", Outcome: agentlog.Ran}}
	if has(names(quiet)) {
		t.Error("a record with no reveal in it has a revealed column")
	}
	loud := append(quiet, agentlog.Entry{Cap: "kv.get", Outcome: agentlog.Ran, Revealed: true})
	cols := logColumns(loud, false, true)
	if !has(names(loud)) {
		t.Fatal("a record with a reveal in it has no revealed column")
	}
	for _, c := range cols {
		if c.Name != "revealed" {
			continue
		}
		if got := c.cell(loud[1], ""); got != "yes" {
			t.Errorf("the revealing row reads %q", got)
		}
		if got := c.cell(loud[0], ""); got != "-" {
			t.Errorf("the other row reads %q", got)
		}
	}
}
