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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	Scope   string    `json:"scope,omitempty"`
	Issued  time.Time `json:"issued"`
	Expires time.Time `json:"expires"`
	Note    string    `json:"note,omitempty"`
	// MaxUses caps how many successful calls this grant authorizes before it
	// is spent, on top of Expires. Zero means unlimited within the TTL — the
	// behavior of every grant issued before this field existed, and the
	// common case today: "for the next 15 minutes" needs no counting.
	MaxUses int `json:"maxUses,omitempty"`
	// Uses counts successful calls consumed so far. Consume increments it
	// only after a granted call actually succeeds — a call refused for an
	// unrelated reason (the capability itself failed, the process was
	// killed mid-run) must not spend a one-time grant that revealed nothing.
	Uses int `json:"uses,omitempty"`
}

// Active reports whether the grant still stands at now: its TTL has not
// passed, and — if it names a use limit — it has not been spent.
func (g Grant) Active(now time.Time) bool {
	return now.Before(g.Expires) && (g.MaxUses == 0 || g.Uses < g.MaxUses)
}

// covers reports whether this grant authorizes a call of capID on scope.
func (g Grant) covers(capID, scope string) bool {
	if g.Target != capID && g.Target != Namespace(capID) {
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
func Covering(grants []Grant, capID, scope string) *Grant {
	for i := range grants {
		if grants[i].covers(capID, scope) {
			return &grants[i]
		}
	}
	return nil
}

// Namespace is the plugin part of a capability ID: the coarsest thing a grant
// may name.
func Namespace(capID string) string {
	if i := strings.Index(capID, "."); i >= 0 {
		return capID[:i]
	}
	return capID
}

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
	var all []Grant
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, view.Errorf("core.grant.corrupt", "parsing %s: %v", Path(), err).
			WithHint("delete the file to clear every grant")
	}
	return all, nil
}

// Save replaces the grant file.
//
// Written atomically (temp file, then rename over the target): Consume's
// lock only ever serializes the writers, and a reader — Load, called from
// Check on every gated MCP call, with no lock of its own — can still land
// mid-write against a plain os.WriteFile, which truncates before it writes.
// A reader that races a torn file sees valid JSON either way: the old
// complete grants, or the new ones, never a half-written one.
func Save(grants []Grant) *view.Error {
	dir := paths.Data()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return view.Errorf("core.grant.write", "creating %s: %v", dir, err)
	}
	data, err := json.MarshalIndent(grants, "", "  ")
	if err != nil {
		return view.Errorf("core.grant.write", "encoding grants: %v", err)
	}
	tmp, err := os.CreateTemp(dir, ".grants-*.tmp")
	if err != nil {
		return view.Errorf("core.grant.write", "writing %s: %v", Path(), err)
	}
	defer os.Remove(tmp.Name()) // no-op once the rename below succeeds
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil {
		return view.Errorf("core.grant.write", "writing %s: %v", Path(), writeErr)
	}
	if closeErr != nil {
		return view.Errorf("core.grant.write", "writing %s: %v", Path(), closeErr)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return view.Errorf("core.grant.write", "writing %s: %v", Path(), err)
	}
	if err := os.Rename(tmp.Name(), Path()); err != nil {
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
// call it.
//
// Destructive is implicit: a capability that permanently removes something is
// exactly what a standing allowlist should not be enough for. Everything else
// opts in, which is how kv.get — a read by the letter of the safety model,
// a leak in practice — ends up here too.
func Required(c plugin.Capability) bool {
	return c.NeedsGrant || c.Safety == plugin.Destructive
}

// scopes reads the records a call names, from the input the capability
// declared as its scope. A capability with no scope, or a call that names no
// record, yields one empty scope: the call is about the capability itself.
func scopes(c plugin.Capability, values map[string]any) []string {
	if c.Scope == "" {
		return []string{""}
	}
	var out []string
	switch v := values[c.Scope].(type) {
	case string:
		if s := strings.TrimSpace(v); s != "" {
			out = append(out, s)
		}
	case []string:
		for _, s := range v {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	case []any:
		for _, raw := range v {
			if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	case nil:
	default:
		out = append(out, fmt.Sprint(v))
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// Check gates one call. It is the whole enforcement: the MCP bridge calls it
// before Run, and no plugin has to remember to.
//
// Every record a call names needs its own cover — `kv env a b c` with a grant
// for `a` is two thirds of a leak, not a partial success.
func Check(c plugin.Capability, values map[string]any) *view.Error {
	if !Required(c) {
		return nil
	}
	grants, verr := Load()
	if verr != nil {
		return verr
	}
	return checkAgainst(c, values, grants)
}

// checkAgainst is Check against a set of grants already in hand, so that the
// authorization decision can be made under the same lock that spends the use.
func checkAgainst(c plugin.Capability, values map[string]any, grants []Grant) *view.Error {
	var missing []string
	for _, scope := range scopes(c, values) {
		covered := false
		for _, g := range grants {
			if g.covers(c.ID, scope) {
				covered = true
				break
			}
		}
		if !covered {
			missing = append(missing, scope)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return view.Errorf("core.grant.required", "no active grant for %s", describe(c.ID, missing)).
		WithHint("a person has to allow this first: rta grant allow " +
			strings.TrimSpace(c.ID+" "+missing[0]) + " --ttl 15m")
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
// This is the whole gate for a use-limited grant, and it has to be one step.
// The sequence it replaces was Check -> Run -> Consume, with the lock held
// only by Consume: two concurrent tool calls both read Uses=0 against
// MaxUses=1, both cleared Check, and both ran. The go-sdk dispatches every
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
// nothing to spend, and the unlocked Check is a correct and complete answer
// for them.
func Reserve(c plugin.Capability, values map[string]any) (release func(), verr *view.Error) {
	if !Required(c) {
		return func() {}, nil
	}
	grants, verr := Load()
	if verr != nil {
		return nil, verr
	}
	if !anyLimited(grants) {
		// Nothing to spend, so the check needs no lock and no refund.
		return func() {}, Check(c, values)
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
	if verr := checkAgainst(c, values, grants); verr != nil {
		return nil, verr
	}
	spent, changed := spend(c, values, grants)
	if !changed {
		return func() {}, nil
	}
	if verr := Save(spent); verr != nil {
		return nil, verr
	}
	return func() { _ = refund(c, values) }, nil
}

// refund gives back a use spent for a call that then failed.
func refund(c plugin.Capability, values map[string]any) *view.Error {
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
	for _, scope := range scopes(c, values) {
		for i := range grants {
			if grants[i].MaxUses > 0 && grants[i].Uses > 0 && grants[i].covers(c.ID, scope) {
				grants[i].Uses--
				changed = true
				break
			}
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

// spend increments the covering grant for every record the call named.
func spend(c plugin.Capability, values map[string]any, grants []Grant) ([]Grant, bool) {
	changed := false
	for _, scope := range scopes(c, values) {
		for i := range grants {
			if grants[i].MaxUses > 0 && grants[i].covers(c.ID, scope) {
				grants[i].Uses++
				changed = true
				break // one grant per named record, the same granularity Check uses
			}
		}
	}
	return grants, changed
}

// Consume spends one use, per record the call named, from whichever grant
// authorized it.
//
// Deprecated in favour of Reserve, which decides and spends under one lock.
// Kept for callers outside the MCP bridge that have already run their own
// check and only need the bookkeeping.
func Consume(c plugin.Capability, values map[string]any) *view.Error {
	if !Required(c) {
		return nil
	}
	grants, verr := Load()
	if verr != nil {
		return verr
	}
	if !anyLimited(grants) {
		return nil
	}

	release, verr := acquireLock()
	if verr != nil {
		return verr
	}
	defer release()

	// Re-read under the lock: another call may have spent a use, or a grant
	// may have been revoked, since the unlocked check above.
	grants, verr = Load()
	if verr != nil {
		return verr
	}
	changed := false
	for _, scope := range scopes(c, values) {
		for i := range grants {
			if grants[i].MaxUses > 0 && grants[i].covers(c.ID, scope) {
				grants[i].Uses++
				changed = true
				break // one grant per named record, the same granularity Check uses
			}
		}
	}
	if !changed {
		return nil
	}
	return Save(grants)
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
// The lock is a sentinel file created with O_EXCL, not flock(2): O_EXCL's
// atomicity is identical on every platform rta ships for (Linux, macOS,
// Windows), where POSIX file locking is not.
func acquireLock() (release func(), verr *view.Error) {
	dir := paths.Data()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, view.Errorf("core.grant.lock", "creating %s: %v", dir, err)
	}
	path := filepath.Join(dir, lockFile)
	deadline := time.Now().Add(lockTimeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, view.Errorf("core.grant.lock", "acquiring the grant file lock: %v", err)
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > lockStale {
			_ = os.Remove(path) // a prior holder crashed without releasing it
			continue
		}
		if time.Now().After(deadline) {
			return nil, view.Errorf("core.grant.lock", "timed out waiting for another call to finish updating grants")
		}
		time.Sleep(lockRetry)
	}
}
