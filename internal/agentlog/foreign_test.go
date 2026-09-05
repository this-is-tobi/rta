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
	// Aimed both high and low, because the two are excluded by different
	// halves of the bound: above the sealed high-water is a number rta has
	// never rolled, and a number it rolled and retired names a file it
	// deleted.
	control := workload(t, 3, nil)

	for _, tc := range []struct {
		name  string
		first int
		// named is how many of the eight Verify should call out. The two
		// halves are closed by different mechanisms and only one of them can
		// name what it excluded: a number above the sealed high-water is
		// provably not rta's, while a number at or below it is one rta did
		// once use, so the file is refused on the ground that it holds no
		// entry rather than on the ground of its name.
		named int
	}{
		{"above anything rta has rolled", 90000, 8},
		{"over numbers rta has already retired", 1, 0},
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

func TestARecordFromBeforeTheBoundIsAdoptedRatherThanDisowned(t *testing.T) {
	// The upgrade. A mark written before segment numbers were sealed has none,
	// and the segments beside it are rta's own — the binary that wrote them
	// had no bound at all, so believing them is the position the record is
	// already in, and refusing them would make rta disown files it wrote.
	// The trust is spent once: the next append seals an answer.
	isolate(t)
	small(t, 1<<10, 40)
	fill(t, 20)
	own, _, err := rolled(bounds())
	if err != nil || len(own) < 2 {
		t.Fatalf("need a rolled record: %v %v", err, own)
	}

	// Strip the segment number, the way a mark from the older build carries
	// none, and re-seal it so it still verifies.
	key, err := seal.Key(keyFile, false)
	if err != nil {
		t.Fatal(err)
	}
	h, ok := readHead(key)
	if !ok {
		t.Fatal("no mark to age")
	}
	h.Seg, h.MAC = nil, ""
	mac, err := headMAC(key, h)
	if err != nil {
		t.Fatal(err)
	}
	h.MAC = mac
	body, _ := json.Marshal(h)
	if err := os.WriteFile(headPath(), body, 0o600); err != nil {
		t.Fatal(err)
	}

	before, err := Verify()
	if err != nil || before.Broken != 0 {
		t.Fatalf("an aged mark broke the record it belongs to: %v %+v", err, before)
	}
	if before.Entries != 20 {
		t.Fatalf("the inherited record holds %d entries, wrote 20", before.Entries)
	}
	// One append settles it, and the segments rta already had are still its
	// own afterwards.
	if err := Append(pad()); err != nil {
		t.Fatal(err)
	}
	if b := bounds(); !b.known || b.limit != own[len(own)-1] {
		t.Fatalf("bound = %+v, want it settled at the highest segment already on disk (%d)",
			b, own[len(own)-1])
	}
	after, err := Verify()
	if err != nil || after.Broken != 0 || after.Entries != 21 {
		t.Fatalf("settling the bound cost the record: %v %+v", err, after)
	}
	// And from here on a planted file is somebody else's.
	forge(t, own[len(own)-1]+50, "not a record\n")
	rep, _ := Verify()
	if len(rep.Foreign) != 1 {
		t.Fatalf("after settling, a planted file is still counted as rta's own: %+v", rep.Foreign)
	}
}

func TestNoSegmentIsRolledThatCouldNotBeSealedFirst(t *testing.T) {
	// The ordering inside rotate, which a probe found untested: the segment
	// number is sealed *before* the rename.
	//
	// Sealing after would leave a window holding rta's own freshly rolled
	// segment above rta's own bound — so the next append would not see it,
	// would take the record's end from the segment before it, and would hand
	// out sequence numbers already in use. Sealing first cannot fail that
	// way: the worst it leaves is a number nothing uses.
	//
	// Made testable by taking the seal away. A directory where the mark
	// belongs is a write that cannot succeed, so this asserts the thing the
	// ordering exists for — a roll rta could not record is a roll rta does
	// not make.
	isolate(t)
	small(t, 1<<10, 40)
	fill(t, 20)
	before, _, err := rolled(bounds())
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(headPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(headPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	// Enough to be well past the roll threshold, so this is a rotation that
	// wants to happen and cannot be sealed.
	for i := 0; i < 20; i++ {
		_ = Append(pad())
	}
	after, _, err := rolled(bound{})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("rta rolled %d segments it could not seal a number for (had %d, now %d)",
			len(after)-len(before), len(before), len(after))
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

	own, _, err := rolled(bounds())
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
