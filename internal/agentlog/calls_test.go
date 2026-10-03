package agentlog

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// Calls is what Read(0) was used for by a counter: the same labels, in the
// same order, over every segment, with a line that does not parse skipped
// rather than ending the walk.
func TestCallsSeesWhatReadSees(t *testing.T) {
	isolate(t)
	small(t, 2<<10, 20)
	for i := 0; i < 24; i++ {
		write(t, Entry{Cap: "sys.cpu", Agent: []string{"a", "b"}[i%2], Outcome: []Outcome{Ran, Refused, Failed}[i%3],
			Auth: Open, Args: map[string]any{"pad": strings.Repeat("x", 100)}})
	}
	files, err := Segments()
	if err != nil || len(files) < 2 {
		t.Fatalf("want a record over several segments, got %v, %v", files, err)
	}
	f, err := os.OpenFile(files[0], os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, werr := f.WriteString("not json at all\n\n")
	if cerr := f.Close(); werr != nil || cerr != nil {
		t.Fatal(werr, cerr)
	}

	entries, err := Read(0)
	if err != nil {
		t.Fatal(err)
	}
	var got []Call
	n, err := Calls(func(c Call) { got = append(got, c) })
	if err != nil {
		t.Fatal(err)
	}
	if n != len(entries) || len(got) != len(entries) {
		t.Fatalf("Calls saw %d (returned %d), Read saw %d", len(got), n, len(entries))
	}
	for i, e := range entries {
		if want := (Call{e.Cap, e.Agent, e.Outcome, e.Auth}); got[i] != want {
			t.Fatalf("call %d = %+v, want %+v", i, got[i], want)
		}
	}
}

// What Calls is for: counting a record without holding it. The control is
// Read(0), which does hold it, so the measurement can be seen to tell the two
// apart; the claim is that the live heap at the last entry of the walk has not
// grown with the record. Counted in bytes the collector reports as live after
// a collection, never in time, so a loaded machine cannot fail it.
func TestCallsKeepsNothingOfTheRecordItWalks(t *testing.T) {
	isolate(t)
	const total = 400
	pad := strings.Repeat("x", 4<<10)
	for i := 0; i < total; i++ {
		write(t, Entry{Cap: "sys.cpu", Outcome: Ran, Auth: Open, Args: map[string]any{"pad": pad}})
	}
	live := func() int64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return int64(m.HeapAlloc)
	}

	before := live()
	entries, err := Read(0)
	if err != nil || len(entries) != total {
		t.Fatalf("Read(0) = %d entries, %v", len(entries), err)
	}
	held := live() - before
	runtime.KeepAlive(entries)
	if held < total*(4<<10)/2 {
		t.Fatalf("holding the record grew the live heap by %d bytes: the control cannot tell holding from not holding", held)
	}

	before = live()
	var during int64
	seen := 0
	if _, err := Calls(func(Call) {
		if seen++; seen == total {
			during = live() - before
		}
	}); err != nil || seen != total {
		t.Fatalf("Calls saw %d, %v", seen, err)
	}
	t.Logf("live heap: holding the record %d bytes, walking it %d", held, during)
	if during > held/4 {
		t.Errorf("the live heap had grown by %d bytes by the last entry, against %d for holding the record: Calls is keeping what it walks", during, held)
	}
}
