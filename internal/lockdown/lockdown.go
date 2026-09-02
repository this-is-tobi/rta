// Package lockdown is the instant path revocation needs when expiry is too
// slow: freeze one principal now, across every network surface, without
// restarting anything.
//
// The gap it closes is real on both surfaces. Revoking grants takes
// standing authority back, but a misbehaving agent's bearer token still
// opens every ungated read tool; and a compromised operator key stays
// enrolled until someone edits the roster and restarts the server, because
// the roster is deliberately read once. A lock is checked per call, so it
// takes effect for running servers on their next request — and it only
// ever subtracts, which is what lets it inherit two standing doctrines for
// free: it needs no passphrase (revoking never asks — an incident is the
// wrong moment to demand a secret), and a forged lock row costs an
// attacker nothing but refusals.
//
// Three kinds of principal, matching the three identities the network
// surfaces actually verify: the agent name a server runs --as, the
// credential label the bearer wall or OIDC verifier proved, and the
// operator label a roster signature proved. The local CLI and TUI are
// never gated — the person at the terminal is the authority locks answer
// to, not a party they restrain.
//
// The file is sealed like the grant file, but its failure direction is the
// guard's, not the grant store's: deleting grants.json removes authority,
// while deleting lockdown.json would restore it. So serving processes read
// through a Pin that remembers the last state it verified — a legitimate
// unlock rewrites the sealed file without the row and propagates on the
// next call, while a file that vanishes or stops verifying after locks
// were seen changes nothing for the process that remembers, and is
// reported. Across restarts, on-disk deletion wins; that is the same
// documented detection regime as every other same-uid rollback, and the
// boundary chapter owns what remains.
package lockdown

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/internal/filelock"
	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/seal"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Kind names which verified identity a lock freezes.
type Kind string

const (
	// KindAgent matches the name a server runs --as: the emergency brake
	// for one agent's whole MCP surface, ungated reads included.
	KindAgent Kind = "agent"
	// KindCredential matches the identity the bearer wall or OIDC verifier
	// proved — one token among several on a shared HTTP server.
	KindCredential Kind = "credential"
	// KindOperator matches a roster label on the operator channel: the
	// answer to a compromised operator key that beats editing the roster
	// and restarting, because it lands on the next call.
	KindOperator Kind = "operator"
)

// Kinds lists the valid kinds, for input validation and help text.
func Kinds() []Kind { return []Kind{KindAgent, KindCredential, KindOperator} }

// CheckKind refuses anything but the three declared kinds, fail closed —
// a typo'd kind must not become a lock that matches nothing while reading
// as protection.
func CheckKind(k string) (Kind, *view.Error) {
	switch Kind(k) {
	case KindAgent, KindCredential, KindOperator:
		return Kind(k), nil
	}
	return "", view.Errorf("core.lock.kind",
		"%q is not something a lock can freeze — agent, credential and operator are", k)
}

// Lock is one frozen principal.
type Lock struct {
	Kind Kind   `json:"kind"`
	Name string `json:"name"`
	// Note is shown to the locked party on refusal, Teleport-style: whoever
	// wrote it knew who would read it.
	Note string `json:"note,omitempty"`
	// By records who set it: "terminal", or operator:<label> for a lock
	// placed over the operator channel.
	By string    `json:"by,omitempty"`
	At time.Time `json:"at"`
	// Expires zero means until removed — incident locks usually are.
	Expires time.Time `json:"expires,omitempty"`
}

func (l Lock) expired(now time.Time) bool {
	return !l.Expires.IsZero() && !l.Expires.After(now)
}

const (
	fileName = "lockdown.json"
	keyFile  = "lockdown.key"
	// maxLockFile bounds every read, for grants.go's exact reason: the read
	// happens before the seal check, so a forged file need not be valid to
	// cost something, only large. A lock is a few hundred bytes.
	maxLockFile = 256 << 10
)

// Path is where the locks live.
func Path() string { return seal.Path(fileName) }

type sealed struct {
	Seal  string `json:"seal"`
	Locks []Lock `json:"locks"`
}

func canonical(locks []Lock) ([]byte, error) { return json.Marshal(locks) }

func sealKey(create bool) ([]byte, *view.Error) {
	key, err := seal.Key(keyFile, create)
	switch {
	case err == nil:
		return key, nil
	case errors.Is(err, seal.ErrMissing), errors.Is(err, seal.ErrShort):
		return nil, view.Errorf("core.lock.unsealed",
			"%s exists with no usable seal key beside it, so it was not written by rta", Path()).
			WithHint("`rm " + Path() + "` clears every lock; re-place the ones that were real")
	default:
		return nil, view.Errorf("core.lock.write", "%v", err)
	}
}

// load reads the file raw: the rows, whether a file was present at all,
// and whether what was present verified. Expired rows are dropped on the
// way out so a TTL'd lock lifts itself.
func load() (locks []Lock, present bool, verr *view.Error) {
	data, err := atomicfile.ReadCapped(Path(), maxLockFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, view.Errorf("core.lock.read", "%v", err)
	}
	key, verr := sealKey(false)
	if verr != nil {
		return nil, true, verr
	}
	var doc sealed
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, true, view.Errorf("core.lock.forged",
			"%s does not parse, so it was not written by rta", Path()).
			WithHint("`rm " + Path() + "` clears every lock; re-place the ones that were real")
	}
	raw, err := canonical(doc.Locks)
	if err != nil {
		return nil, true, view.Errorf("core.lock.read", "%v", err)
	}
	if !seal.Equal(doc.Seal, seal.MAC(key, raw)) {
		return nil, true, view.Errorf("core.lock.forged",
			"%s does not carry rta's own seal, so something else wrote it", Path()).
			WithHint("`rm " + Path() + "` clears every lock; re-place the ones that were real")
	}
	now := time.Now()
	live := doc.Locks[:0]
	for _, l := range doc.Locks {
		if !l.expired(now) {
			live = append(live, l)
		}
	}
	return live, true, nil
}

// Load is the human-facing read: the live locks, or the seal alarm.
func Load() ([]Lock, *view.Error) {
	locks, _, verr := load()
	return locks, verr
}

func save(locks []Lock) *view.Error {
	key, verr := sealKey(true)
	if verr != nil {
		return verr
	}
	raw, err := canonical(locks)
	if err != nil {
		return view.Errorf("core.lock.write", "%v", err)
	}
	data, err := json.MarshalIndent(sealed{Seal: seal.MAC(key, raw), Locks: locks}, "", "  ")
	if err != nil {
		return view.Errorf("core.lock.write", "%v", err)
	}
	if err := atomicfile.Write(Path(), data, 0o600); err != nil {
		return view.Errorf("core.lock.write", "%v", err)
	}
	return nil
}

func mutate(f func([]Lock) []Lock) *view.Error {
	release, err := filelock.Acquire(Path()+".lock", 10*time.Second, 25*time.Millisecond, 5*time.Second)
	if err != nil {
		return view.Errorf("core.lock.busy", "another rta is changing the locks: %v", err)
	}
	defer release()
	stored, _, verr := load()
	if verr != nil {
		// A file that does not verify is not honoured and not built upon:
		// writing "the fix" over bytes of unknown authorship would launder
		// them. The operator clears it first, loudly.
		return verr
	}
	return save(f(stored))
}

// Build assembles one lock from the raw strings a surface collected — the
// CLI's flags or a LockSpec off the wire — so both surfaces mint the same
// shape and parse TTL with the same grammar. Empty ttl means until
// removed: incident locks usually are, and a forgotten lock costs
// refusals, never access.
func Build(kind, name, note, ttl, by string) (Lock, *view.Error) {
	k, verr := CheckKind(kind)
	if verr != nil {
		return Lock{}, verr
	}
	l := Lock{Kind: k, Name: name, Note: strings.TrimSpace(note), By: by, At: time.Now()}
	if s := strings.TrimSpace(ttl); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			return Lock{}, view.Errorf("core.lock.ttl",
				"%q is not a lock window — 30m, 2h; leave it off for a lock that stands until removed", ttl)
		}
		l.Expires = l.At.Add(d)
	}
	return l, nil
}

// Add places or refreshes one lock — same kind and name replaces the row,
// so re-locking with a new note or window needs no rm first.
func Add(l Lock) *view.Error {
	if _, verr := CheckKind(string(l.Kind)); verr != nil {
		return verr
	}
	// The name grammar is the agent-name grammar for the reason the roster
	// borrowed it: this string is rendered in listings and refusals, and a
	// homoglyph principal is a lookalike row nobody meant to trust or
	// freeze.
	if verr := grant.CheckAgent(l.Name); verr != nil {
		return verr
	}
	if strings.TrimSpace(l.Name) == "" {
		return view.Errorf("core.lock.name", "a lock needs the principal's name")
	}
	return mutate(func(stored []Lock) []Lock {
		out := stored[:0]
		for _, s := range stored {
			if s.Kind != l.Kind || s.Name != l.Name {
				out = append(out, s)
			}
		}
		return append(out, l)
	})
}

// Remove lifts one lock. The second return says whether it was there —
// "nothing was locked" and "unlocked" are different sentences.
func Remove(kind Kind, name string) (bool, *view.Error) {
	found := false
	verr := mutate(func(stored []Lock) []Lock {
		out := stored[:0]
		for _, s := range stored {
			if s.Kind == kind && s.Name == name {
				found = true
				continue
			}
			out = append(out, s)
		}
		return out
	})
	return found, verr
}

// Pin is a serving process's view of the locks, re-read per check so a
// lock placed from anywhere lands on the next call — with the guard pin's
// memory against the one edit that must not work live: a file that
// vanishes or stops verifying after this process saw locks keeps the last
// verified set in effect, because on-disk absence cannot be told apart
// from a same-uid rm by the attacker the locks are aimed at. A legitimate
// unlock rewrites the sealed file without the row and propagates normally.
type Pin struct {
	mu       sync.Mutex
	lastGood []Lock
	seen     bool
	alarmed  bool
}

// NewPin builds a pin for one serving process.
func NewPin() *Pin { return &Pin{} }

// snapshot re-reads the file and returns the set in effect for this
// process, plus an alarm sentence the first time the file goes missing or
// stops verifying while locks were in effect — for the server's stderr,
// once per incident, not per call.
func (p *Pin) snapshot() ([]Lock, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	locks, present, verr := load()
	switch {
	case verr == nil && present:
		p.lastGood, p.seen, p.alarmed = locks, true, false
	case verr == nil && !present && !p.seen:
		// Never had locks, still none: the clean machine.
	default:
		// Vanished after being seen, or no longer verifying: hold the last
		// verified set and say so once.
		locks = p.lastGood
		if !p.alarmed {
			p.alarmed = true
			return locks, fmt.Sprintf("%s no longer verifies or is gone while locks were in effect — "+
				"holding the last verified set for this process; whatever rewrote it is the thing to look at", Path())
		}
	}
	return locks, ""
}

// Frozen reports the lock covering (kind, name), if any, and snapshot's
// alarm.
func (p *Pin) Frozen(kind Kind, name string) (*Lock, string) {
	locks, alarm := p.snapshot()
	return match(locks, kind, name), alarm
}

// Check is Frozen for the MCP surface's two identities in one read: the
// agent name the server runs --as, and the credential the bearer wall or
// OIDC verifier proved for this caller.
func (p *Pin) Check(agent, credential string) (*Lock, string) {
	locks, alarm := p.snapshot()
	if l := match(locks, KindAgent, agent); l != nil {
		return l, alarm
	}
	return match(locks, KindCredential, credential), alarm
}

func match(locks []Lock, kind Kind, name string) *Lock {
	if name == "" {
		return nil
	}
	now := time.Now()
	for i := range locks {
		if locks[i].Kind == kind && locks[i].Name == name && !locks[i].expired(now) {
			return &locks[i]
		}
	}
	return nil
}

// Refusal is the sentence a frozen principal reads, with the note the
// locker wrote for exactly this moment.
func Refusal(l *Lock) *view.Error {
	msg := fmt.Sprintf("this %s is locked", l.Kind)
	if l.Note != "" {
		msg += ": " + l.Note
	}
	return view.Errorf("core.lock.frozen", "%s", msg).
		WithHint("a person lifts it with `rta lock rm " + string(l.Kind) + " " + l.Name + "`")
}
