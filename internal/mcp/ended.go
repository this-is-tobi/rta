package mcp

import (
	"fmt"
	"sync"
	"time"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// endedGrants is what this server remembers of the grants it has let a call
// through on, so that a refusal for a grant that has since run out can say it
// ran out.
//
// "No active grant for note.add" reads the same after two approved calls on a
// two-use grant as it does for a grant nobody ever issued, and neither the
// agent nor the person reading the record could tell "the grant never took"
// from "the grant was used up". Naming it settles which.
//
// **Own history only, and that is the design rather than a shortcut.** The
// grants file knows more: it keeps a spent grant until it expires, so a
// refusal could name every grant that ever covered the call. It does not,
// for the reason grant.RefusedStale gives for connections — the refusal is
// one sentence whether nothing was granted or something once was, so that it
// cannot be used to ask the operator's file which records were granted to
// this agent's name, one probe at a time. What is said here is limited to
// grants this caller's own earlier calls were run on, which it watched
// happen: the sentence tells it nothing it was not there for. A grant spent
// in another server, revoked, or never used here stays "no active grant".
//
// Keyed by who the refusal is charged to (refusalKey), so that over HTTP one
// credential does not hear about a grant another one used up.
type endedGrants struct {
	mu   sync.Mutex
	used []usedGrant
}

type usedGrant struct {
	caller string
	grant  grant.Grant
	// uses is how many of the grant's uses this server has seen spent, counted
	// up from what the file held when the call was let through. A call that
	// spent more than one on the same grant (several records under one wide
	// grant) is counted once, so the figure can fall short of the truth and
	// never exceed it: the worst it does is leave a refusal worded as before.
	uses int
}

// maxUsedGrants bounds what is remembered, oldest forgotten first. A server
// with this many distinct grants in play is an agent working across a lot of
// records, and the cost of forgetting one is a refusal worded as before.
const maxUsedGrants = 64

// spentOn records that a call ran on these grants. Called once the handler
// has been reached, which is when a use is final: a call that failed before
// then was refunded (release) and spent nothing to remember.
func (e *endedGrants) spentOn(caller string, covering []grant.Grant) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, g := range covering {
		uses := 0
		if g.MaxUses > 0 {
			uses = g.Uses + 1
		}
		for i, u := range e.used {
			if u.caller == caller && sameGrant(u.grant, g) {
				uses = max(uses, u.uses)
				e.used = append(e.used[:i], e.used[i+1:]...)
				break
			}
		}
		e.used = append(e.used, usedGrant{caller: caller, grant: g, uses: uses})
	}
	if over := len(e.used) - maxUsedGrants; over > 0 {
		e.used = e.used[over:]
	}
}

// sameGrant is whether two snapshots are the one grant: what a grant is
// issued as, not what it has been through since (its uses, its deadline once
// renewed), so a renewal updates the entry instead of adding a second.
func sameGrant(a, b grant.Grant) bool {
	return a.Target == b.Target && a.Scope == b.Scope && a.Profile == b.Profile &&
		a.ProfilePin == b.ProfilePin && a.Agent == b.Agent && a.Digest == b.Digest &&
		a.Issued.Equal(b.Issued)
}

// knows is whether anything is remembered for caller, so that the common
// refusal, a caller that was never let through on any grant, costs no read of
// the grants file.
func (e *endedGrants) knows(caller string) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, u := range e.used {
		if u.caller == caller {
			return true
		}
	}
	return false
}

// ended is the sentence for a call on scope that a grant this caller used
// would have covered, or "" when that grant did not end the way this can
// tell.
//
// The latest grant used among those covering the call is the one asked: a
// grant used up and then issued again is the second one's story, and a
// sentence about the first would explain a refusal it did not cause.
func (e *endedGrants) ended(caller string, c plugin.Capability, scope string, by grant.Caller, now time.Time) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var latest *usedGrant
	for i := range e.used {
		u := &e.used[i]
		if u.caller == caller && grant.Covering([]grant.Grant{u.grant}, c.ID, scope, by) != nil {
			latest = u
		}
	}
	if latest == nil {
		return ""
	}
	g := latest.grant
	switch {
	case g.MaxUses > 0 && latest.uses >= g.MaxUses:
		return fmt.Sprintf("the grant for %s ended (%d of %d %s)",
			g.Named(), g.MaxUses, g.MaxUses, format.Plural(g.MaxUses, "use", "uses"))
	case !now.Before(g.Expires) || !now.Before(g.Issued.Add(grant.MaxTTL)):
		return fmt.Sprintf("the grant for %s ended (it expired)", g.Named())
	}
	return ""
}

// explain rewrites the gate's "no active grant" when the one record the call
// is missing a grant for is covered by a grant this caller has used up, and
// leaves it alone in every other case.
//
// Only the message changes. The code stays core.grant.required, so everything
// that reads it (consent asks on it, the record files it, the hint still
// names the command that issues a new grant) behaves as before; what changes
// is the sentence the agent is handed, the record's reason and the "why" the
// operator reads on a parked call.
//
// One missing record, and no active grant covering it, are both checked
// against the grants file as the gate reads it: a call missing several
// records, or covered by a grant the gate then set aside for a reason that
// is not this one (a connection that moved, an environment switched off),
// is not explained by a grant that ended, and a sentence that claimed so
// would send the operator to the wrong place.
func (e *endedGrants) explain(verr *view.Error, c plugin.Capability, values map[string]any,
	by grant.Caller, caller string) *view.Error {
	if verr == nil || verr.Code != "core.grant.required" || !e.knows(caller) {
		return verr
	}
	active, lerr := grant.Load()
	if lerr != nil {
		return verr
	}
	var missing []string
	for _, scope := range grant.Scopes(c, values) {
		if grant.Covering(active, c.ID, scope, by) == nil {
			missing = append(missing, scope)
		}
	}
	if len(missing) != 1 {
		return verr
	}
	said := e.ended(caller, c, missing[0], by, time.Now())
	if said == "" {
		return verr
	}
	out := *verr
	out.Message = said
	return &out
}
