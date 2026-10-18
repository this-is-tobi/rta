package agent

import (
	"slices"
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

// The column is in the full table whatever the rows hold, as every column of
// it is, so that a shipper finds the same fields in every batch.
func TestTheRevealedColumnIsInTheFullTableWhateverTheRowsHold(t *testing.T) {
	quiet := []agentlog.Entry{{Cap: "sys.cpu", Outcome: agentlog.Ran}}
	loud := append(slices.Clone(quiet), agentlog.Entry{Cap: "kv.get", Outcome: agentlog.Ran, Revealed: true})
	revealed := func(rows []agentlog.Entry) (col logColumn, ok bool) {
		for _, c := range logColumns(rows, false, true) {
			if c.Name == "revealed" {
				return c, true
			}
		}
		return logColumn{}, false
	}
	if _, ok := revealed(quiet); !ok {
		t.Error("a record with no reveal in it has no revealed column")
	}
	col, ok := revealed(loud)
	if !ok {
		t.Fatal("a record with a reveal in it has no revealed column")
	}
	if got := col.cell(loud[1], ""); got != "yes" {
		t.Errorf("the revealing row reads %q", got)
	}
	if got := col.cell(loud[0], ""); got != "-" {
		t.Errorf("the other row reads %q", got)
	}
}
