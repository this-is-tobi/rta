package agentlog

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func recentBlockOf(t *testing.T, size int64) {
	t.Helper()
	old := recentBlock
	recentBlock = size
	t.Cleanup(func() { recentBlock = old })
}

// Reading a segment backwards in blocks must answer exactly what filtering
// the whole record answers, at every block size — including ones smaller than
// a line, where the carried half of a line has to grow across several blocks,
// and ones that land a boundary on a newline — and across a rotation.
func TestRecentReadingBackwardsAgreesWithFilteringTheWholeRecord(t *testing.T) {
	isolate(t)
	small(t, 4<<10, 20)
	now := time.Now()
	const total = 40
	for i := 0; i < total; i++ {
		at := now.Add(-time.Duration(total-i) * 10 * time.Minute)
		write(t, Entry{At: at, Cap: "sys.cpu", Outcome: Ran, Auth: Open,
			Args: map[string]any{"pad": strings.Repeat("x", 40+i*7)}})
	}
	if files, _ := Segments(); len(files) < 2 {
		t.Fatalf("the record is one segment, a rotation was meant to be crossed: %v", files)
	}
	all, err := Read(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, since := range []time.Time{
		now.Add(-24 * time.Hour), now.Add(-3 * time.Hour), now.Add(-time.Hour), now.Add(-35 * time.Minute), now.Add(time.Hour),
	} {
		var want []int64
		for _, e := range all {
			if !e.At.Before(since) {
				want = append(want, e.Seq)
			}
		}
		for _, block := range []int64{7, 64, 300, 301, 1 << 10, 5 << 10, 256 << 10} {
			recentBlockOf(t, block)
			got, err := Recent(since)
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(seqList(got)) != fmt.Sprint(want) {
				t.Errorf("Recent(%v ago) with %d-byte blocks = %v, want %v", time.Since(since).Round(time.Minute), block, seqList(got), want)
			}
		}
	}
}

// The reason Recent reads backwards: the dashboard asks for the last hour of a
// record whose newest segment holds days, every few seconds, and used to parse
// all of the segment to count the last few rows. Counted rather than timed.
func TestRecentDoesNotParseWhatIsOlderThanTheWindow(t *testing.T) {
	isolate(t)
	now := time.Now()
	pad := strings.Repeat("x", 4<<10)
	const total = 600
	for i := 0; i < total; i++ {
		write(t, Entry{At: now.Add(-time.Duration(total-i) * time.Minute), Cap: "sys.cpu", Outcome: Ran, Auth: Open,
			Args: map[string]any{"pad": pad}})
	}
	// Half a minute inside the fifth, because the record stamps to the second.
	since := now.Add(-5*time.Minute - 30*time.Second)
	if got, err := Recent(since); err != nil || len(got) != 5 {
		t.Fatalf("Recent = %d entries, %v; want the last five minutes' 5", len(got), err)
	}
	const budget = 2000
	allocs := testing.AllocsPerRun(5, func() {
		if _, err := Recent(since); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > budget {
		t.Errorf("Recent allocated %.0f times for five recent rows among %d, over %d: it is parsing the whole segment", allocs, total, budget)
	}
}
