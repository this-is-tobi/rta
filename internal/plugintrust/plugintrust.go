// Package plugintrust decides which plugin artifacts rta is allowed to run.
//
// **Being on $PATH is not consent.** Discovery scans $PATH for `rta-plugin-*`
// and rta then *launches* each one to ask what it declares — so, before this,
// dropping a file called `rta-plugin-anything` into any directory on any
// $PATH entry bought code execution on the next `rta` invocation, including
// the `rta __complete` that a tab press runs. Nobody typed a command naming
// it, nobody approved anything, and the first sign of it would be a process.
//
// That is the same shape package managers spent years closing. npm's install
// scripts ran by virtue of a package being in the tree; bun and pnpm answered
// it with a trusted-dependency list, where an unapproved package is installed
// and its lifecycle scripts simply do not run until somebody says so. rta's
// version of "a lifecycle script" is the Describe handshake, because that is
// the point at which a stranger's code executes.
//
// So an artifact runs when the operator has said this artifact may run:
//
//	rta plugin trust weather
//
// # Keyed on the digest, because a name is not an artifact
//
// The same rule `--allow-destructive <id>@<digest>` and
// `plugins.<ns>@<digest>` already follow. Trusting a *name* would
// trust whatever is called that tomorrow, which is exactly the substitution
// the check exists to notice. Rebuilding a plugin therefore requires trusting
// it again — the same friction pnpm has on a version bump, and it is the
// feature rather than the cost: a plugin's bytes changing under a name you
// already approved is the event worth stopping for.
//
// # Kept where rta writes, not where people share
//
// The data directory, beside the grant file and the selection — never the
// config file. A digest names the same bytes on every machine, so config
// would work mechanically, and that is the problem: config is the file people
// commit, copy between machines and clone from a colleague's dotfiles. Trust
// that travels in a shared file is trust nobody granted.
//
// # Evaluated at load, so a revocation lands on the next command
//
// The check runs once per process, in LoadInto, before anything is launched.
// `rta plugin untrust` therefore does not reach into a session already
// running: an `rta mcp serve` or a TUI that loaded a plugin has the process,
// and taking the approval away does not take the process. The alternative —
// re-checking on every call — would take effect only for a plugin that had not
// yet been spawned, so revocation would work sometimes and not others with
// nothing on screen to say which, and a security control that is unpredictable
// about when it applies is one nobody can reason about. One rule, said out
// loud: a running session keeps what it loaded, and the next `rta` does not.
//
// # What it is not
//
// It is not a signature, an audit, or a claim that the artifact is safe. It is
// a record that a person decided to run this exact artifact, which is the one
// fact rta can establish and the one that was missing. Everything after it is
// still the safety class, the grant, the sandbox and the operator.
package plugintrust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/internal/filelock"
	"github.com/this-is-tobi/rule-them-all/internal/paths"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Entry is one artifact the operator has approved.
type Entry struct {
	// Digest is the sha256 of the file's contents, hex. The key.
	Digest string `json:"digest"`
	// Names is every name this artifact has been trusted under, oldest first,
	// and Path is where it was last seen. Both are informational — a plugin
	// that moves is still the same artifact — and both are worth keeping,
	// because "which of these is the one I approved in March" is answered by a
	// path far better than by twelve hex characters.
	//
	// **A list rather than one name, because a revocation must not miss.**
	// One file can sit on $PATH under two names, and trusting it twice
	// recorded only the second: `rta plugin untrust <first name>` then said
	// nothing called that is trusted — while that artifact stayed trusted and
	// kept executing under the name the operator had just tried to withdraw.
	// A revocation that reports failure and leaves the approval standing is
	// the wrong half of the outcome to get wrong, and the operator is left
	// believing the opposite of what happened.
	Names []string `json:"names,omitempty"`
	// Name is the pre-Names spelling, read and never written. Folded into
	// Names on load so an existing record keeps the labels it has: dropping
	// them would reintroduce, as a migration, exactly the miss Names exists to
	// remove.
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
	// At is when it was trusted.
	At time.Time `json:"at"`
	// Allow is the credential locations this artifact may read, from rta's
	// closed set of needs.
	//
	// **A second decision, on the same key.** Trust says the bytes may run;
	// this says they may read a location the confinement otherwise denies to
	// every plugin. Two separate commands write them, because "run this" and
	// "read my kubeconfig" are different questions and one answer must not
	// stand in for the other — but both attach to the digest, so a rebuild
	// asks both again. That is the property, not the friction: a plugin's
	// bytes changing under a name you already allowed is exactly the event
	// worth stopping for, and it is the more so when what was allowed is a
	// credential store.
	Allow []string `json:"allow,omitempty"`
}

// Short is the digest as a person quotes it.
func (e Entry) Short() string {
	if len(e.Digest) > 12 {
		return e.Digest[:12]
	}
	return e.Digest
}

// Label is the name to show for this artifact: the one it was most recently
// trusted under, which is the one an operator has in mind — and the one Path
// was recorded under, so the two halves of a row describe the same thing.
//
// Add moves a repeated name to the end rather than leaving it where it first
// appeared, which is what makes "last" mean "most recent". Without that,
// trusting pg, then psql, then pg again left Names as [pg psql] and this
// returned "psql" while Path pointed at the pg binary — one row describing two
// different names.
func (e Entry) Label() string {
	if len(e.Names) == 0 {
		return ""
	}
	return e.Names[len(e.Names)-1]
}

// Knows reports whether this artifact has ever been trusted under this name.
func (e Entry) Knows(name string) bool {
	for _, n := range e.Names {
		if n == name {
			return true
		}
	}
	return false
}

// normalize folds the legacy single name in and drops duplicates, so the rest
// of the package only ever reads Names.
func (e Entry) normalize() Entry {
	var out []string
	seen := map[string]bool{}
	for _, n := range append(append([]string{}, e.Names...), e.Name) {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	e.Names, e.Name = out, ""
	return e
}

// Set is what may run, indexed by digest.
type Set struct {
	entries map[string]Entry
}

// Trusts reports whether this exact artifact has been approved.
//
// An empty digest is never trusted. That is the load-bearing zero value: a
// caller that could not hash a file must not get "yes" by default, and every
// failure path in Identify hands back an empty Identity.
func (s Set) Trusts(digest string) bool {
	if digest == "" {
		return false
	}
	_, ok := s.entries[digest]
	return ok
}

// Entries lists what is trusted, most recently trusted first.
func (s Set) Entries() []Entry {
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].Digest < out[j].Digest
	})
	return out
}

// Len is how many artifacts are trusted.
func (s Set) Len() int { return len(s.entries) }

// file is the on-disk shape, a separate type so the format can gain a field
// without every reader having to care.
type file struct {
	Trusted []Entry `json:"trusted"`
}

// Path is where the record lives.
func Path() string { return filepath.Join(paths.Data(), "trusted.json") }

// mu serialises this process's writes, and a file lock serialises everybody
// else's — the same mechanism internal/grant and builtin/kv use, for the same
// reason and with the same durations.
//
// Not the trade internal/recent makes. There, a lost write costs a suggestion.
// Here the dangerous direction is a lost *untrust*: two processes read the
// same list, one removes an approval, the other adds an unrelated one, and the
// second write puts the removed entry back. An approval that resurrects itself
// is the one outcome a revocation must not have, and it is worth a lock file
// on a command somebody runs occasionally.
var mu sync.Mutex

const (
	lockStale   = 5 * time.Second
	lockRetry   = 10 * time.Millisecond
	lockTimeout = 2 * time.Second
)

func lock() (func(), *view.Error) {
	if err := os.MkdirAll(paths.Data(), 0o755); err != nil {
		return nil, view.Errorf("plugin.trust.mkdir", "creating %s: %v", paths.Data(), err)
	}
	release, err := filelock.Acquire(filepath.Join(paths.Data(), "trusted.lock"),
		lockStale, lockRetry, lockTimeout)
	if err != nil {
		return nil, view.Errorf("plugin.trust.lock", "acquiring the trust file lock: %v", err)
	}
	return release, nil
}

// Load reads what has been trusted.
//
// **Every failure answers the empty set**, and the direction is deliberate.
// This is a gate: a file that cannot be read, cannot be parsed, or has been
// truncated must cost the operator a plugin, never cost them the check. The
// opposite default — "unreadable, so allow" — is how a security control
// becomes a formality the first time a disk fills up.
// maxTrustFile bounds every read of the trust store, for the reason
// internal/atomicfile.ReadCapped states — and this is the file where that
// reason bites hardest: it decides which plugin binaries are allowed to run
// at all, and Load's own contract below is that an unreadable store costs the
// operator a plugin rather than the check. An oversized store now takes that
// same fail-closed path instead of taking the process with it.
//
// An entry is a digest, a path and a few fields; 256 KiB is hundreds of
// trusted artifacts, matched to the cap its sibling state files use.
const maxTrustFile = 256 << 10

func Load() Set {
	s := Set{entries: map[string]Entry{}}
	data, err := atomicfile.ReadCapped(Path(), maxTrustFile)
	if err != nil {
		return s
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return s
	}
	for _, e := range f.Trusted {
		if e.Digest == "" {
			continue
		}
		s.entries[e.Digest] = e.normalize()
	}
	return s
}

// Add records an artifact as trusted. Trusting one already trusted refreshes
// where it was seen and remembers the name it was trusted under this time,
// and is not an error: re-running the command after moving a binary is a
// reasonable thing to do and refusing it teaches nothing.
func Add(digest, name, path string) *view.Error {
	if digest == "" {
		return view.Errorf("plugin.trust.nodigest", "cannot trust an artifact with no digest")
	}
	mu.Lock()
	defer mu.Unlock()
	release, verr := lock()
	if verr != nil {
		return verr
	}
	defer release()
	f, verr := read()
	if verr != nil {
		return verr
	}
	e := Entry{Digest: digest, Path: path, At: time.Now().UTC().Truncate(time.Second)}
	out := make([]Entry, 0, len(f.Trusted)+1)
	for _, existing := range f.Trusted {
		if existing.Digest != digest {
			out = append(out, existing)
			continue
		}
		// Every name it has carried, so a revocation typed against any of
		// them finds it.
		e.Names = existing.normalize().Names
		// And whatever it was allowed to read. Re-trusting the same bytes is
		// a refresh of where they were seen, so dropping the grant here would
		// make `rta plugin trust` a silent revocation — the operator's own
		// artifact, still the same digest, quietly losing access with nothing
		// said. The digest is what both decisions hang on; while it stands,
		// both stand.
		e.Allow = append([]string{}, existing.Allow...)
	}
	if name != "" {
		// Appended after dropping any earlier occurrence, so the list stays in
		// least-to-most-recently-used order and Label means what it says.
		kept := e.Names[:0]
		for _, n := range e.Names {
			if n != name {
				kept = append(kept, n)
			}
		}
		e.Names = append(kept, name)
	}
	return write(file{Trusted: append(out, e.normalize())})
}

// Allowed is the credential locations an artifact may read, empty for one that
// has been granted none — which is every artifact until somebody says
// otherwise.
func (s Set) Allowed(digest string) []string {
	for _, e := range s.entries {
		if e.Digest == digest {
			return append([]string{}, e.Allow...)
		}
	}
	return nil
}

// Allow records that an artifact may read the named locations, replacing
// whatever it was allowed before rather than adding to it.
//
// Replacing, because the command states the whole grant: adding would make a
// location impossible to withdraw without withdrawing all of them, and a
// permission you can only accumulate is one nobody trims. `rta plugin
// disallow` is the empty case spelled as its own command, so taking access
// away is a thing somebody typed rather than a subtlety of an argument list.
//
// Refused for an artifact that is not trusted: allowing bytes to read a
// credential store while they are not even allowed to run is a record that
// means nothing and would outlive the reason it was written.
func Allow(digest string, locations []string) *view.Error {
	if digest == "" {
		return view.Errorf("plugin.allow.nodigest", "cannot allow an artifact with no digest")
	}
	mu.Lock()
	defer mu.Unlock()
	release, verr := lock()
	if verr != nil {
		return verr
	}
	defer release()
	f, verr := read()
	if verr != nil {
		return verr
	}
	found := false
	out := make([]Entry, 0, len(f.Trusted))
	for _, e := range f.Trusted {
		if e.Digest == digest {
			found = true
			e.Allow = append([]string{}, locations...)
			if len(e.Allow) == 0 {
				e.Allow = nil
			}
		}
		out = append(out, e)
	}
	if !found {
		return view.Errorf("plugin.allow.untrusted",
			"that artifact is not trusted, so there is nothing to allow it").
			WithHint("`rta plugin trust <name>` first — running it at all is the decision " +
				"that comes before reading anything")
	}
	return write(file{Trusted: out})
}

// minDigestPrefix is the shortest prefix Remove accepts as naming a digest,
// matching the pin-length floor internal/mcp, internal/profile and
// internal/pluginconf all use for the same reason: short enough to type,
// long enough that a collision is not something to plan around.
const minDigestPrefix = 8

// fullDigestLen is a sha256 hex digest's length. A string this long can
// only equal one artifact's digest — cryptographic collision aside — so
// only a shorter string is a prefix Remove has to check for ambiguity.
const fullDigestLen = 64

// Remove withdraws trust from an artifact, by digest or by the name it was
// trusted under. It reports how many entries went.
//
// By name as well as by digest because that is how somebody will reach for
// it: an operator taking back a plugin after a rebuild has a name in their
// head, and every digest that name ever had is a thing they want gone —
// deliberately more than one entry when that is what the name matches. A
// digest given in full or as a prefix is a different promise: matched
// exactly, so `rta plugin untrust 1a2b3c4d` means one artifact and nothing
// else, which is why a prefix short enough to match more than one trusted
// digest is refused up front rather than quietly taking all of them.
func Remove(which string) (int, *view.Error) {
	if strings.TrimSpace(which) == "" {
		return 0, view.Errorf("plugin.untrust.empty", "nothing named")
	}
	mu.Lock()
	defer mu.Unlock()
	release, verr := lock()
	if verr != nil {
		return 0, verr
	}
	defer release()
	f, verr := read()
	if verr != nil {
		return 0, verr
	}
	if len(which) >= minDigestPrefix && len(which) < fullDigestLen {
		var matches []string
		for _, e := range f.Trusted {
			if strings.HasPrefix(e.Digest, which) {
				matches = append(matches, e.Digest)
			}
		}
		if len(matches) > 1 {
			return 0, view.Errorf("plugin.untrust.ambiguous",
				"%q matches %d trusted artifacts, not one", which, len(matches)).
				WithHint("name one: " + strings.Join(matches, ", "))
		}
	}
	var kept []Entry
	removed := 0
	for _, e := range f.Trusted {
		// Any name it has ever been trusted under, not just the most recent:
		// the operator is withdrawing a plugin, and every name it has carried
		// is a name they might reach for.
		if e.normalize().Knows(which) ||
			(len(which) >= minDigestPrefix && strings.HasPrefix(e.Digest, which)) {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	if removed == 0 {
		return 0, nil
	}
	if verr := write(file{Trusted: kept}); verr != nil {
		return 0, verr
	}
	return removed, nil
}

// read loads the file for a read-modify-write.
//
// **Unlike Load, every failure here is fatal to the write**, and the asymmetry
// is the point. Load answers the question "may this run", so it fails closed:
// an unreadable record trusts nothing. This answers "what is already
// approved", and failing open in the same direction means an unreadable or
// half-parsed file yields an empty list — which the caller then writes back
// with one entry in it, destroying every prior approval and reporting success.
// A hand-edited file, a file another user owns in a writable directory, or a
// future format change would each have done it silently.
//
// A file that is simply not there is not a failure: that is a machine with
// nothing trusted yet.
func read() (file, *view.Error) {
	var f file
	data, err := atomicfile.ReadCapped(Path(), maxTrustFile)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, view.Errorf("plugin.trust.unreadable", "reading %s: %v", Path(), err).
			WithHint("rta will not rewrite a record it could not read, because that would " +
				"withdraw every approval already in it")
	}
	if len(data) == 0 {
		return f, view.Errorf("plugin.trust.empty", "%s is empty", Path()).
			WithHint("delete it to start over — `rta plugin trust` then lists what is waiting")
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, view.Errorf("plugin.trust.invalid", "parsing %s: %v", Path(), err).
			WithHint("delete it to start over — every plugin then needs approving again, " +
				"which is the safe direction")
	}
	return f, nil
}

func write(f file) *view.Error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return view.Errorf("plugin.trust.encode", "encoding %s: %v", Path(), err)
	}
	if err := os.MkdirAll(paths.Data(), 0o755); err != nil {
		return view.Errorf("plugin.trust.mkdir", "creating %s: %v", paths.Data(), err)
	}
	// 0600, like the grant file. It is not a secret, and it is a list of what
	// this machine will execute — which is nobody else's business to read on a
	// shared box, and nobody else's business at all to write.
	if err := atomicfile.Write(Path(), data, 0o600); err != nil {
		return view.Errorf("plugin.trust.write", "writing %s: %v", Path(), err)
	}
	return nil
}
