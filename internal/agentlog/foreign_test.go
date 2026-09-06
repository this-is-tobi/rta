package agentlog

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/seal"
)

// What a writer that cannot read can do to the record by *creating* files.
//
// The package comment states the threat model as "a writer that cannot read":
// a confined plugin is denied every read of the data directory and
// is not denied a write. The chain answers a writer who appends a flattering
// line, and the high-water mark answers one who truncates. Neither of them
// looked at the thing a writer can do most cheaply of all — put a *new file*
// in the directory under a name rta would mistake for its own.
//
// Retention was driven by counting files that match `agent-log.NNNNN.jsonl`,
// so the count was whatever the directory said it was.

// forge drops a file into the data directory under a name rta uses for its
// own rolled segments. It holds nothing readable, which is the cheapest thing
// to write and the hardest to notice.
func forge(t *testing.T, n int, body string) string {
	t.Helper()
	p := segmentPath(n)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func pad() Entry {
	return Entry{Cap: "sys.cpu", Outcome: Ran, Auth: Open,
		Args: map[string]any{"pad": strings.Repeat("x", 200)}}
}

// workload runs the same traffic twice over: once undisturbed, once with
// meddle() called partway through. A control is the only way to read this,
// because the second half of the traffic rotates and retires on its own —
// "more was retired than before" is true of an untouched record too.
func workload(t *testing.T, keep int, meddle func(*testing.T)) Report {
	t.Helper()
	isolate(t)
	small(t, 1<<10, keep)
	fill(t, 30)
	if meddle != nil {
		meddle(t)
	}
	fill(t, 10)
	rep, err := Verify()
	if err != nil {
		t.Fatalf("the record became unreadable: %v", err)
	}
	return rep
}

func TestFilesRtaNeverWroteCannotDriveItsRetention(t *testing.T) {
	// The attack. rta keeps `keepSegments` full files; the attacker creates
	// that many more, so the next rotation counts far too many and retires the
	// oldest — which are the real ones. Each is anchored on the way out, so
	// afterwards the record verifies clean and reports the loss as rta's own
	// retention: history erased, and the erasure laundered.
	//
	// Aimed both high and low. Under the sealed high-water these were two
	// different exclusions and only the high one could be named; the file's
	// own tail answers both the same way, so rta now says so either way —
	// which is worth having, because a file pretending to be part of the
	// record is worth looking into whatever it failed to achieve.
	control := workload(t, 3, nil)

	for _, tc := range []struct {
		name  string
		first int
		named int
	}{
		{"above anything rta has rolled", 90000, 8},
		{"over numbers rta has already retired", 1, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := workload(t, 3, func(t *testing.T) {
				for i := 0; i < 8; i++ {
					forge(t, tc.first+i, "not a record\n")
				}
			})
			if got.Broken != 0 {
				// Also a failure, and a different one: a file rta did not
				// write must not be able to make it accuse itself either.
				t.Fatalf("a foreign file broke the chain at %d: %s", got.Broken, got.Why)
			}
			if got.Retired != control.Retired {
				t.Fatalf("files rta never wrote changed how much history it retired: "+
					"%d against a control of %d — and it verifies, so it reads as rta's own retention",
					got.Retired, control.Retired)
			}
			if got.Entries != control.Entries {
				t.Fatalf("the record holds %d entries against a control of %d",
					got.Entries, control.Entries)
			}
			// And where rta can say so, it does: something wrote a file
			// pretending to be part of the ledger, which is worth looking into
			// whatever it failed to achieve.
			if len(got.Foreign) != tc.named {
				t.Fatalf("Verify names %d foreign files, want %d: %v",
					len(got.Foreign), tc.named, got.Foreign)
			}
		})
	}
}

func TestTheBoundHoldsBeforeTheFirstRotation(t *testing.T) {
	// Found by running the fix against the shipped binary rather than against
	// a test: `rta doctor` said nothing about four planted files, because the
	// segment number was only sealed when a rotation raised it. A record that
	// has never rolled had no bound at all — and on the shipped policy the
	// first roll is some twenty-eight thousand calls away, so "fresh install"
	// and "never" are the same state for most machines. It is also the state
	// in which the record is smallest and a plant is most effective.
	//
	// Zero is a real answer: rta has rolled nothing, so nothing named like a
	// segment is its own.
	isolate(t)
	fill(t, 5)
	if segs, _ := Segments(); len(segs) != 1 {
		t.Fatalf("this test needs a record that has never rotated, got %v", segs)
	}
	for i := 1; i <= 3; i++ {
		forge(t, i, "not a record\n")
	}
	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Foreign) != 3 {
		t.Fatalf("a record that has never rolled names %d of 3 planted files: %v",
			len(rep.Foreign), rep.Foreign)
	}
	if rep.Broken != 0 || rep.Entries != 5 {
		t.Fatalf("the planted files changed the record: %+v", rep)
	}
}

// A record written by an older rta — whose mark carried a sealed segment
// high-water this build no longer reads, and whose entries were sealed over
// the re-marshalled struct rather than the line — is still rta's own record.
//
// It is adopted because its files say so: every segment ends in an entry
// this key verifies, which is the whole of the question now. Nothing has to
// be trusted once and sealed afterwards, which is what the upgrade path this
// replaced existed to do.
func TestARecordFromAnOlderRTAIsStillItsOwn(t *testing.T) {
	isolate(t)
	small(t, 1<<10, 40)
	fill(t, 20)
	key := recordKey(t)
	own, _, _, err := rolled(key)
	if err != nil || len(own) < 2 {
		t.Fatalf("need a rolled record: %v %v", err, own)
	}

	// The mark as the older build wrote it: a segment number this build does
	// not read, and no idea that it should not be there.
	h, ok := readHead(key)
	if !ok {
		t.Fatal("no mark to age")
	}
	aged := map[string]any{"seq": h.Seq, "seal": h.Seal, "at": h.At, "segment": own[len(own)-1]}
	body, err := json.Marshal(aged)
	if err != nil {
		t.Fatal(err)
	}
	var replayed head
	if err := json.Unmarshal(body, &replayed); err != nil {
		t.Fatal(err)
	}
	mac, err := headMAC(key, replayed)
	if err != nil {
		t.Fatal(err)
	}
	replayed.MAC = mac
	body, _ = json.Marshal(replayed)
	if err := os.WriteFile(headPath(), body, 0o600); err != nil {
		t.Fatal(err)
	}

	before, err := Verify()
	if err != nil || before.Broken != 0 {
		t.Fatalf("an older mark broke the record it belongs to: %v %+v", err, before)
	}
	if before.Entries != 20 {
		t.Fatalf("the inherited record holds %d entries, wrote 20", before.Entries)
	}
	if err := Append(pad()); err != nil {
		t.Fatal(err)
	}
	after, err := Verify()
	if err != nil || after.Broken != 0 || after.Entries != 21 {
		t.Fatalf("appending to an inherited record cost it: %v %+v", err, after)
	}
	if len(after.Foreign) != 0 {
		t.Fatalf("rta disowned its own segments: %v", after.Foreign)
	}
	// And a planted file is still somebody else's — including a *parseable*
	// one, which is the case the sealed high-water could not reach: a number
	// at or below the mark was "rta's own", so a planted file that looked
	// like an entry made rta report its own record as broken at entry 1.
	forge(t, own[len(own)-1]+50, "not a record\n")
	forge(t, own[len(own)-1]+51,
		`{"seq":1,"at":"2026-08-01T12:00:00Z","capability":"kv.get","outcome":"ran","auth":"open","prev":"","seal":"deadbeef"}`+"\n")
	rep, _ := Verify()
	if len(rep.Foreign) != 2 {
		t.Fatalf("a planted file was counted as rta's own: %+v", rep.Foreign)
	}
	if rep.Broken != 0 {
		t.Fatalf("a planted file made rta accuse itself: %+v", rep)
	}
}

// recordKey is the key this record is sealed under, for the tests that ask
// the ownership question directly.
func recordKey(t *testing.T) []byte {
	t.Helper()
	key, err := seal.Key(keyFile, false)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// A roll used to need its segment number sealed into the mark *before* the
// rename, or a crash in between left rta's own newest segment looking like
// somebody else's — the trickiest invariant in this package, and one nothing
// in the rename could enforce.
//
// There is nothing to seal now, so the crash window is gone rather than
// narrow: the renamed file is recognised because its last entry verifies,
// which was already true the moment it was written. This is that state,
// reached the only way a crash could reach it.
func TestARollThatCrashedBeforeAnythingElseIsStillTheRecord(t *testing.T) {
	isolate(t)
	small(t, 1<<10, 40)
	fill(t, 20)
	key := recordKey(t)
	own, _, _, err := rolled(key)
	if err != nil {
		t.Fatal(err)
	}
	next := 1
	if len(own) > 0 {
		next = own[len(own)-1] + 1
	}
	// The rename, and then nothing: no mark written, no entry appended.
	if err := os.Rename(Path(), segmentPath(next)); err != nil {
		t.Fatal(err)
	}
	after, _, foreign, err := rolled(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(foreign) != 0 {
		t.Fatalf("rta disowned the segment it had just rolled: %v", foreign)
	}
	if len(after) != len(own)+1 {
		t.Fatalf("segments = %v, want the rolled one included", after)
	}
	// And the record goes on from there, at the right sequence.
	if err := Append(pad()); err != nil {
		t.Fatal(err)
	}
	rep, err := Verify()
	if err != nil || rep.Broken != 0 || rep.Entries != 21 {
		t.Fatalf("appending after the interrupted roll: %v %+v", err, rep)
	}
}

func TestAForeignSegmentCannotStopTheRecordBeingWritten(t *testing.T) {
	// The same write, aimed at availability instead. A file rta counts as a
	// segment holds no readable entry, so reading its last line fails — and
	// retire runs inside rotate, which runs inside Append, so that failure
	// used to come back out as "this call could not be recorded" for every
	// call after the next roll. One write, and the record stops.
	isolate(t)
	small(t, 1<<10, 2)
	fill(t, 20)
	forge(t, 1, "not a record\n")

	for i := 0; i < 20; i++ {
		if err := Append(pad()); err != nil {
			t.Fatalf("append %d after one file rta did not write: %v", i, err)
		}
	}
	rep, err := Verify()
	if err != nil || rep.Broken != 0 {
		t.Fatalf("the record is not intact: %v %+v", err, rep)
	}
}

func TestACorruptSegmentIsLeftAloneRatherThanEndingTheRecord(t *testing.T) {
	// The same property with no attacker in it, which is why it is worth its
	// own test: a segment rta *did* write, damaged by a bad disk or a killed
	// process mid-rename. Retention is housekeeping and must never be the
	// reason a call goes unrecorded — and a file whose end cannot be read is
	// also one that must not be deleted, because the anchor that would record
	// its retirement is exactly what cannot be written for it.
	isolate(t)
	small(t, 1<<10, 2)
	fill(t, 30)

	own, _, _, err := rolled(recordKey(t))
	if err != nil || len(own) == 0 {
		t.Fatalf("no segments to damage: %v %v", err, own)
	}
	damaged := segmentPath(own[0])
	if err := os.WriteFile(damaged, []byte("\x00\x00 half a line"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if err := Append(pad()); err != nil {
			t.Fatalf("append %d over a damaged segment: %v", i, err)
		}
	}
	if _, err := os.Stat(damaged); err != nil {
		t.Fatalf("the damaged segment was deleted without an anchor for it: %v", err)
	}
}
