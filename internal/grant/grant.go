// Package grant is consent for AI agents: which capabilities an MCP caller
// may invoke, on which records, until when.
//
// The safety class an operator opts into (`--allow-write`, an allowlist of
// destructive IDs) is a decision taken once, when the server is launched, for
// every call it will ever make. That is the coarse half. A grant is the fine
// half: it names one capability — optionally one record — carries a deadline,
// and can only be issued by a person at a terminal. An agent that could grant
// itself access would make the whole mechanism theatre, so the capabilities
// that issue grants refuse to run over MCP.
//
// This started as a kv-only feature, because a leaked password is the most
// obvious harm. It is not the only one: an agent that empties a task list or
// repoints /etc/hosts has also done something nobody asked for. So the gate
// lives here, next to the surface it defends, and is enforced once in the MCP
// bridge rather than by each plugin remembering to ask.
//
// The file is plaintext on purpose. It holds no secret — capability names,
// record names and timestamps — and it must be readable *without* unlocking
// anything, so "what is the agent allowed to do right now?" stays answerable
// in a hurry.
package grant

import (
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/internal/filelock"
	"github.com/this-is-tobi/rule-them-all/internal/paths"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

const (
	file = "grants.json"
	// DefaultTTL is short on purpose: a grant is for the task at hand.
	DefaultTTL = 15 * time.Minute
	// MaxTTL caps how long a grant can last. A day is already generous for
	// something whose entire point is that it expires.
	MaxTTL = 24 * time.Hour
)

// Grant authorizes one kind of call until it expires.
type Grant struct {
	// Target is a capability ID ("kv.get") or a plugin name ("kv"), which
	// covers every capability in it.
	Target string `json:"target"`
	// Scope narrows the grant to one record — the key, task or hostname the
	// capability names. Empty means every record the target can reach, which
	// is the wider and rarer thing to want.
	Scope string `json:"scope,omitempty"`
	// Profile narrows the grant to one of the operator's named connections.
	//
	// Empty means the call must name NO profile — the operator's base
	// configuration — and nothing else. **It is not a wildcard, and there is
	// no wildcard.**
	//
	// Scope's empty-means-every-record deliberately does not transfer. That is
	// a *record* wildcard inside a connection the operator already chose,
	// which is what makes it tolerable; a profile wildcard would be a
	// *connection* wildcard — one grant issued while pointed at a scratch
	// database authorizing the identical call against production. That is
	// D94's credential redirect rebuilt from the grant side.
	//
	// It is also the only reading under which this field's arrival is safe for
	// grants already on disk: they unmarshal with Profile empty and keep
	// covering exactly the unprofiled calls they cover today, gaining nothing.
	// Under "empty means any" every one of them would silently widen to every
	// connection added afterwards.
	//
	// omitempty is load-bearing for the seal, not decoration. canonical() is
	// json.Marshal over the parsed []Grant, so a field that is the zero string
	// on every stored grant is omitted and re-encodes byte-identically, and
	// every existing seal still verifies. Without it old rows re-encode with
	// "profile":"", fail hmac.Equal, and rta reports its own file as forged.
	Profile string `json:"profile,omitempty"`
	// ProfilePin fingerprints the connection this grant was issued against:
	// profile.ConnStamp of the entry for this grant's namespace.
	//
	// **Because Profile is a name, and a name is not a connection.** Editing
	// an environment's `host`, `endpoint` or `secrets:` mapping repoints every
	// live grant naming it, silently: the operator consented to a call
	// reaching staging, and the identical grant now authorizes it against
	// whatever that name means afterwards. ADR 0019 and ADR 0020 both record
	// this as the thing to build, "required the moment stage 2 lands" — the
	// moment a connection can also carry cluster coordinates and a credential
	// read out of that cluster.
	//
	// It is the same rule the rest of rta already follows for artifacts:
	// `--allow-destructive <id>@<digest>` (ADR 0015) and `plugins.<ns>@<digest>`
	// (ADR 0016) bind an authorization to a thing rather than to a label. This
	// binds it to a *connection* for the same reason and by the same means.
	//
	// **Required exactly when Profile is set.** An unprofiled grant keeps an
	// empty profile and an empty pin and is untouched, which is also what
	// leaves the field free for the seal. Both other readings are wrong:
	// "empty matches anything" rebuilds the hole for every grant issued before
	// this and makes the empty pin the default a blind-writing attacker
	// produces — ADR 0019's no-wildcard argument, one field along — and
	// "empty matches nothing" would refuse every live profiled grant on
	// upgrade. Fail-closed, self-healing within one TTL.
	//
	// Not re-stamped at load to smooth that upgrade, for legacy()'s own
	// reason: a migration that re-seals what it finds is the same hole with
	// more steps, because re-stamping binds the grant to whatever the config
	// says *now*, which is precisely the repoint this exists to catch.
	//
	// **What it can and cannot promise.** It is content-addressed, so it
	// answers "this differs from what was consented to" and not "this has been
	// edited since": a profile changed and changed back matches again. It
	// binds content and never provenance — Lookup's Trusted() check still
	// stands in front of it. And no hash over a config file can see
	// `RTA_PROFILE_<NAME>_<INPUT>`, so the claim is scoped to the configured
	// connection. See profile.ConnStamp.
	//
	// omitempty for the seal, exactly as Profile above documents: canonical()
	// is json.Marshal over the parsed []Grant, so a field that is the zero
	// string on every stored grant re-encodes byte-identically and every
	// existing seal still verifies.
	ProfilePin string    `json:"profilePin,omitempty"`
	Issued     time.Time `json:"issued"`
	Expires    time.Time `json:"expires"`
	Note       string    `json:"note,omitempty"`
	// TTL is the window as the operator typed it ("15m", "1h"), so renew can
	// extend by the same amount rather than guess at one.
	//
	// Empty on every grant sealed before this field existed; renew falls back
	// to Expires.Sub(Issued), which for a grant that has never been renewed is
	// exactly the window it was issued with.
	TTL string `json:"ttl,omitempty"`
	// MaxUses caps how many successful calls this grant authorizes before it
	// is spent, on top of Expires. Zero means unlimited within the TTL — the
	// behavior of every grant issued before this field existed, and the
	// common case today: "for the next 15 minutes" needs no counting.
	MaxUses int `json:"maxUses,omitempty"`
	// Uses counts what has been spent so far. Reserve increments it *before*
	// the call runs, under the lock that authorized it, and hands back a
	// release() that gives the use back if the call then fails — a call
	// refused for an unrelated reason (the capability itself failed, the
	// process was killed mid-run) must not spend a one-time grant that
	// revealed nothing. Incrementing afterwards was the obvious ordering and
	// the wrong one: it left the decision and the spend in different critical
	// sections, so two concurrent calls both read Uses=0 and both ran.
	Uses int `json:"uses,omitempty"`
}

// Active reports whether the grant still stands at now: its TTL has not
// passed, and — if it names a use limit — it has not been spent.
func (g Grant) Active(now time.Time) bool {
	// MaxTTL is applied here and not only where a grant is issued. Checking it
	// at issue alone means the cap lives in the CLI and the file is trusted to
	// have been written by it — so a grant claiming to expire in 2099 was
	// honoured for seventy years by a rule that reads "a day is already
	// generous". Enforcing it on the way out makes the cap a property of what
	// a grant can *do* rather than of how one is asked for, which is the only
	// version that survives the file being written by something else.
	return now.Before(g.Expires) && now.Before(g.Issued.Add(MaxTTL)) &&
		(g.MaxUses == 0 || g.Uses < g.MaxUses)
}

// Window is the lifetime this grant was issued with, for renew to extend by
// the same amount rather than guess.
//
// TTL when it is recorded; otherwise Expires-Issued, which for a grant that
// has never been renewed is exactly the window it was issued with, and is the
// only answer available for one sealed before TTL existed. Clamped to MaxTTL
// so a hand-edited file cannot turn a renewal into a longer grant than any
// person could have asked for.
func (g Grant) Window() time.Duration {
	if g.TTL != "" {
		if d, err := time.ParseDuration(g.TTL); err == nil && d > 0 {
			return min(d, MaxTTL)
		}
	}
	if d := g.Expires.Sub(g.Issued); d > 0 {
		return min(d, MaxTTL)
	}
	return DefaultTTL
}

// covers reports whether this grant authorizes a call of capID on scope,
// running against profile.
func (g Grant) covers(capID, scope, profile string) bool {
	if g.Target != capID && g.Target != Namespace(capID) {
		return false
	}
	// Exact, in both directions, with no g.Profile == "" escape hatch. A grant
	// for "staging" does not authorize a call naming no profile, and a grant
	// naming no profile does not authorize a call on "staging". See the field
	// comment: this is what makes the arrival of Profile safe for every grant
	// already sealed on disk.
	if g.Profile != profile {
		return false
	}
	// A scoped grant authorizes that record and nothing else — including a
	// call that names no record at all, which is by definition wider.
	return g.Scope == "" || g.Scope == scope
}

// Covering returns the first grant in grants that would authorize a call to
// capID on scope, or nil if none does.
//
// It is covers() made visible outside the package, for the one caller that
// legitimately needs to ask the question in the other direction: not "is
// this call allowed" but "after I remove some grants, is this target still
// allowed by what's left". grant.revoke uses it to say so honestly — a row
// naming exactly the target it was asked to revoke can be gone while a
// wider grant still authorizes every call that target would ever make, and
// "No active grant for X" is the wrong thing to print when that is true.
func Covering(grants []Grant, capID, scope, profile string) *Grant {
	for i := range grants {
		if grants[i].covers(capID, scope, profile) {
			return &grants[i]
		}
	}
	return nil
}

// Namespace is the plugin part of a capability ID: the coarsest thing a grant
// may name.
//
// Delegates to plugin.Namespace rather than deriving it again. Two copies of
// "the part before the first dot" is one too many once internal/profile needs
// the same answer to decide which plugin a profile configures.
func Namespace(capID string) string { return plugin.Namespace(capID) }

// Normalize accepts the forms people type for a target — "kv", "kv.*",
// "kv.get" — and returns the stored one.
func Normalize(target string) string {
	return strings.TrimSuffix(strings.TrimSpace(target), ".*")
}

// Path is where grants are kept.
func Path() string { return filepath.Join(paths.Data(), file) }

// Load returns the grants that are still active; expired and fully-spent ones
// are dropped on read, so nothing has to sweep them.
func Load() ([]Grant, *view.Error) {
	all, verr := loadAll()
	if verr != nil {
		return nil, verr
	}
	now := time.Now()
	active := make([]Grant, 0, len(all))
	for _, g := range all {
		if g.Active(now) {
			active = append(active, g)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].Expires.Before(active[j].Expires) })
	return active, nil
}

// loadAll returns every stored grant, including the ones Load hides.
//
// A grant spent to its last use stops being Active, so Load can no longer see
// it — which is right for every caller that asks "what is allowed" and wrong
// for the one that has to give a use back after a failed call. Refunding
// needs the record Load has already filtered away.
func loadAll() ([]Grant, *view.Error) {
	data, err := os.ReadFile(Path())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, view.Errorf("core.grant.unreadable", "reading %s: %v", Path(), err)
	}
	if legacy(data) {
		// A grant file from before the seal. Every grant in it is dropped and
		// nothing is refused — see seal.go for why that is neither honouring
		// it nor erroring on it.
		return nil, nil
	}
	var doc sealed
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, view.Errorf("core.grant.corrupt", "parsing %s: %v", Path(), err).
			WithHint("delete the file to clear every grant")
	}
	key, verr := sealKey(false)
	if verr != nil {
		return nil, verr
	}
	// Refused loudly rather than treated as no grants. Falling back to empty
	// would be safe for authorization and wrong for the operator: a tampered
	// file would look exactly like "you have not issued any", so the one
	// moment worth noticing would present as the ordinary case.
	canon, err := canonical(doc.Grants)
	if err != nil {
		return nil, view.Errorf("core.grant.corrupt", "re-encoding %s: %v", Path(), err)
	}
	if !hmac.Equal([]byte(doc.Seal), []byte(seal(key, canon))) {
		// Nothing is honoured either way; only the sentence differs. A file
		// carrying fields this build never declared fails the seal because
		// canonical() re-encodes what it parsed and drops them — which is what
		// an ordinary downgrade looks like from here, and accusing the
		// operator's own newer rta of forgery while telling them to delete
		// every grant is the wrong reading of it. See unknown().
		if extra := unknown(data); len(extra) > 0 {
			return nil, view.Errorf("core.grant.unknownfields",
				"%s carries grant fields this rta does not know (%s), so its seal cannot be "+
					"checked — it was written by a newer rta, or it has been modified",
				Path(), strings.Join(extra, ", ")).
				WithHint("no grant is honoured either way; upgrade rta to read it, or `rm " +
					Path() + "` to clear every grant and re-issue what you still need")
		}
		return nil, view.Errorf("core.grant.forged",
			"%s does not match its seal — it was written by something other than rta", Path()).
			WithHint("no grant is honoured until this is resolved; `rm " + Path() +
				"` clears every grant, and any that were legitimate can be re-issued")
	}
	return doc.Grants, nil
}

// Save replaces the grant file.
//
// Written atomically (temp file, then rename over the target): the grant lock
// only ever serializes the writers, and a reader — Load, called from Reserve's
// unlocked fast path on every gated MCP call — can still land mid-write
// against a plain os.WriteFile, which truncates before it writes.
// A reader that races a torn file sees valid JSON either way: the old
// complete grants, or the new ones, never a half-written one.
func Save(grants []Grant) *view.Error {
	dir := paths.Data()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return view.Errorf("core.grant.write", "creating %s: %v", dir, err)
	}
	canon, err := canonical(grants)
	if err != nil {
		return view.Errorf("core.grant.write", "encoding grants: %v", err)
	}
	key, verr := sealKey(true)
	if verr != nil {
		return verr
	}
	data, err := json.MarshalIndent(sealed{Seal: seal(key, canon), Grants: grants}, "", "  ")
	if err != nil {
		return view.Errorf("core.grant.write", "encoding grants: %v", err)
	}
	// 0600 enforced, not requested: this file is what an agent's authority
	// is read from, so it must not become readable — or writable — because
	// of a permissive umask.
	if err := atomicfile.Write(Path(), data, 0o600); err != nil {
		return view.Errorf("core.grant.write", "writing %s: %v", Path(), err)
	}
	return nil
}

// Mutate rewrites the grant file under the lock: it hands f every stored
// grant and saves what f gives back, unless f declines.
//
// It exists because grant.allow and grant.revoke did the same Load-mutate-Save
// round trip with no lock at all, on the file that decides what an AI agent may
// do. A revoke landing inside Reserve's post-lock Load..Save was read, filtered
// and written by the revoker, then written back by Reserve from the snapshot it
// had taken a moment earlier — so `rta grant revoke kv.get` said "revoked 1
// grant(s)" and the grant was in the file again a millisecond later, still
// authorizing calls, still listed by `grant list`. The window is small and
// needs a --max-uses grant to exist at all, which is not a reason to keep it.
// The lock lives here rather than around each caller's round trip so the
// discipline is a property of the package: a capability that edits grants
// tomorrow cannot forget it, because there is no other way to write the file.
//
// f sees loadAll's answer, not Load's. A grant spent to its last use is no
// longer Active, so Load hides it — and a writer that round-trips through Load
// deletes every row it was not shown, merely by saving. That row is exactly the
// one refund reaches for when the guarded call fails, so the use would never
// come back and a one-time grant that revealed nothing would stay spent.
// Deciding over the whole file also means f drops a dead row because it meant
// to, not because of what it happened to be handed.
//
// Returning false stores nothing, which is how --dry-run and "there was nothing
// to revoke" get their answer: what the person is told is decided under the
// same lock as the write it declines, so the message and the file cannot
// disagree.
func Mutate(f func([]Grant) ([]Grant, bool)) *view.Error {
	unlock, verr := acquireLock()
	if verr != nil {
		return verr
	}
	defer unlock()
	stored, verr := loadAll()
	if verr != nil {
		return verr
	}
	next, save := f(stored)
	if !save {
		return nil
	}
	return Save(next)
}

// Required reports whether a capability needs a grant before an agent may
// call it, given the profile the call names.
//
// Destructive is implicit: a capability that permanently removes something is
// exactly what a standing allowlist should not be enough for. Everything else
// opts in, which is how kv.get — a read by the letter of the safety model,
// a leak in practice — ends up here too.
//
// **Naming a profile is itself enough**, and that clause is the whole of the
// profiles feature. plugins/pg declares no NeedsGrant and all six of its
// capabilities are Safety: Read — honestly, since pg.query runs inside a READ
// ONLY transaction — so without it a profile-aware covers() would do nothing
// for pg at all, and "read the database the operator configured" would
// quietly become "read any database the operator has configured".
//
// The alternative was marking pg's capabilities NeedsGrant, which is wrong in
// the other direction: it would gate `rta pg status` against the operator's
// own localhost, where nobody consented to anything because nothing left the
// machine. The requirement belongs to the connection, not to the capability —
// so the zero-config path stays exactly as frictionless as it is today, and
// consent is required at precisely the moment a call reaches somewhere else.
func Required(c plugin.Capability, profile string) bool {
	return profile != "" || c.NeedsGrant || c.Safety == plugin.Destructive
}

// scopes reads the records a call names, from the input the capability
// declared as its scope. A capability with no scope, or a call that names no
// record, yields one empty scope: the call is about the capability itself.
//
// Deduplicated, not just collected: a record named twice in one call (a
// StringSlice-typed scope repeating a value, by mistake or on purpose) used
// to make spend() walk that scope twice in the same call, incrementing
// MaxUses' Uses counter once per occurrence rather than once per record —
// letting a --max-uses 1 grant authorize itself twice within a single call
// that named the same key two ways. Found by review (PROJECT.md D74) and
// reproduced directly against Reserve.
func scopes(c plugin.Capability, values map[string]any) []string {
	if c.Scope == "" {
		return []string{""}
	}
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	switch v := values[c.Scope].(type) {
	case string:
		add(v)
	case []string:
		for _, s := range v {
			add(s)
		}
	case []any:
		for _, raw := range v {
			if s, ok := raw.(string); ok {
				add(s)
			}
		}
	case nil:
	default:
		add(numericScope(v))
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// numericScope renders a non-string scope value (an Int-typed Scope field,
// e.g. todo.rm's id) the way an operator would type it, not the way Go's
// %v verb does — fmt.Sprint(float64(1000000)) is "1e+06", which a grant
// issued for the operator-typed string "1000000" (rta grant allow todo.rm
// 1000000) would never match. An MCP call's JSON number always decodes to
// float64 before it reaches here (internal/mcp/bridge.go calls Reserve on
// the raw decoded values, before plugin.Resolve's numeric coercion), so this
// is the boundary that has to normalise it. A whole-number float is printed
// as a plain integer; anything with a fractional part falls back to %v,
// since no Field.Type this package scopes against is ever a genuine Float.
// Found by review (PROJECT.md D74), which demonstrated the mismatch
// directly rather than only asserting it existed.
func numericScope(v any) string {
	if f, ok := v.(float64); ok && f == math.Trunc(f) {
		return strconv.FormatInt(int64(f), 10)
	}
	return fmt.Sprint(v)
}

// allocate walks the scopes a call names and, for each, greedily picks the
// first grant that covers it and can still afford one more use in this same
// call — a grant already spoken for elsewhere in the call, right up to its
// MaxUses, no longer counts as covering a further scope, so allocation falls
// through to the next grant that does cover it. It returns, per grant index
// touched, how many additional uses this call would spend, and the scopes
// nothing with budget left covers.
//
// checkAgainst and spend used to walk scopes independently — checkAgainst
// asking only "does anything cover this", spend incrementing whatever it
// found with no memory of what earlier scopes in the same call had already
// claimed. A single MaxUses:1 grant with no Scope (or a Scope wide enough to
// cover several records) covered every scope a multi-record call like
// `kv.env key=[a,b,c]` named, so checkAgainst approved the whole call and
// spend then walked the same grant three times in the one Save it made — a
// grant issued to reveal one secret once revealing three, with the budget it
// was capped at blown past in a call that never touched the lock twice.
// Sharing one walk and one tally is what keeps the authorization decision
// and the spend decision looking at the same arithmetic.
func allocate(c plugin.Capability, values map[string]any, grants []Grant, profile string) (tally map[int]int, missing []string) {
	tally = map[int]int{}
	for _, scope := range scopes(c, values) {
		covered := false
		for i, g := range grants {
			if !g.covers(c.ID, scope, profile) {
				continue
			}
			if g.MaxUses > 0 && g.Uses+tally[i] >= g.MaxUses {
				continue // this grant's budget is spoken for by this same call; try another
			}
			if g.MaxUses > 0 {
				tally[i]++
			}
			covered = true
			break
		}
		if !covered {
			missing = append(missing, scope)
		}
	}
	return tally, missing
}

// checkAgainst answers "is this call authorized" against a set of grants
// already in hand, so that the authorization decision can be made under the
// same lock that spends the use.
//
// Every record a call names needs its own cover — `kv env a b c` with a grant
// for `a` is two thirds of a leak, not a partial success.
//
// **Unexported, and it stays that way.** There used to be a Check() wrapping
// this that did its own Load, and it was the package's second gate: it applied
// the pin filter but not the active bound, so the two exported entry points
// answered differently about the same call. Nothing called it — the bridge has
// always called Reserve — but its own doc comment claimed it was "the whole
// enforcement", which is the sentence that would have sent the next caller to
// the weaker of the two. A gate reachable only through the function that
// spends the use cannot drift from it.
func checkAgainst(c plugin.Capability, values map[string]any, grants []Grant, profile string) *view.Error {
	_, missing := allocate(c, values, grants, profile)
	if len(missing) == 0 {
		return nil
	}
	// The hint has to name the profile, because a grant that does not name it
	// authorizes nothing: covers() matches the profile exactly, so
	// `rta grant allow pg.status --ttl 15m` issued for a call on "prod"
	// produces a row that looks right in `grant list` and refuses every call it
	// was issued for. A refusal that hands somebody a command which does not
	// fix the problem is worse than one that hands them nothing — they run it,
	// see success, and go looking for the cause somewhere else.
	//
	// The subject is the namespace rather than the capability when a profile is
	// in play: an operator granting access to a connection almost never means
	// "and only the status call".
	return refuseMissing(c, missing, profile)
}

// samePin compares two connection fingerprints.
//
// **An empty pin matches nothing, including another empty pin**, and that
// asymmetry with ordinary string equality is the whole control. Two ways of
// arriving at empty had to be closed, and `==` closed neither:
//
//   - A profiled grant issued before this field existed carries no pin. Left
//     equal to an empty computed stamp it would be honoured, which is exactly
//     the "empty means any" reading Profile's own comment rejects one field up.
//   - ConnStampFor answers empty for a profile that has been deleted, renamed,
//     or stripped of the plugin. A grant naming a connection that no longer
//     exists must authorize nothing rather than everything.
//
// The same load-bearing zero value plugintrust.Set.Trusts uses, for the same
// reason: a caller that could not compute the thing being compared must not
// get "yes" by default.
func samePin(a, b string) bool { return a != "" && a == b }

// reachable drops the grants this call cannot be authorized by, and reports
// each survivor's position in the set it came from.
//
// Two subtractions, and they were two functions until the second one made the
// case for merging them:
//
//   - **active** — the environment the operator has switched on. While they are
//     working in one place, a grant naming any other profile does not count.
//   - **pin** — the connection this call will actually be filled from. A grant
//     issued against a different one does not count, because the name it
//     consented to now resolves somewhere else.
//
// **Both are filters on the grant set, not checks beside it**, and that is the
// whole design. The active bound started as a short-circuit in internal/mcp
// that refused before Reserve, and it was wrong in two ways at once. It
// compared `profileName != active` without excluding the empty profile, so
// switching *anything* on refused every call that named no profile — most of
// the catalogue — and blacked out the whole agent surface with a hint telling
// the operator to issue grants that could not help. And even correct, it
// produced its own refusal, which named every scope the call carried while the
// real check names only the uncovered ones: an agent holding a partial grant
// could tell the two apart and read "the operator is working somewhere else"
// off the difference.
//
// The pin has the same shape and a sharper version of the same leak. A second
// refusal saying "this profile changed since you were granted" separates
// "granted, then edited" from "never granted", disclosing to an agent both that
// the profile exists and that consent was once given for it. Filtering removes
// the second refusal by construction, so the sentence an agent sees is the
// identical one a call with no grant at all gets, out of the same allocate()
// over the same tally.
//
// **It can only subtract**: nothing here adds a grant, widens one, or changes
// which connection a call is filled from. A call naming no profile still
// matches the grants that name none, because covers() already compares the two
// exactly. A grant with a profile and no pin is one issued before the field
// existed, and it is dropped rather than honoured — fail-closed, and it heals
// when the operator re-consents, which is within one TTL because MaxTTL is a
// day.
//
// pin is the stamp of the connection *this call resolves through*, computed by
// the caller from the same config the fill will read. That matters where the
// two can differ: `rta mcp serve` snapshots profiles at startup (so that a
// profile removed from the file stops being reachable at the next start), and
// a pin taken from a fresh read would refuse every call on an environment
// edited since — with "re-issue the grant" as a remedy that could never work,
// because the server would keep computing the older stamp. Computed from what
// the call will use, a mismatch means what it says.
//
// **The positions are the point.** The decision is made over what is left — but
// the *write* goes back to the whole file, and a caller that saved the filtered
// slice would delete every row the filters dropped. That is not hypothetical:
// Reserve did exactly that, so one use-limited call erased the operator's other
// grants, including the stale-pinned row `rta grant list` and `rta doctor`
// exist to show them. The hazard is the one Mutate's own comment names — "a
// writer that round-trips through Load deletes every row it was not shown,
// merely by saving" — reached through two filters instead of one.
//
// Returning positions rather than a filtered copy makes the mistake hard to
// repeat: there is no subset to hand to Save. Merging the two filters into one
// function is the other half of it — the pair used to be callable separately,
// and the one caller that applied only one of them was the package's second,
// weaker gate.
func reachable(grants []Grant, profile, pin, active string) (view []Grant, at []int) {
	for i, g := range grants {
		if active != "" && g.Profile != "" && g.Profile != active {
			continue
		}
		if profile != "" && g.Profile == profile && !samePin(g.ProfilePin, pin) {
			continue
		}
		view = append(view, g)
		at = append(at, i)
	}
	return view, at
}

// identity names a grant across a reload.
//
// The fields runAllow dedupes on, plus the moment of consent — which together
// are what makes two rows the same decision rather than two decisions that
// happen to look alike. Needed because a refund runs after the grant file has
// been written and re-read, so an index means nothing by then, and matching by
// "the first row that covers this" is what let a refund give back a use it
// never spent.
type identity struct {
	target, scope, profile, pin string
	issued                      time.Time
}

func (g Grant) identity() identity {
	return identity{g.Target, g.Scope, g.Profile, g.ProfilePin, g.Issued}
}

// spentUse records that this call took n uses from one specific grant, so the
// refund can give back exactly that and nothing else.
type spentUse struct {
	who identity
	n   int
}

// Stale reports whether this grant names a connection that no longer matches
// the one it was issued against.
//
// For the surfaces where a *person* is looking — `rta grant list`, `rta
// doctor` — which is where the remedy belongs, because the remedy names the
// profile and the agent-facing refusal deliberately does not. Computed by
// asking the same filter the gate asks, so a row cannot be marked stale by one
// rule and refused by another.
func (g Grant) Stale(pin string) bool {
	return g.Profile != "" && !samePin(g.ProfilePin, pin)
}

// refuseMissing is the sentence a call gets when nothing authorizes it.
func refuseMissing(c plugin.Capability, missing []string, profile string) *view.Error {
	if len(missing) == 0 {
		missing = []string{""}
	}
	// The hint has to name the profile, because a grant that does not name it
	// authorizes nothing: covers() matches the profile exactly, so
	// `rta grant allow pg.status --ttl 15m` issued for a call on "prod"
	// produces a row that looks right in `grant list` and refuses every call it
	// was issued for. A refusal that hands somebody a command which does not
	// fix the problem is worse than one that hands them nothing — they run it,
	// see success, and go looking for the cause somewhere else.
	//
	// The subject is the namespace rather than the capability when a profile is
	// in play: an operator granting access to a connection almost never means
	// "and only the status call".
	what := strings.TrimSpace(c.ID + " " + missing[0])
	if profile != "" {
		what = strings.TrimSpace(Namespace(c.ID)+" "+missing[0]) + " --profile " + profile
	}
	return view.Errorf("core.grant.required", "no active grant for %s", describe(c.ID, missing)).
		WithHint("a person has to allow this first: rta grant allow " + what + " --ttl 15m")
}

// describe names what was refused the way the person issuing the grant will
// have to type it.
func describe(capID string, scopes []string) string {
	if len(scopes) == 1 && scopes[0] == "" {
		return capID
	}
	named := make([]string, 0, len(scopes))
	for _, s := range scopes {
		if s == "" {
			named = append(named, capID)
			continue
		}
		named = append(named, capID+" "+s)
	}
	return strings.Join(named, ", ")
}

// Reserve authorizes a call and spends its use in one atomic step, returning
// a release to call if the call then fails.
//
// **This is the gate.** Not one of two — the only exported way to ask whether
// a call is authorized, and the only thing that spends a use. It has to be one
// step. The sequence it replaces was check -> Run -> consume, with the lock
// held only by the consume: two concurrent tool calls both read Uses=0 against
// MaxUses=1, both cleared the check, and both ran. The go-sdk dispatches every
// tools/call in its own goroutine, so an agent pipelining two requests is the
// normal case rather than an exotic one — and the outcome was that a grant
// documented as "read this value exactly once" delivered the secret twice,
// then recorded a single use, leaving no trace in `grant list` that it had
// happened.
//
// Spending before the call and refunding on failure keeps the property the
// old ordering was protecting — a transient failure must not burn a one-time
// grant that delivered nothing — while closing the window, because the
// decision and the spend now happen under the same lock.
//
// Grants with no use limit (MaxUses == 0, everything issued before the field
// existed, and the overwhelming common case) never take the lock: there is
// nothing to spend, and the unlocked checkAgainst is a correct and complete
// answer for them.
//
// active is the environment the operator currently has switched on, or "", and
// pin is the connection this call resolves through. Both only ever subtract —
// see reachable().
func Reserve(c plugin.Capability, values map[string]any, profile, pin, active string) (release func(), verr *view.Error) {
	if !Required(c, profile) {
		return func() {}, nil
	}
	grants, verr := Load()
	if verr != nil {
		return nil, verr
	}
	view, _ := reachable(grants, profile, pin, active)
	if !anyLimited(view) {
		// Checked against this same snapshot — checkAgainst, not Check, which
		// would Load again. A second, independent read here reopened exactly
		// the window this function exists to close: a MaxUses grant created
		// in the gap between the two reads would be invisible to the
		// snapshot that decided nothing needed spending, yet visible (and
		// authorizing) to a fresh reload, so the call would run on a grant
		// that never recorded the use — "read this once" delivering the
		// secret for free. One read, reused for both the spend decision and
		// the authorization decision, makes that impossible: whatever this
		// call is authorized against is exactly what it already knows has
		// nothing to spend.
		return func() {}, checkAgainst(c, values, view, profile)
	}

	unlock, verr := acquireLock()
	if verr != nil {
		return nil, verr
	}
	defer unlock()

	// Re-read under the lock: the unlocked Load above is only a fast path for
	// deciding whether a lock is needed at all.
	grants, verr = Load()
	if verr != nil {
		return nil, verr
	}
	view, at := reachable(grants, profile, pin, active)
	if verr := checkAgainst(c, values, view, profile); verr != nil {
		return nil, verr
	}
	// The tally is over the view; the write is over everything Load returned.
	// Saving the view instead is how one call came to erase the operator's
	// other grants — see reachable.
	tally, missing := allocate(c, values, view, profile)
	if len(missing) > 0 || len(tally) == 0 {
		// checkAgainst just approved every scope, so missing is unreachable
		// here; a scope that could not be covered must not silently spend the
		// ones that could.
		return func() {}, nil
	}
	spent := make([]spentUse, 0, len(tally))
	for i, n := range tally {
		grants[at[i]].Uses += n
		spent = append(spent, spentUse{who: grants[at[i]].identity(), n: n})
	}
	if verr := Save(grants); verr != nil {
		return nil, verr
	}
	return func() { _ = refund(spent) }, nil
}

// refund gives back the uses a call spent, for a call that then failed.
//
// **It gives back what was taken, from the grants it was taken from.** The
// first version recomputed: it walked the call's scopes and, for each, gave a
// use back to the first grant that covered it, restarting the search every
// time. Where one call had spent uses on two different grants — an ordinary
// multi-record call against a wide grant and a narrow one — that walked into
// the same grant twice, giving back a use the call never took there and
// leaving the narrow grant paid for a call that failed. The operator's budget
// stayed the same size and moved: a use they had scoped to one record became a
// use on any record.
//
// Recomputing was wrong for a second reason, too. The state it recomputes
// against is the state *after* the spend, and other calls may have run in
// between — so "the first covering grant with a use to give" is a question
// about now, and the only correct question is what this call took.
func refund(spent []spentUse) *view.Error {
	if len(spent) == 0 {
		return nil
	}
	unlock, verr := acquireLock()
	if verr != nil {
		return verr
	}
	defer unlock()
	// loadAll, not Load: spending the last use makes a grant inactive, so the
	// record that needs the refund is exactly the one Load hides.
	grants, verr := loadAll()
	if verr != nil {
		return verr
	}
	changed := false
	for _, s := range spent {
		for i := range grants {
			if grants[i].identity() != s.who {
				continue
			}
			// Never below zero: a concurrent revoke-and-reissue could put a
			// fresh row here, and a refund must not mint uses.
			if give := min(s.n, grants[i].Uses); give > 0 {
				grants[i].Uses -= give
				changed = true
			}
			break
		}
	}
	if !changed {
		return nil
	}
	// Prune on the way out, the same rule Load applies on the way in, so a
	// refund does not resurrect anything that expired while the call ran.
	now := time.Now()
	keep := make([]Grant, 0, len(grants))
	for _, g := range grants {
		if g.Active(now) {
			keep = append(keep, g)
		}
	}
	return Save(keep)
}

func anyLimited(grants []Grant) bool {
	for _, g := range grants {
		if g.MaxUses > 0 {
			return true
		}
	}
	return false
}

const (
	lockFile = "grants.json.lock"
	// lockStale reclaims a lock left behind by a process that crashed while
	// holding it, rather than waiting on it forever.
	lockStale   = 5 * time.Second
	lockRetry   = 10 * time.Millisecond
	lockTimeout = 2 * time.Second
)

// acquireLock serializes read-modify-write access to the grant file across
// processes and goroutines: two MCP tool calls spending the same one-time
// grant at once — plausible under `rta mcp serve --http`, or two `rta mcp
// serve` processes sharing one data directory — must not both see it
// unspent and both succeed. A plain Load-then-Save round trip has no such
// guarantee. Every writer takes it: the belief that a person issuing or
// revoking a grant at a terminal is serialized by there being one person
// typing one command at a time is true of the other people and false of the
// MCP server, which is spending uses in another process the whole time — that
// assumption is what let grant.revoke's unlocked round trip lose a revocation
// to a concurrent Reserve. Writes go through Mutate, Reserve or refund, and
// all three hold this.
//
// The lock is a sentinel file, not flock(2): creating a name that cannot
// already exist behaves identically on every platform rta ships for (Linux,
// macOS, Windows), where POSIX file locking does not.
//
// **The lock is held by identity, not by name.** Every operation on the
// sentinel used to be by path — release removed whatever was there, and a
// waiter that judged the lock stale removed whatever was there — and a name
// is not the file you looked at. Two waiters both finding a crashed holder's
// lock both removed it and both created their own, so both held it; a holder
// whose lock had been broken as stale removed its successor's on the way out.
// Either way two processes are inside a read-modify-write the lock exists to
// serialize, which puts back exactly the lost revocation described above. So
// the sentinel now carries a token, acquiring it is a Publish that reports
// whose token won, releasing it is a no-op unless the token is still ours,
// and breaking a stale one moves the file first and confirms by identity that
// it moved the one it judged.
func acquireLock() (release func(), verr *view.Error) {
	path := filepath.Join(paths.Data(), lockFile)
	release, err := filelock.Acquire(path, lockStale, lockRetry, lockTimeout)
	if err != nil {
		return nil, view.Errorf("core.grant.lock", "acquiring the grant file lock: %v", err)
	}
	return release, nil
}
