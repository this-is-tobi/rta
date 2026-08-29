package agentlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/seal"
)

// Rotation, and the one thing it must not cost: the chain
// decision 8). A deleted segment and a deleted line leave the same
// evidence — a gap — so everything below is about telling rta's own
// retention apart from somebody else's deletion.

// small shrinks the retention policy so a test can drive several rotations
// without writing sixty-four megabytes to do it.
func small(t *testing.T, segment int64, keep int) {
	t.Helper()
	oldSeg, oldKeep := maxSegment, keepSegments
	maxSegment, keepSegments = segment, keep
	t.Cleanup(func() { maxSegment, keepSegments = oldSeg, oldKeep })
}

// fill writes n entries, each padded so that a handful fills a segment.
func fill(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := Append(Entry{
			Cap: "sys.cpu", Outcome: Ran, Auth: Open,
			Args: map[string]any{"pad": strings.Repeat("x", 200)},
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
}

func TestTheChainRunsThroughARotation(t *testing.T) {
	isolate(t)
	small(t, 1<<10, 40) // ~3 entries a segment, and keep enough that none retires
	fill(t, 30)

	files, err := Segments()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 3 {
		t.Fatalf("30 entries produced %d files, so nothing rotated", len(files))
	}
	if filepath.Base(files[len(files)-1]) != file {
		t.Fatalf("the last segment is %s, want the file being written", files[len(files)-1])
	}
	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken != 0 {
		t.Fatalf("rotation broke the chain at entry %d: %s", rep.Broken, rep.Why)
	}
	if rep.Entries != 30 {
		t.Fatalf("the record holds %d entries, wrote 30", rep.Entries)
	}
	if rep.Files != len(files) {
		t.Fatalf("Verify counted %d files, there are %d", rep.Files, len(files))
	}
	if rep.Retired != 0 {
		t.Fatalf("nothing should have been retired yet, report says %d", rep.Retired)
	}
	// Sequence numbers do not restart per file, or "entry 4" would name
	// several different calls.
	all, err := Read(0)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range all {
		if e.Seq != int64(i+1) {
			t.Fatalf("entry %d carries seq %d — the count restarted at a rotation", i, e.Seq)
		}
	}
}

func TestRetiredHistoryStillVerifies(t *testing.T) {
	isolate(t)
	small(t, 1<<10, 2)
	fill(t, 40)

	files, _ := Segments()
	if len(files) > 4 {
		t.Fatalf("%d files survive a keep-2 policy", len(files))
	}
	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken != 0 {
		t.Fatalf("retiring old history broke the chain at %d: %s", rep.Broken, rep.Why)
	}
	if rep.Retired == 0 {
		t.Fatal("nothing was reported as retired, but files were dropped")
	}
	if rep.RetiredAt.IsZero() {
		t.Fatal("the report does not say when history was dropped")
	}
	// The surviving record picks up exactly where the retired history left
	// off, so "what is missing" is answerable rather than merely absent.
	all, _ := Read(0)
	if len(all) == 0 || all[0].Seq != rep.Retired+1 {
		t.Fatalf("the record starts at %d, retirement ended at %d", all[0].Seq, rep.Retired)
	}
	if int64(rep.Entries)+rep.Retired != 40 {
		t.Fatalf("%d kept plus %d retired is not the 40 written", rep.Entries, rep.Retired)
	}
}

func TestASegmentDeletedByHandIsNotRetirement(t *testing.T) {
	// The whole point of the anchor: removing a file has to look different
	// from rta removing it.
	isolate(t)
	small(t, 1<<10, 8)
	fill(t, 30)

	files, _ := Segments()
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken == 0 {
		t.Fatal("a segment deleted by hand left the record looking whole")
	}
	if !strings.Contains(rep.Why, "retired") {
		t.Fatalf("why = %q, want it to say nothing records the gap as retired", rep.Why)
	}
}

func TestAForgedAnchorCannotLaunderADeletion(t *testing.T) {
	// An anchor is the one document that says "this gap is fine", so forging
	// one is how a deletion would be laundered. It is sealed under the same
	// key as the entries, and an unverifiable one is dropped rather than
	// believed.
	isolate(t)
	small(t, 1<<10, 8)
	fill(t, 30)

	files, _ := Segments()
	gone, err := lastEntryIn(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	// Everything an attacker can compute without the key: the right sequence
	// number, the right seal copied from the entry they are erasing.
	body, _ := json.Marshal(anchor{
		Seq: gone.Seq, Seal: gone.Seal, At: time.Now().UTC(),
		File: filepath.Base(files[0]), MAC: strings.Repeat("ab", 32),
	})
	f, err := os.OpenFile(retiredPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(append(body, '\n'))
	f.Close()

	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken == 0 {
		t.Fatal("a forged retirement record made a deletion verify")
	}
	if !strings.Contains(rep.Why, "do not verify") {
		t.Fatalf("why = %q, want it to name the unverifiable retirement record", rep.Why)
	}
}

func TestAnAnchorWrittenBeforeItsFileWentIsHarmless(t *testing.T) {
	// retire() writes the anchor and then removes the file, so a crash in
	// between leaves an anchor for a segment that is still there. That must
	// cost nothing: verification starts from the earliest entry it can
	// actually see and never consults an anchor it does not need.
	isolate(t)
	small(t, 1<<10, 40)
	fill(t, 30)

	files, _ := Segments()
	last, err := lastEntryIn(files[0])
	if err != nil {
		t.Fatal(err)
	}
	key, err := keyForTest()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAnchor(key, anchor{
		Seq: last.Seq, Seal: last.Seal, At: time.Now().UTC().Truncate(time.Second),
		File: filepath.Base(files[0]),
	}); err != nil {
		t.Fatal(err)
	}
	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken != 0 {
		t.Fatalf("a stale retirement record broke a whole chain at %d: %s", rep.Broken, rep.Why)
	}
	if rep.Retired != 0 {
		t.Fatalf("a segment that is still here was reported as retired (%d)", rep.Retired)
	}
}

func TestReadingBackwardsCrossesSegments(t *testing.T) {
	isolate(t)
	small(t, 1<<10, 40)
	fill(t, 30)

	files, _ := Segments()
	if len(files) < 3 {
		t.Fatalf("only %d files, the read never has to cross one", len(files))
	}
	got, err := Read(12)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 12 {
		t.Fatalf("Read(12) returned %d", len(got))
	}
	// Newest last, contiguous, ending at the newest entry there is.
	for i, e := range got {
		if e.Seq != int64(30-12+1+i) {
			t.Fatalf("entry %d is seq %d, want a contiguous run ending at 30: %v", i, e.Seq, seqs(got))
		}
	}
	// And asking for more than exists is not an error.
	all, err := Read(500)
	if err != nil || len(all) != 30 {
		t.Fatalf("Read(500) = %d entries, %v", len(all), err)
	}
}

func TestRotationSurvivesAnEmptyActiveFile(t *testing.T) {
	// The moment after a roll the active file does not exist. Appending then
	// has to continue the chain from the newest segment rather than start a
	// second one at sequence 1.
	isolate(t)
	small(t, 1<<10, 40)
	fill(t, 10)
	// The state a crash between the rename and the next write leaves: the
	// entries are all in segments and nothing is being written to yet.
	// Through the same two steps rta takes, in the same order — the sealed
	// segment number goes up before the rename, so a crash here leaves a
	// record whose newest file rta still recognises as its own.
	key, err := seal.Key(keyFile, true)
	if err != nil {
		t.Fatal(err)
	}
	nums, _, err := rolled(bounds())
	if err != nil {
		t.Fatal(err)
	}
	next := 1
	if len(nums) > 0 {
		next = nums[len(nums)-1] + 1
	}
	if err := bumpSegment(key, next); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(Path(), segmentPath(next)); err != nil {
		t.Fatal(err)
	}
	if err := Append(Entry{Cap: "sys.mem", Outcome: Ran, Auth: Open}); err != nil {
		t.Fatal(err)
	}
	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken != 0 {
		t.Fatalf("appending after a roll broke the chain at %d: %s", rep.Broken, rep.Why)
	}
	if rep.Entries != 11 {
		t.Fatalf("%d entries, want 11", rep.Entries)
	}
}

func TestTheRetiredListIsNotMistakenForASegment(t *testing.T) {
	// agent-log-retired.jsonl sits beside agent-log.00001.jsonl, and reading
	// it as a segment would feed anchors to the chain walker as entries.
	isolate(t)
	small(t, 1<<10, 2)
	fill(t, 40)

	files, err := Segments()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range files {
		if strings.Contains(filepath.Base(p), "retired") {
			t.Fatalf("the retirement list was read as a segment: %s", p)
		}
	}
	if _, err := os.Stat(retiredPath()); err != nil {
		t.Fatalf("nothing was retired, so this test proves nothing: %v", err)
	}
}

// The gap rotation's own tests uncovered: a chain shows an edited line and
// a line taken out of the middle, and shows nothing about lines taken off
// the end. The promise is that "an edited or removed line is visible", and
// for a truncation that was not true.

func TestTruncatingTheRecordIsVisible(t *testing.T) {
	isolate(t)
	write(t,
		Entry{Cap: "sys.cpu", Outcome: Ran, Auth: Open},
		Entry{Cap: "kv.get", Outcome: Refused, Auth: Blocked, Reason: "no grant"},
		Entry{Cap: "kv.get", Outcome: Ran, Auth: Live},
	)
	// The cheapest possible cover-up, and the one that needs no read at all:
	// drop the last line and every remaining line still verifies.
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
		t.Fatal("a truncated record verified — the last call erased itself")
	}
	if !strings.Contains(rep.Why, "removed from the end") {
		t.Fatalf("why = %q, want it to name the truncation", rep.Why)
	}
}

func TestEmptyingTheRecordEntirelyIsVisible(t *testing.T) {
	// The same attack taken to its conclusion: delete every segment. There
	// is no chain left to break, so the mark is the only thing that speaks.
	isolate(t)
	small(t, 1<<10, 40)
	fill(t, 12)
	files, _ := Segments()
	for _, p := range files {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken == 0 {
		t.Fatal("deleting the whole record left it looking like a machine that had never run an agent")
	}
}

func TestRemovingTheMarkIsItselfVisible(t *testing.T) {
	// An attacker who cannot forge the mark can still delete it, and a
	// missing mark has to read as "nothing says where this ends" rather than
	// as a clean bill of health.
	isolate(t)
	write(t, Entry{Cap: "sys.cpu", Outcome: Ran, Auth: Open})
	if err := os.Remove(headPath()); err != nil {
		t.Fatal(err)
	}
	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken == 0 {
		t.Fatal("a record with no high-water mark verified")
	}
	if !strings.Contains(rep.Why, "supposed to end") {
		t.Fatalf("why = %q", rep.Why)
	}
}

func TestAForgedMarkIsNotBelieved(t *testing.T) {
	isolate(t)
	write(t,
		Entry{Cap: "sys.cpu", Outcome: Ran, Auth: Open},
		Entry{Cap: "kv.get", Outcome: Ran, Auth: Live},
	)
	// Truncate, then re-point the mark at the entry left behind — everything
	// an attacker can do without the key.
	raw, _ := os.ReadFile(Path())
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if err := os.WriteFile(Path(), []byte(lines[0]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var kept Entry
	if err := json.Unmarshal([]byte(lines[0]), &kept); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(head{
		Seq: kept.Seq, Seal: kept.Seal, At: time.Now().UTC(), MAC: strings.Repeat("cd", 32),
	})
	if err := os.WriteFile(headPath(), body, 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken == 0 {
		t.Fatal("a forged high-water mark made a truncation verify")
	}
}

func TestAMarkOneEntryBehindIsNotAnAlarm(t *testing.T) {
	// Append writes the entry and then the mark, so a crash in between
	// leaves the mark one entry short. That is the deliberate direction: it
	// under-reports rather than accusing somebody of a deletion that never
	// happened.
	isolate(t)
	write(t, Entry{Cap: "sys.cpu", Outcome: Ran, Auth: Open})
	key, err := keyForTest()
	if err != nil {
		t.Fatal(err)
	}
	before, ok := readHead(key)
	if !ok {
		t.Fatal("no mark after an append")
	}
	write(t, Entry{Cap: "sys.mem", Outcome: Ran, Auth: Open})
	// Put the older mark back, as a crash would have left it.
	body, _ := json.Marshal(before)
	if err := os.WriteFile(headPath(), body, 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken != 0 {
		t.Fatalf("a stale mark reported a break at %d: %s", rep.Broken, rep.Why)
	}
	if rep.Last != 2 {
		t.Fatalf("the report says the record ends at %d", rep.Last)
	}
}

func TestABurstIsRecordedInFull(t *testing.T) {
	// The go-sdk gives every tools/call its own goroutine, and the file lock
	// underneath is a spin with a sleep — fine for the two or three waiters
	// it was built for, hopeless as a queue. A live burst of 300 calls lost
	// 241 of them, and the chain reported itself whole throughout, because a
	// dropped append leaves no gap to find.
	isolate(t)
	const burst = 300
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if err := Append(Entry{
				Cap: "sys.cpu", Outcome: Ran, Auth: Open,
				Args: map[string]any{"n": i},
			}); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Entries != burst {
		t.Fatalf("%d of %d simultaneous calls were recorded", rep.Entries, burst)
	}
	if rep.Broken != 0 {
		t.Fatalf("the burst broke the chain at %d: %s", rep.Broken, rep.Why)
	}
	if rep.Missed != 0 {
		t.Fatalf("%d calls were lost", rep.Missed)
	}
}

func TestARecordThatLostSomethingSaysSo(t *testing.T) {
	// The honest failure. record() never fails a call over the ledger, so a
	// write that cannot happen has to be admitted by the next one that can —
	// otherwise the sequence closes over the absence and nothing anywhere
	// says a call went unrecorded.
	isolate(t)
	write(t, Entry{Cap: "sys.cpu", Outcome: Ran, Auth: Open})

	// A directory where the file should be: every append fails, and none of
	// them may fail quietly.
	if err := os.Remove(Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(Path(), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := Append(Entry{Cap: "kv.get", Outcome: Ran, Auth: Open}); err == nil {
			t.Fatal("an append into a directory succeeded")
		}
	}
	if err := os.Remove(Path()); err != nil {
		t.Fatal(err)
	}

	if err := Append(Entry{Cap: "sys.mem", Outcome: Ran, Auth: Open}); err != nil {
		t.Fatal(err)
	}
	got, err := Read(0)
	if err != nil {
		t.Fatal(err)
	}
	last := got[len(got)-1]
	if last.Missed != 3 {
		t.Fatalf("the entry after three lost calls admits %d", last.Missed)
	}
	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Missed != 3 {
		t.Fatalf("the report counts %d lost calls", rep.Missed)
	}
	if rep.Broken != 0 {
		t.Fatalf("admitting a loss broke the chain at %d: %s", rep.Broken, rep.Why)
	}
	// And the admission is not repeated on every entry afterwards.
	if err := Append(Entry{Cap: "sys.mem", Outcome: Ran, Auth: Open}); err != nil {
		t.Fatal(err)
	}
	got, _ = Read(1)
	if got[0].Missed != 0 {
		t.Fatalf("the next entry repeated the claim: %d", got[0].Missed)
	}
}

func seqs(es []Entry) []int64 {
	out := make([]int64, len(es))
	for i, e := range es {
		out[i] = e.Seq
	}
	return out
}
