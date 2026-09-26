package agent

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/lockdown"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/view"
)

// An empty queue is a sentence on a screen, the way an empty lock list is —
// not a bordered table with a header row and nothing under it — and a table
// to anything that parses it. The sentence in place of the table was what
// every format got: `-o json | jq '.rows[]'` met a text view, and -o csv
// refused one and exited 2.
func TestAnEmptyQueueIsASentence(t *testing.T) {
	isolate(t)
	v, err := run(t, "agent.pending", nil)
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok || len(tbl.Rows) != 0 || !strings.Contains(tbl.Empty, "nothing is waiting") {
		t.Fatalf("empty queue = %+v, want an empty table saying so", v)
	}
	var out bytes.Buffer
	if err := cli.Render(&out, v, cli.Options{Format: cli.Pretty, NoColor: true, Screen: true}); err != nil ||
		!strings.Contains(out.String(), "nothing is waiting") || strings.Contains(out.String(), "╭") {
		t.Errorf("pretty = %q (%v), want the sentence and no grid", out.String(), err)
	}
	out.Reset()
	if err := cli.Render(&out, v, cli.Options{Format: cli.CSV}); err != nil ||
		strings.TrimSpace(out.String()) != "id,capability,record,safety,profile,would do,expires in" {
		t.Errorf("csv = %q (%v), want the header row alone", out.String(), err)
	}
}

// One lost call reads as one.
//
// These are the two numbers on the record's own summary that say the ledger
// is not whole — the count rta could not write down, and the count its
// retention dropped — and one is the commonest value either takes: a single
// call missed during one bad moment. "1 calls rta could not write down" on
// the screen an operator opens to decide whether the audit trail can be
// trusted reads as a fault in the tool doing the counting.
//
// The entry note below them had the rule and these two did not, in the same
// file: it spelled the singular out in a two-branch if, which is the shape
// the counting vocabulary in pkg/format exists to stop being written again.
func TestOneMissedCallIsNotOneCalls(t *testing.T) {
	isolate(t)
	pairs := recordPairs(agentlog.Report{
		Entries: 4, Missed: 1, Retired: 1, RetiredAt: time.Now(),
	}, nil)
	kv := view.KeyValue{Pairs: pairs}
	for _, c := range []struct{ key, want string }{
		{"not recorded", "1 call rta could not write down"},
		{"retired", "the first 1 call, dropped"},
	} {
		if got := pairValue(kv, c.key); !strings.HasPrefix(got, c.want) {
			t.Errorf("%s = %q, want it to start %q", c.key, got, c.want)
		}
	}

	pairs = recordPairs(agentlog.Report{Entries: 9, Missed: 2, Retired: 3, RetiredAt: time.Now()}, nil)
	kv = view.KeyValue{Pairs: pairs}
	if got := pairValue(kv, "not recorded"); !strings.HasPrefix(got, "2 calls") {
		t.Errorf("two missed calls = %q", got)
	}
	if got := pairValue(kv, "retired"); !strings.HasPrefix(got, "the first 3 calls") {
		t.Errorf("three retired calls = %q", got)
	}
}

func lock(t *testing.T, name string) {
	t.Helper()
	l, verr := lockdown.Build("agent", name, "incident", "", "terminal")
	if verr != nil {
		t.Fatal(verr)
	}
	if verr := lockdown.Add(l); verr != nil {
		t.Fatal(verr)
	}
}

func pairValue(v view.View, key string) string {
	for _, p := range v.(view.KeyValue).Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

// A lock was visible on `lock list` and nowhere else; the dashboard tile
// now names what is frozen, and says "nothing" when nothing is.
func TestTheOverviewNamesWhatIsLocked(t *testing.T) {
	isolate(t)
	v, err := run(t, "agent.overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(v, "locked"); got != "nothing" {
		t.Fatalf("locked = %q on a clean machine", got)
	}
	lock(t, "claude")
	v, err = run(t, "agent.overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(v, "locked"); !strings.HasPrefix(got, "claude") {
		t.Fatalf("locked = %q, want the frozen agent named", got)
	}
}

// The bridge refuses a frozen agent before any other gate, so an answer
// releases nothing while the lock stands. The operator's screen says so
// rather than reporting "allowed" for a call that is about to be refused.
func TestAllowingALockedAgentsCallSaysItIsRefusedAnyway(t *testing.T) {
	isolate(t)
	p, err := consent.Ask(consent.Call{
		Cap: "kv.get", Safety: "write", Scopes: []string{"db-password"}, Agent: "claude",
		Args: map[string]any{"key": "db-password"}, Why: "no active grant",
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	lock(t, "claude")
	v, err := run(t, "agent.allow", map[string]any{"id": p.Request.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(v, "but"); !strings.Contains(got, "claude is locked") {
		t.Fatalf("answer = %+v, want the lock named", v)
	}
}
