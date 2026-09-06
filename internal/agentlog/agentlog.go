// Package agentlog is the record of what agents did with rta: one line per
// call arriving over MCP, chained so that an edited or missing line is
// visible.
//
// Only the network surfaces are recorded: calls arriving over MCP, and the
// remote operator channel's mutations — a revoked or issued grant, an
// answered consent, a lock placed or lifted — because those change the
// authority every other line is read against, and "who changed it" is the
// first question an incident review asks of this file. The operator's own
// CLI and TUI calls are not recorded — this answers "what happened while I
// was away", and mixing in the commands the operator typed themselves
// would double the file and blur the only question it exists to answer.
// The channel's reads stay off the record for the polling reason: a
// watching dashboard asks for status every few seconds, and recording
// polls would churn real history out of retention.
//
// The chain's threat model is internal/seal's: it
// stops a writer that cannot read (a confined plugin appending a flattering
// line cannot make it verify) and it stops nothing running at the
// operator's own uid, which can read the key and rewrite the whole file.
// That is tamper-evidence against accident and against confined code, never
// proof against a determined local attacker — and saying so is the point,
// because a log that claims more than it delivers is worse than no log.
//
// # Rotation, and why the chain survives it
//
// The record is a series of files, not one: `agent-log.jsonl` is the one
// being written, and `agent-log.00001.jsonl` upward are the ones already
// full. Sequence numbers never restart and the first entry of a new file
// carries the last entry's seal from the file before it, so the chain runs
// through the whole series exactly as it ran through one file.
//
// Dropping the oldest file is the part that needs care, because a deleted
// segment and a deleted *line* leave the same evidence — a gap — and one of
// those is rta's own retention while the other is what this file exists to
// catch. So a segment is never simply removed: its last entry's sequence
// and seal are written first to `agent-log-retired.jsonl` as a sealed
// anchor, which is what a later verification links the surviving chain
// back to. History rta dropped on purpose verifies; history somebody else
// dropped does not.
//
// # Where the record ends
//
// A chain shows an edited line and a line removed from the middle, and on
// its own it shows nothing at all about lines removed from the *end*: a
// truncated file is a shorter file that still verifies, which would let the
// confined plugin erase its own trail by deleting rather than
// by writing. So the last entry's sequence and seal are also kept in
// `agent-log-head.json`, sealed under the same key. Rolling the record back
// now means forging that too, and forging it needs the key that reading is
// denied.
//
// # Which files are the record
//
// Rotation and retention were driven by asking the directory which files
// matched `agent-log.NNNNN.jsonl`, and that made the retention policy
// something anybody who could create a file could drive. Creating a file is
// the cheapest write there is, and the naming scheme is in this source, so a
// writer that cannot read — the whole threat model above — could plant eight
// files under unused numbers, watch the next rotation count far too many
// segments, and have rta retire the oldest, which are the real ones. Each was
// anchored on the way out, so afterwards the record verified clean and
// reported the loss as rta's own retention: history erased, and the erasure
// laundered through the mechanism built to tell the two apart. The same write
// aimed at a low number instead stopped the record being written at all,
// because a segment whose last entry cannot be read failed retire, and retire
// runs inside Append.
//
// So the answer is sealed rather than observed. The high-water mark carries
// the highest segment number rta has ever rolled, and a file numbered above
// it was not written by rta. Below it, a name rta did once use, the question
// is answered by content instead: a file holding no readable entry is not
// counted toward retention and is never deleted — not counted, so it cannot
// push a real segment out; never deleted, because the anchor that would
// record its retirement is derived from the entry that cannot be read, and a
// segment removed without an anchor is the gap all of this exists to notice.
package agentlog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/filelock"
	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/internal/seal"
)

const (
	file        = "agent-log.jsonl"
	keyFile     = "agent-log.key"
	retiredFile = "agent-log-retired.jsonl"
	headFile    = "agent-log-head.json"
	// maxLine bounds one entry, so a capability with enormous arguments
	// cannot write a line nothing will read back.
	maxLine = 16 << 10
	// maxField bounds one string field of an entry that is already over
	// maxLine — enough for any message a person reads, and ten of them
	// still fit the line.
	maxField = 1 << 10
	// tailWindow is how much of a file's end is read to find its last
	// entry. Entries are a few hundred bytes; this is generous.
	tailWindow = 64 << 10
	// The file lock's three durations, named because their order is easy to
	// read wrong: the second version of this call passed 30s expecting it to
	// be the timeout, and it is the staleness threshold — the timeout was
	// the 3s at the end, which is what a burst then blew through.
	lockStale   = 15 * time.Second
	lockRetry   = 5 * time.Millisecond
	lockTimeout = 10 * time.Second
)

// appendMu serializes appends inside this process.
//
// The file lock is for the other `rta mcp serve` processes sharing a data
// directory, and it is a spin with a sleep between tries — fine for the two
// or three waiters it was built for, and hopeless as a queue. The go-sdk
// dispatches every tools/call in its own goroutine, so a pipelined burst
// put hundreds of goroutines on that spin at once: they drained at roughly
// one per retry interval and the rest timed out, which cost 241 of 300
// calls their place in the record while the chain went on reporting itself
// whole. A mutex makes the queue a queue, and leaves exactly one goroutine
// per process contending for the file.
var appendMu sync.Mutex

// missed counts records this process could not write.
//
// A dropped append leaves no gap to find: sequence numbers come from the
// last entry actually written, so the record simply closes over the
// absence. The count rides on the next entry that does get written, which
// is the difference between a log that lost something and a log that says
// so.
var missed atomic.Int64

// The retention policy, as two numbers.
//
// maxSegment is how big the file being written grows before it is rolled
// aside: at roughly 300 bytes a call, some 28,000 calls — small enough that
// reading one segment back is instant, large enough that a busy day does
// not produce a directory listing. keepSegments is how many full files are
// kept beside it, so the record settles at about 64 MB.
//
// Retention is a policy and this is a default, but a default is what almost
// everybody runs: the pair is chosen so that a machine left alone for a
// year holds a bounded amount of somebody's command history rather than an
// unbounded one. What is dropped is said out loud — `rta doctor` and `rta
// agent log --detail` both report how much history has been retired and
// when — because a log that quietly forgets is a log somebody will trust
// for the period it no longer covers.
//
// Variables rather than constants so the tests can drive a rotation without
// writing sixty-four megabytes to do it. Nothing outside this package
// writes to them.
var (
	maxSegment   int64 = 8 << 20
	keepSegments       = 7
)

// Outcome is what happened to a call, from the operator's point of view.
type Outcome string

const (
	// Ran: the handler ran and returned a view.
	Ran Outcome = "ran"
	// Failed: the handler ran and returned an error — the call was allowed,
	// the work did not succeed.
	Failed Outcome = "failed"
	// Refused: a gate would not let the call through. Reason carries which
	// one, and Auth carries where in the stack it stood: blocked means the
	// call never cleared the authority gate, while open or grant on a
	// refused row means authority allowed it and the handler's own policy
	// gate (a localOnly capability probed over MCP, a credential-minting
	// verb refusing agents) still said no.
	Refused Outcome = "refused"
)

// Authorization is how a call came to be allowed, which is the column an
// operator reads down when they want to know what they consented to.
type Authorization string

const (
	// Open: the capability needed no grant (a plain read).
	Open Authorization = "open"
	// Standing: an existing grant covered it.
	Standing Authorization = "grant"
	// Live: the operator answered a parked request.
	Live Authorization = "approved"
	// Denied: the operator answered, and the answer was no.
	Denied Authorization = "declined"
	// Blocked: refused before any question could be asked.
	Blocked Authorization = "blocked"
	// Operator: a roster-enrolled operator's signed call over the remote
	// channel — authorized by enrollment and an ed25519 signature, never by
	// a grant. Rows carrying it are the channel's mutations; Credential
	// names which key, in the same operator:<label> form grants and locks
	// attribute themselves with.
	Operator Authorization = "operator"
)

// Entry is one recorded call.
//
// Field order is the order they are written in; the JSON is what an
// operator greps, so it stays readable rather than compact.
type Entry struct {
	Seq  int64     `json:"seq"`
	At   time.Time `json:"at"`
	Cap  string    `json:"capability"`
	Tool string    `json:"tool,omitempty"`
	// Args are the arguments as the caller sent them, already redacted and
	// model-cleaned by the bridge — the same treatment the result gets, for
	// the same reason: this file is read by people and by the next agent
	// that greps it.
	Args map[string]any `json:"args,omitempty"`
	// Agent is the name the operator launched this server under (`rta mcp
	// serve --as claude-desktop`), empty when they did not name it. It is the
	// same string a grant compares against, so "what may this agent do" and
	// "what did it do" are answered about the same principal.
	Agent string `json:"agent,omitempty"`
	// Client is the name and version the caller announced for *itself* in the
	// MCP initialize handshake — "claude-ai 0.1.0", "cursor-vscode 1.2".
	//
	// **Recorded beside Agent and never instead of it**, because a name a
	// thing chooses for itself is not an identity: anything that can
	// speak the protocol can say it is Claude. It is provenance, exactly as
	// kv's Origin is — useful for reading the record back, useless for
	// deciding anything, and worth keeping precisely because it is the field
	// that shows an operator they have a second agent before they have got
	// round to naming it.
	//
	// A row where the two disagree is the interesting row: a client asserting
	// one name on a server launched as another is either a reconfigured
	// client or something worth looking at, and the record can only show that
	// if it keeps both.
	Client string `json:"client,omitempty"`
	// Credential names which bearer credential authenticated this call, over
	// the HTTP transport only — the static token's own label, or an OIDC
	// subject. Empty over stdio, where there is nothing on the wire to name:
	// Agent there is the operator's word and nothing else backs it.
	//
	// A third field beside Agent and Client for the reason Client exists
	// beside Agent: a remote instance's token file or OIDC audience can admit
	// more than one holder, and without this every one of them logs as the
	// identical Agent. This is the field that tells them apart after the
	// fact, which Agent alone cannot once more than one credential is valid
	// for it.
	Credential string `json:"credential,omitempty"`
	// Session is the short id of the server process that wrote this entry
	// (internal/session). Three Claude Code windows registered under the
	// same --as name are three servers and one principal; grants and the
	// ceiling apply to the principal, and this is what tells the three
	// apart when reading the record back. Provenance, like Client: nothing
	// decides on it.
	Session string `json:"session,omitempty"`
	// Role names the role the covering grant was issued under, and
	// RoleIssued when that grant was issued, so a role issued at 09:00 and
	// re-issued at 14:00 read apart. Provenance, like Session: written
	// after the gate has answered, and nothing decides on it. RoleIssued is
	// a string for the seal's sake — omitempty does not omit a zero
	// time.Time, and a field that appeared on every old row re-marshalled
	// would read as every old row edited.
	Role       string        `json:"role,omitempty"`
	RoleIssued string        `json:"roleIssued,omitempty"`
	Profile    string        `json:"profile,omitempty"`
	Outcome    Outcome       `json:"outcome"`
	Auth       Authorization `json:"auth"`
	// Code is the machine's half of what went wrong: the dotted, stable
	// code of the refusal or the error ("core.grant.required"), alone. It
	// used to ride Reason as a "code: message" prefix, which made every jq
	// and SIEM rule a piece of string surgery over a sentence rta is free
	// to reword — the code is the contract, the wording never was. Absent
	// on rows where nothing went wrong, and on rows written before the
	// split, whose Reason still carries the glued form.
	Code string `json:"code,omitempty"`
	// Reason is the person's half: the message, without the code.
	Reason string `json:"reason,omitempty"`
	// Millis is how long the handler took, absent for calls that never ran.
	Millis int64 `json:"ms,omitempty"`
	// Missed counts the calls immediately before this one that rta could not
	// write down. It is inside the seal like everything else, so a record
	// admitting a gap is a record that admits it verifiably.
	Missed int64 `json:"missed,omitempty"`
	// MarkLost says the mark recording where the record ends was missing or
	// did not verify when this entry was written. The next entry heals the
	// mark — a record must go on being written — and this is the sealed
	// admission that it did, permanent in the chain, so a truncation that
	// took the mark with it is not laundered by the next call. Verify
	// reports it as information: anything removed from the end before
	// this entry cannot be detected, everything after it can.
	MarkLost bool `json:"markLost,omitempty"`
	// Prev is the previous entry's MAC, and Seal this entry's. Together they
	// are the chain — across files as well as within one.
	//
	// Seal is last in this struct and omitempty, and both matter: the seal
	// covers the line's own bytes up to it, so it has to be the field the
	// line ends with and has to vanish from the bytes being sealed. See
	// sealedLine.
	Prev string `json:"prev"`
	Seal string `json:"seal,omitempty"`
}

// Path is where the record being written lives. The full record is this
// file plus whatever Segments returns.
func Path() string { return filepath.Join(paths.Data(), file) }

// retiredPath is the sealed list of segments rta dropped.
func retiredPath() string { return filepath.Join(paths.Data(), retiredFile) }

func segmentPath(n int) string {
	return filepath.Join(paths.Data(), fmt.Sprintf("agent-log.%05d.jsonl", n))
}

// segmentNumber reads the index out of a rolled file's name, and refuses
// everything else in the data directory — including `agent-log-retired.jsonl`,
// which is why that one is spelled with a hyphen.
func segmentNumber(name string) (int, bool) {
	if !strings.HasPrefix(name, "agent-log.") || !strings.HasSuffix(name, ".jsonl") {
		return 0, false
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(name, "agent-log."), ".jsonl")
	if mid == "" {
		return 0, false
	}
	for _, r := range mid {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(mid)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// rolled sorts the numbered files in the data directory into the ones that
// are part of this record and the ones that are not, and reads each own
// segment's last entry on the way — which is the same read that decides.
//
// # Why this is not simply "what is in the directory"
//
// It was, and that made the retention policy something anybody who could
// create a file could drive. The threat model this package states is a
// writer that cannot read, and creating a file is the cheapest write there
// is: the naming scheme is in this source, so `agent-log.90000.jsonl` is a
// name anybody can produce, and eight of them made rta count far too many
// segments at the next rotation and retire the oldest — which are the real
// ones. It anchored each on the way out, so afterwards the record verified
// clean and reported the loss as rta's own retention. The same write aimed
// low instead of high stopped the record being written at all.
//
// # Why the answer is the file's own tail
//
// A segment rta wrote ends in an entry rta sealed, and sealing needs the key
// that the writer in the threat model cannot read. So the file answers the
// question itself: verify its last line and the file has said whether it is
// part of this record.
//
// This replaced a sealed high-water mark on segment numbers — one more
// number in the head file, a settle() upgrade path for records written
// before it, and an invariant that the mark had to be raised *before* the
// rename that used it, on pain of rta disowning its own newest segment
// after a crash in between. All of that existed to answer a question the
// bytes on disk could answer. It also closes what the mark could not: a
// planted file numbered *below* the mark was "rta's own", so a parseable
// one under a retired number made rta report its own record as broken at
// entry 1.
func rolled(key []byte) (own []int, ends map[int]Entry, foreign []int, err error) {
	entries, err := os.ReadDir(paths.Data())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	ends = map[int]Entry{}
	for _, e := range entries {
		n, ok := segmentNumber(e.Name())
		if !ok {
			continue
		}
		last, verified := tailOf(key, segmentPath(n))
		if !verified {
			foreign = append(foreign, n)
			continue
		}
		own = append(own, n)
		ends[n] = last
	}
	sort.Ints(own)
	sort.Ints(foreign)
	return own, ends, foreign, nil
}

// tailOf reads a file's last entry and says whether it verifies — the one
// question that decides whether the file is part of this record.
//
// An unreadable file is not ours, and that is the safe direction on both
// counts: it is not counted toward retention, so a file rta cannot read
// never costs a real segment of history, and it is not walked as part of the
// chain, so it cannot make rta accuse itself.
func tailOf(key []byte, path string) (Entry, bool) {
	line, e, err := lastLineIn(path)
	if err != nil || e.Seq <= 0 || len(key) == 0 {
		return Entry{}, false
	}
	return e, verifies(key, line, e)
}

// segments is every file the record is spread over, oldest first, with the
// one being written last — and the numbered files that are not part of it.
func segments(key []byte) (files []string, ends map[int]Entry, foreign []int, err error) {
	nums, ends, foreign, err := rolled(key)
	if err != nil {
		return nil, nil, nil, err
	}
	files = make([]string, 0, len(nums)+1)
	for _, n := range nums {
		files = append(files, segmentPath(n))
	}
	if _, err := os.Stat(Path()); err == nil {
		files = append(files, Path())
	}
	return files, ends, foreign, nil
}

// Segments returns every file the record is spread over, oldest first, with
// the one being written last. Only files that exist are named.
func Segments() ([]string, error) {
	key, err := seal.Key(keyFile, false)
	if errors.Is(err, seal.ErrMissing) {
		// No key means no record rta wrote, so no numbered file is one of
		// its segments. The active file is still named if it is there, so a
		// caller sees exactly what it can honestly be shown.
		if _, statErr := os.Stat(Path()); statErr == nil {
			return []string{Path()}, nil
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files, _, _, err := segments(key)
	return files, err
}

// anchor records the end of a segment rta retired: what the surviving chain
// links back to, and the only thing standing between "rta dropped this on
// purpose" and "somebody deleted it".
//
// Sealed under the ledger's own key, so forging one needs the key and
// reading the key needs the read a confined plugin is denied — the same
// bound the entries themselves have, which is the point: an anchor that was
// easier to forge than an entry would be the way to launder a deletion.
//
// Not chained to each other, deliberately. An anchor only ever matters
// relative to the entries that remain: dropping a middle segment leaves a
// sequence gap with no anchor for it, and dropping the oldest leaves the
// new earliest entry with no anchor to link to. Both are already visible,
// so a chain here would guard nothing and would need its own repair story.
type anchor struct {
	Seq  int64     `json:"seq"`
	Seal string    `json:"seal"`
	At   time.Time `json:"at"`
	File string    `json:"file"`
	MAC  string    `json:"mac"`
}

func anchorMAC(key []byte, a anchor) (string, error) {
	a.MAC = ""
	body, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	return seal.MAC(key, body), nil
}

// readAnchors returns the anchors that verify, and how many did not.
//
// An anchor that does not verify is dropped rather than trusted, which
// leaves the chain it would have linked reported as broken. That is the
// fail-closed direction: a forged anchor's whole purpose is to make a
// deletion look like retention.
// maxRetired and maxHead bound the two fixed-name files this package reads
// whole, for the reason internal/atomicfile.ReadCapped states.
//
// The head is one record: 4 KiB is orders past it. The retired list is the
// one file here that legitimately grows — one anchor per rotation, forever,
// which is the point of it — so its cap is sized as a lifetime rather than a
// working set: at a few hundred bytes per anchor, 8 MiB is tens of thousands
// of rotations, far beyond any real install, while still refusing the single
// enormous write this exists to stop. A cap that only has to be *reachable in
// principle* is still a cap.
const (
	maxRetired = 8 << 20
	maxHead    = 4 << 10
)

func readAnchors(key []byte) (map[int64]anchor, int, error) {
	raw, err := atomicfile.ReadCapped(retiredPath(), maxRetired)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	out := map[int64]anchor{}
	bad := 0
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var a anchor
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			bad++
			continue
		}
		want, err := anchorMAC(key, a)
		if err != nil || !seal.Equal(a.MAC, want) {
			bad++
			continue
		}
		out[a.Seq] = a
	}
	return out, bad, nil
}

func writeAnchor(key []byte, a anchor) error {
	mac, err := anchorMAC(key, a)
	if err != nil {
		return err
	}
	a.MAC = mac
	line, err := json.Marshal(a)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(retiredPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// head marks where the record is supposed to end.
//
// The chain alone cannot say. Editing a line breaks the seal and removing
// one from the middle breaks the sequence, but truncating the file leaves a
// shorter record whose every remaining line still verifies — which is the
// cheapest possible way for a confined plugin to erase its own trail, and
// it needs no read at all. This is the high-water mark that makes the
// deletion visible, sealed with the same key for the same reason the
// entries are.
type head struct {
	Seq  int64     `json:"seq"`
	Seal string    `json:"seal"`
	At   time.Time `json:"at"`
	MAC  string    `json:"mac"`
}

func headPath() string { return filepath.Join(paths.Data(), headFile) }

func headMAC(key []byte, h head) (string, error) {
	h.MAC = ""
	body, err := json.Marshal(h)
	if err != nil {
		return "", err
	}
	return seal.MAC(key, body), nil
}

// writeHead records the new end of the record, atomically so that a crash
// leaves the old mark rather than half of a new one.
func writeHead(key []byte, e Entry) error {
	h := head{Seq: e.Seq, Seal: e.Seal, At: time.Now().UTC().Truncate(time.Second)}
	mac, err := headMAC(key, h)
	if err != nil {
		return err
	}
	h.MAC = mac
	body, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return atomicfile.Write(headPath(), body, 0o600)
}

// readHead returns the high-water mark, or ok=false when there is none or
// it does not verify. An unverifiable mark is treated as absent, and absent
// is itself reported: both mean nothing trustworthy says where the record
// ends.
func readHead(key []byte) (head, bool) {
	raw, err := atomicfile.ReadCapped(headPath(), maxHead)
	if err != nil {
		return head{}, false
	}
	var h head
	if err := json.Unmarshal(raw, &h); err != nil {
		return head{}, false
	}
	want, err := headMAC(key, h)
	if err != nil || !seal.Equal(h.MAC, want) {
		return head{}, false
	}
	return h, true
}

// Append records one call.
//
// Errors are returned rather than raised, and every caller drops them onto
// stderr rather than failing the call: a call that succeeded and was not
// recorded is a gap in the log, while a call refused because the log could
// not be written is rta breaking the operator's tooling to protect its own
// bookkeeping.
//
// What it must not be is silent. Sequence numbers come from the last entry
// actually written, so a dropped record leaves the chain closed over the
// absence with nothing to find — the count therefore rides on the next
// entry that does get written (Entry.Missed), inside its seal.
func Append(e Entry) (err error) {
	// Counted here so that every path out of this function is covered,
	// including the ones that fail before the lock is even taken.
	defer func() {
		if err != nil {
			missed.Add(1)
		}
	}()
	appendMu.Lock()
	defer appendMu.Unlock()

	// The key is minted for a record that has none, never over one that
	// exists: a fresh key beside six hundred entries made every one of
	// them read as edited, and named entry 1 as the culprit. And a key
	// that is present but too short is named by its path with the next
	// step, because the server's stderr is the only place this is seen.
	keyPath := seal.Path(keyFile)
	if _, statErr := os.Stat(keyPath); os.IsNotExist(statErr) {
		if info, err := os.Stat(Path()); err == nil && info.Size() > 0 {
			return fmt.Errorf("%s is missing beside a record that is not empty; rta will not mint a new key over an existing record — restore it, or move agent-log*.jsonl aside to start a new one", keyPath)
		}
	}
	key, err := seal.Key(keyFile, true)
	if err != nil {
		return fmt.Errorf("agent log key %s: %w — restore it, or move the record aside to start a new one", keyPath, err)
	}
	if err := os.MkdirAll(paths.Data(), 0o700); err != nil {
		return err
	}
	// One writer at a time: the chain is read-then-append, and two servers
	// interleaving would produce two entries claiming the same predecessor.
	// The same lock covers rolling and retiring, so a second server cannot
	// append to a file the first one is in the middle of renaming.
	release, err := filelock.Acquire(Path()+".lock", lockStale, lockRetry, lockTimeout)
	if err != nil {
		return fmt.Errorf("agent log is busy: %w", err)
	}
	defer release()

	// The sealed bound on which files are the record, read once and carried
	// through: rotate may raise it, and the mark written at the end has to
	// record whatever it ends up being.
	//
	// Rotation first, then the read: a roll renames the active file to a
	// numbered one, and the entry this call is about to write has to chain
	// to whatever ended up last — which after a roll is the tail of the file
	// that was just renamed.
	if err := rotate(key); err != nil {
		return err
	}
	last, err := lastEntry(key)
	if err != nil {
		return err
	}
	e.Seq = last.Seq + 1
	e.Prev = last.Seal
	if _, ok := readHead(key); !ok && last.Seq > 0 {
		e.MarkLost = true
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	e.At = e.At.UTC().Truncate(time.Second)
	// Taken, not read: whatever was lost is now this entry's to report, and
	// leaving it in place would repeat the claim on every entry after.
	e.Missed = missed.Swap(0)
	e.Seal = ""
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(body) > maxLine {
		// Rather than drop the entry: the call happened, and a truncated
		// record of it beats none. Args are the unbounded part that is
		// expected; every string field is bounded after them, because one
		// was not, once. A refusal that echoed a 3 MB argument in its
		// message became a 3 MB row, the tail reader could not find a line
		// end inside its window, and every append on the machine failed
		// from then on — silently, to a stderr the client swallows. The
		// row is now bounded whatever a handler puts in it, and lastEntryIn
		// widens its window besides, so an old oversized row is read past
		// rather than fatal.
		e.Args = map[string]any{"…": fmt.Sprintf("%d bytes of arguments, omitted", len(body))}
		e.Reason = clip(e.Reason, maxField)
		e.Code = clip(e.Code, maxField)
		e.Client = clip(e.Client, maxField)
		e.Credential = clip(e.Credential, maxField)
		e.Agent = clip(e.Agent, maxField)
		e.Profile = clip(e.Profile, maxField)
		e.Tool = clip(e.Tool, maxField)
		e.Cap = clip(e.Cap, maxField)
		if body, err = json.Marshal(e); err != nil {
			return err
		}
	}
	line, mac := sealedLine(key, e, body)
	if line == nil {
		return fmt.Errorf("sealing the entry")
	}
	e.Seal = mac
	f, err := os.OpenFile(Path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// After the entry, not before. A mark written first and never followed
	// by its entry would report a record that had been rolled back, which is
	// the alarm this exists to raise — so the crash window is one entry of
	// *under*-reporting rather than a false accusation.
	return writeHead(key, e)
}

// rotate rolls the active file aside once it is full and retires the oldest
// segments past the retention count. Called with the append lock held.
//
// limit is the sealed segment high-water on the way in; the returned value is
// it on the way out, raised by one if this call rolled. Threaded through
// rather than re-read because the mark is written at the end of Append, and a
// roll whose number never reaches the mark is a segment rta would refuse to
// recognise on the next call — the record would appear to lose its most
// recent file.
func rotate(key []byte) error {
	info, err := os.Stat(Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() < maxSegment {
		return nil
	}
	// One past the highest number on disk, ours or not. Skipping over a file
	// rta does not recognise costs nothing — the record has never needed its
	// segment numbers to be contiguous — and reusing one would overwrite
	// whatever is there, which for a segment that fails to verify is the
	// evidence that it does.
	next := 1
	entries, err := os.ReadDir(paths.Data())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if n, ok := segmentNumber(e.Name()); ok && n >= next {
			next = n + 1
		}
	}
	if err := os.Rename(Path(), segmentPath(next)); err != nil {
		return err
	}
	return retire(key)
}

// retire drops the oldest segments past keepSegments, anchoring each before
// it goes.
//
// The anchor is written *before* the file is removed, and the order is the
// safe one. A crash in between leaves an anchor for a segment that is still
// there, which costs nothing — verification starts from the earliest entry
// it can actually see and never consults an anchor it does not need. The
// other order would leave a gap with nothing recording it, which is exactly
// what this mechanism exists to distinguish from tampering.
func retire(key []byte) error {
	nums, ends, _, err := rolled(key)
	if err != nil {
		return err
	}
	for len(nums) > keepSegments {
		p, last := segmentPath(nums[0]), ends[nums[0]]
		nums = nums[1:]
		if last.Seq > 0 {
			if err := writeAnchor(key, anchor{
				Seq:  last.Seq,
				Seal: last.Seal,
				At:   time.Now().UTC().Truncate(time.Second),
				File: filepath.Base(p),
			}); err != nil {
				return err
			}
		}
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	return nil
}

// sealOf recomputes an entry's MAC: the entry as it was marshalled with an
// empty Seal, prefixed by its predecessor's.
//
// The MAC covers the entry without its own seal field, because a document
// containing its own MAC needs a rule for what that field held while the
// MAC was computed, and every such rule is a place for the writer and the
// reader to disagree. It covers Prev too, which is what makes the file a
// chain rather than a pile of individually valid lines: without it, entries
// could be reordered or deleted wholesale and every remaining line would
// still verify.
func sealOf(key []byte, e Entry) (string, error) {
	prev := e.Prev
	e.Seal = ""
	body, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	// The writer this reproduces marshalled Seal unconditionally, so the
	// bytes it sealed ended in an empty seal field. Rebuilt here rather than
	// held in place by a struct tag, so the shape rta writes today is free
	// to change without dragging the old one along with it.
	legacy := make([]byte, 0, len(body)+11)
	legacy = append(legacy, body[:len(body)-1]...)
	legacy = append(legacy, `,"seal":""}`...)
	return seal.MAC(key, append([]byte(prev), legacy...)), nil
}

// sealedLine renders one entry as the bytes that go on disk: its own JSON,
// with the seal spliced in as the last field, computed over everything
// before it.
//
// **The seal covers the line, not the struct it was parsed from**, and that
// is the difference between a record that survives this program changing and
// one that does not. sealOf re-marshals a *parsed* Entry, so verification
// only agrees with the writer while marshal(parse(line)) reproduces the
// original bytes — which makes every field's json tag, omitempty and
// position load-bearing forever, and any change to Entry read as every
// existing record having been tampered with. One such change already needed
// a dedicated test to prove it had not broken anything (legacy_test.go), and
// that test is a warning rather than a solution: the next change would need
// another, and the one after that would have to be refused.
//
// Sealing the bytes also covers fields this binary does not know about. A
// record written by a newer rta keeps verifying here rather than silently
// losing whatever the parse dropped.
func sealedLine(key []byte, e Entry, body []byte) ([]byte, string) {
	mac := seal.MAC(key, append([]byte(e.Prev), body...))
	quoted, err := json.Marshal(mac)
	if err != nil {
		return nil, ""
	}
	line := make([]byte, 0, len(body)+len(quoted)+9)
	line = append(line, body[:len(body)-1]...)
	line = append(line, `,"seal":`...)
	line = append(line, quoted...)
	return append(line, '}'), mac
}

// unsealed splits a stored line back into the bytes its seal covers and the
// seal itself. ok is false for a line that does not end in a seal field,
// which is every line rta did not write.
func unsealed(line []byte, sealed string) ([]byte, bool) {
	quoted, err := json.Marshal(sealed)
	if err != nil {
		return nil, false
	}
	suffix := append([]byte(`,"seal":`), quoted...)
	suffix = append(suffix, '}')
	if !bytes.HasSuffix(line, suffix) {
		return nil, false
	}
	body := make([]byte, 0, len(line)-len(suffix)+1)
	body = append(body, line[:len(line)-len(suffix)]...)
	return append(body, '}'), true
}

// verifies reports whether one stored line carries the seal rta would have
// written for it.
//
// Two forms are accepted, and the second is a bridge rather than a policy. A
// line rta writes today is sealed over its own bytes (sealedLine). A line
// written before that was sealed over the re-marshalled struct, and the only
// alternative to still accepting those is telling every operator who
// upgrades that their audit record has been tampered with — which is a false
// alarm, and a false alarm on this particular screen is worse than the
// duplication here. Both forms need the key, so neither weakens anything.
func verifies(key []byte, line []byte, e Entry) bool {
	if body, ok := unsealed(line, e.Seal); ok {
		if seal.Equal(e.Seal, seal.MAC(key, append([]byte(e.Prev), body...))) {
			return true
		}
	}
	want, err := sealOf(key, e)
	return err == nil && seal.Equal(e.Seal, want)
}

// lastEntry reads the final entry of the record: the active file's last
// line, or — in the moment after a roll, when the active file does not yet
// exist — the last line of the newest segment. Getting that fallback wrong
// would start a second chain at every rotation.
func lastEntry(key []byte) (Entry, error) {
	e, err := lastEntryIn(Path())
	if err != nil {
		return Entry{}, err
	}
	if e.Seq > 0 {
		return e, nil
	}
	nums, ends, _, err := rolled(key)
	if err != nil || len(nums) == 0 {
		return Entry{}, err
	}
	return ends[nums[len(nums)-1]], nil
}

// lastEntryIn reads one file's final entry without reading the whole file.
func lastEntryIn(path string) (Entry, error) {
	_, e, err := lastLineIn(path)
	return e, err
}

// lastLineIn reads one file's final entry and the bytes it was stored as,
// which is what verifying its seal needs.
func lastLineIn(path string) ([]byte, Entry, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, Entry{}, nil
	}
	if err != nil {
		return nil, Entry{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, Entry{}, err
	}
	size := info.Size()
	if size == 0 {
		return nil, Entry{}, nil
	}
	// The window grows until a line parses or the whole file has been
	// read: a row larger than the window — written before rows were
	// bounded — must be read past, not reported as the end of the record.
	for chunk := int64(tailWindow); ; chunk *= 4 {
		start := size - chunk
		if start < 0 {
			start = 0
		}
		buf := make([]byte, size-start)
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return nil, Entry{}, err
		}
		lines := bytes.Split(bytes.TrimRight(buf, "\n"), []byte("\n"))
		for i := len(lines) - 1; i >= 0; i-- {
			var e Entry
			if json.Unmarshal(lines[i], &e) == nil && e.Seq > 0 {
				return bytes.TrimSpace(lines[i]), e, nil
			}
		}
		if start == 0 {
			break
		}
	}
	// A file that holds no parseable entry at all: appending with a zero
	// predecessor would silently start a second chain, so say so instead.
	return nil, Entry{}, fmt.Errorf("%s holds no readable entry", path)
}

// Read returns the most recent entries, newest last, at most limit of them.
//
// It reads backwards from the newest segment and stops as soon as it has
// enough, so showing thirty rows costs thirty rows and not the whole
// record. limit <= 0 means everything there is, which is what a
// verification-adjacent caller wants and what nothing on a display path
// should ask for.
func Read(limit int) ([]Entry, error) {
	files, err := Segments()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		var out []Entry
		for _, p := range files {
			es, err := entriesIn(p)
			if err != nil {
				return nil, err
			}
			out = append(out, es...)
		}
		return out, nil
	}
	var out []Entry
	for i := len(files) - 1; i >= 0 && len(out) < limit; i-- {
		lines, err := tailLines(files[i], limit-len(out))
		if err != nil {
			return nil, err
		}
		seg := make([]Entry, 0, len(lines))
		for _, l := range lines {
			var e Entry
			if json.Unmarshal([]byte(l), &e) != nil {
				continue
			}
			seg = append(seg, e)
		}
		out = append(seg, out...)
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// tailLines returns at most want of a file's final lines, in order, reading
// from the end and widening until it has them.
func tailLines(path string, want int) ([]string, error) {
	if want <= 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size == 0 {
		return nil, nil
	}
	for chunk := int64(tailWindow); ; chunk *= 4 {
		start := size - chunk
		if start < 0 {
			start = 0
		}
		buf := make([]byte, size-start)
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
		if start > 0 {
			// The first line of a window that does not begin at the file's
			// start is half a line.
			lines = lines[1:]
		}
		if len(lines) >= want || start == 0 {
			if len(lines) > want {
				lines = lines[len(lines)-want:]
			}
			return lines, nil
		}
	}
}

// entriesIn parses one whole file.
func entriesIn(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := newLineReader(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			// A corrupt line is not a reason to lose the rest of the file;
			// Verify is what reports it.
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Report is what Verify found.
type Report struct {
	Entries int
	// Broken is the sequence number of the first entry whose seal or
	// predecessor does not check out, 0 when the chain is whole.
	Broken int64
	// Why names what was wrong at that entry, for the operator to read.
	Why string
	// Size is the record's total size in bytes, across every segment.
	Size int64
	// Files is how many segments the record is spread over.
	Files int
	// Retired is how many entries rta dropped by its own retention, and
	// RetiredAt when the most recent of those went. Zero when the record
	// still holds everything it ever recorded.
	Retired   int64
	RetiredAt time.Time
	// Last is the highest sequence number still on disk.
	Last int64
	// Missed is how many calls the entries themselves admit could not be
	// written down. Counted from the record rather than from memory, so it
	// survives the process that lost them.
	Missed int64
	// Foreign names files in the data directory that carry a segment's name
	// and a number rta has never rolled. They are excluded from the record —
	// see rolled() — and reported rather than quietly skipped: something
	// wrote a file pretending to be part of the ledger, and whatever can do
	// that is the thing to go and look at.
	Foreign []string
	// Unanchored marks the one fault that is repairable, and the distinction
	// it draws is the whole reason Reanchor can exist safely.
	//
	// Broken is set for three different situations and an operator reading it
	// cannot act on any of them differently: an entry that fails its own
	// seal, a record shorter than the mark says it should be, and a mark that
	// is simply not there. Only the last one is a lost *marker* rather than
	// lost *evidence* — every entry on disk verified, and what is missing is
	// the note saying where the record was supposed to stop. Re-anchoring
	// that is bookkeeping.
	//
	// Re-anchoring either of the others would be destroying the finding. A
	// failed seal means a line was edited; a record short of its mark means
	// entries were removed from the end. In both cases the chain is doing its
	// job, and a repair command that could not tell them apart would be a
	// tool for erasing the thing the record exists to show.
	Unanchored bool
	// MarkLost lists the entries written while the end mark was missing
	// or unverifiable (Entry.MarkLost). The mark was healed by each of
	// them; what is permanent is the admission that, before each, the end
	// of the record was not vouched for.
	MarkLost []int64
}

// Verify walks the chain from the beginning of what is still on disk.
//
// It reports the first break rather than every one, because the first is
// the only one that means anything: after a line has been changed, every
// entry that followed it has the wrong predecessor and would be reported
// too, which turns one edit into a screen of noise.
//
// Streaming, and that is not an optimisation. A record at its retention
// ceiling is tens of megabytes, and a verification that had to hold all of
// it in memory would be a verification an operator learns not to run.
func Verify() (Report, error) {
	rep := Report{}
	// The same lock Append holds for its own read-then-write, and for the
	// same reason: Append can rotate mid-call, renaming the active file to
	// a numbered one and recreating it empty, and Verify has to see one
	// state or the other — never a file list snapshotted before a rotation
	// read back after it, which is a rename this function was never told
	// about happening underneath a file name it already committed to. Held
	// for the whole walk, not just the snapshot: releasing it the moment
	// the file list is in hand would still leave every read after that
	// point racing the next rotation.
	appendMu.Lock()
	defer appendMu.Unlock()
	release, err := filelock.Acquire(Path()+".lock", lockStale, lockRetry, lockTimeout)
	if err != nil {
		return rep, fmt.Errorf("agent log is busy: %w", err)
	}
	defer release()

	// Read before the file list, because the file list is decided by it: a
	// numbered file is part of this record when its last entry verifies
	// under this key. Missing is handled below, where "there is no key" and
	// "there is no record" are told apart.
	key, keyErr := seal.Key(keyFile, false)
	if keyErr != nil && !errors.Is(keyErr, seal.ErrMissing) {
		return rep, keyErr
	}
	files, _, foreign, err := segments(key)
	if err != nil {
		return rep, err
	}
	for _, n := range foreign {
		rep.Foreign = append(rep.Foreign, filepath.Base(segmentPath(n)))
	}
	if len(files) == 0 {
		// Nothing on disk at all. That is either a machine no agent has ever
		// called, or one whose record was removed wholesale — and with no
		// chain left to break, the high-water mark is the only thing that can
		// tell them apart.
		if keyErr != nil {
			return rep, nil
		}
		if h, ok := readHead(key); ok && h.Seq > 0 {
			rep.Broken = 1
			rep.Why = fmt.Sprintf(
				"nothing is on disk and rta last wrote entry %d, so the whole record has been removed", h.Seq)
		}
		return rep, nil
	}
	rep.Files = len(files)
	for _, p := range files {
		if info, err := os.Stat(p); err == nil {
			rep.Size += info.Size()
		}
	}
	if keyErr != nil {
		return rep, fmt.Errorf("%s exists with no seal key beside it, so it was not written by rta", Path())
	}
	anchors, badAnchors, err := readAnchors(key)
	if err != nil {
		return rep, err
	}

	prev := ""
	var seq int64
	started := false
	for _, p := range files {
		f, err := os.Open(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return rep, err
		}
		sc := newLineReader(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var e Entry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				continue
			}
			rep.Entries++
			rep.Missed += e.Missed
			if e.MarkLost {
				rep.MarkLost = append(rep.MarkLost, e.Seq)
			}
			if !started {
				started = true
				// Where the surviving record begins. Entry 1 needs no
				// explanation; anything else needs an anchor saying rta
				// retired what came before, or it is a deletion.
				if e.Seq != 1 {
					a, ok := anchors[e.Seq-1]
					if !ok {
						f.Close()
						rep.Broken = e.Seq
						rep.Why = fmt.Sprintf(
							"the record starts at entry %d and nothing records entries 1-%d as retired",
							e.Seq, e.Seq-1)
						if badAnchors > 0 {
							rep.Why += fmt.Sprintf(" (%d retirement records do not verify)", badAnchors)
						}
						return rep, nil
					}
					prev, seq = a.Seal, a.Seq
					rep.Retired, rep.RetiredAt = a.Seq, a.At
				}
			}
			seq++
			switch {
			case e.Seq != seq:
				rep.Broken, rep.Why = e.Seq, fmt.Sprintf("expected entry %d and found %d, so entries are missing or reordered", seq, e.Seq)
			case e.Prev != prev:
				rep.Broken, rep.Why = e.Seq, "it does not follow the entry before it"
			default:
				if !verifies(key, []byte(line), e) {
					rep.Broken, rep.Why = e.Seq, "its contents do not match its seal"
				}
			}
			if rep.Broken != 0 {
				f.Close()
				return rep, nil
			}
			prev = e.Seal
			seq = e.Seq
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return rep, err
		}
	}
	rep.Last = seq

	// And finally: does the record end where rta last said it did? Every
	// check above walks forward from the beginning and none of them can see
	// entries that are no longer there, so without this a truncated file is
	// simply a shorter one that verifies.
	h, ok := readHead(key)
	switch {
	case !ok && rep.Entries > 0:
		rep.Broken = seq
		rep.Unanchored = true
		rep.Why = "nothing records where this record is supposed to end, so entries could have been removed from it without trace"
	case ok && h.Seq > seq:
		rep.Broken = seq + 1
		rep.Why = fmt.Sprintf("the record stops at entry %d and rta last wrote entry %d, so %s been removed from the end",
			seq, h.Seq, plural(h.Seq-seq, "entry has", "entries have"))
	}
	// Deliberately not also comparing the mark's seal against the last
	// entry's. A probe removed that comparison and broke no test, which was
	// the right answer rather than a missing one: an attacker without the
	// key cannot produce an entry at sequence N that verifies and differs,
	// so every case the comparison could catch is already caught by the
	// entry's own seal — and one with the key can rewrite the mark too. A
	// check that guards nothing is a check somebody eventually trusts.
	return rep, nil
}

// plural keeps the two truncation messages readable without pulling a
// dependency in for one sentence.
func plural(n int64, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// lineReader walks a file line by line with no ceiling on a line's length.
// bufio.Scanner stops at its buffer with "token too long", and a record
// holding one oversized row — written before rows were bounded — would
// then be unreadable from that row on, in every command that reads it.
type lineReader struct {
	r    *bufio.Reader
	line string
	err  error
}

func newLineReader(f *os.File) *lineReader { return &lineReader{r: bufio.NewReaderSize(f, 64<<10)} }

func (l *lineReader) Scan() bool {
	if l.err != nil {
		return false
	}
	line, err := l.r.ReadString('\n')
	if err != nil {
		if !errors.Is(err, io.EOF) {
			l.err = err
		}
		if line == "" {
			return false
		}
	}
	l.line = line
	return true
}

func (l *lineReader) Text() string { return l.line }
func (l *lineReader) Err() error   { return l.err }

// clip cuts s to at most n bytes on a rune boundary, marking the cut: a
// byte-slice through a multi-byte character would put invalid UTF-8 into a
// sealed file, where it stays forever.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for len(s) > n {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s + "…"
}

// ReadAfter is the record from just past seq onwards, oldest first, at most
// limit entries. It walks the segments forward and skips whole files whose
// last entry is at or before the cursor, so shipping from a cursor costs
// what is new and not the whole record.
//
// This is the read the shipping recipe needs and Read is not: Read keeps
// the newest entries, so `--after 0 --limit 500` on a 600-entry record
// answered 101–600 and the archive never saw the first hundred.
func ReadAfter(seq int64, limit int) ([]Entry, error) {
	files, err := Segments()
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, p := range files {
		last, err := lastEntryIn(p)
		if err != nil || last.Seq <= seq {
			continue
		}
		es, err := entriesIn(p)
		if err != nil {
			return nil, err
		}
		for _, e := range es {
			if e.Seq <= seq {
				continue
			}
			out = append(out, e)
			if limit > 0 && len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

// Recent is every entry written at or after since, oldest first, bounded by
// time rather than by count: a tile that says "calls in the last hour" must
// not stop counting at the last five hundred rows.
func Recent(since time.Time) ([]Entry, error) {
	files, err := Segments()
	if err != nil {
		return nil, err
	}
	var out []Entry
	for i := len(files) - 1; i >= 0; i-- {
		es, err := entriesIn(files[i])
		if err != nil {
			return nil, err
		}
		var keep []Entry
		for _, e := range es {
			if !e.At.Before(since) {
				keep = append(keep, e)
			}
		}
		out = append(keep, out...)
		if len(es) > 0 && es[0].At.Before(since) {
			break
		}
	}
	return out, nil
}
