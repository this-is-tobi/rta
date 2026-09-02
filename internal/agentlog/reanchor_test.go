package agentlog

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// Reanchor exists for exactly one fault and must refuse every other, so most
// of what is worth testing here is what it declines to do. A repair that
// could also quiet an edited line would be a tool for erasing the finding the
// record exists to produce.

func threeEntries(t *testing.T) {
	t.Helper()
	write(t,
		Entry{Cap: "sys.cpu", Outcome: Ran, Auth: Open},
		Entry{Cap: "kv.get", Outcome: Refused, Auth: Blocked, Reason: "no grant"},
		Entry{Cap: "sys.mem", Outcome: Ran, Auth: Open},
	)
}

// The repairable case: the entries are all intact and only the note saying
// where they stop is gone.
func TestReanchorRestoresARecordThatLostOnlyItsMark(t *testing.T) {
	isolate(t)
	threeEntries(t)
	if err := os.Remove(headPath()); err != nil {
		t.Fatal(err)
	}

	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken == 0 || !rep.Unanchored {
		t.Fatalf("a record with no mark reported broken=%d unanchored=%v, want a repairable fault",
			rep.Broken, rep.Unanchored)
	}

	seq, err := Reanchor()
	if err != nil {
		t.Fatalf("Reanchor: %v", err)
	}
	if seq != 3 {
		t.Errorf("re-anchored at %d, want the last entry 3", seq)
	}
	rep, err = Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken != 0 {
		t.Errorf("after re-anchoring the record still reads broken at %d (%s)", rep.Broken, rep.Why)
	}
	if rep.Entries != 3 {
		t.Errorf("entries = %d, want the 3 that were there — re-anchoring must not drop any", rep.Entries)
	}
}

// **The refusal that matters most.** An edited line is evidence; a repair
// that could overwrite the mark on top of it would turn this command into the
// one an attacker reaches for.
func TestReanchorRefusesToPaperOverAnEditedLine(t *testing.T) {
	isolate(t)
	threeEntries(t)

	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	var e Entry
	if err := json.Unmarshal([]byte(lines[1]), &e); err != nil {
		t.Fatal(err)
	}
	e.Outcome, e.Reason = Ran, ""
	edited, _ := json.Marshal(e)
	lines[1] = string(edited)
	if err := os.WriteFile(Path(), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Reanchor(); !errors.Is(err, ErrNothingToRepair) {
		t.Fatalf("Reanchor on an edited record returned %v, want ErrNothingToRepair", err)
	}
	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken != 2 {
		t.Errorf("the edit is no longer reported (broken=%d) — the finding was erased", rep.Broken)
	}
}

// Entries removed from the end are also evidence: the mark is what proves
// they were ever there, so rewriting it is exactly the cover-up.
func TestReanchorRefusesARecordShorterThanItsMark(t *testing.T) {
	isolate(t)
	threeEntries(t)

	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if err := os.WriteFile(Path(), []byte(strings.Join(lines[:2], "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken == 0 {
		t.Fatal("a truncated record read as whole")
	}
	if rep.Unanchored {
		t.Fatal("a truncated record was marked repairable — the mark is what proves the loss")
	}
	if _, err := Reanchor(); !errors.Is(err, ErrNothingToRepair) {
		t.Fatalf("Reanchor on a truncated record returned %v, want ErrNothingToRepair", err)
	}
}

// Nothing to do is its own answer, not a silent rewrite of a mark that is
// already correct.
func TestReanchorRefusesARecordThatIsAlreadyWhole(t *testing.T) {
	isolate(t)
	threeEntries(t)
	if _, err := Reanchor(); !errors.Is(err, ErrNothingToRepair) {
		t.Fatalf("Reanchor on a whole record returned %v, want ErrNothingToRepair", err)
	}
}

// An empty record has no tip to anchor to, and must not invent one.
func TestReanchorRefusesAnEmptyRecord(t *testing.T) {
	isolate(t)
	if _, err := Reanchor(); !errors.Is(err, ErrNothingToRepair) {
		t.Fatalf("Reanchor on an empty record returned %v, want ErrNothingToRepair", err)
	}
}
