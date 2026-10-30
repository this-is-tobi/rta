package agent

import (
	"slices"
	"time"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/grant"
)

// unanswered is the refusals for want of a grant that still ask the operator
// for one: each of refused, in the order given, that no later call of the same
// shape got past the grant gate and no grant standing now covers.
//
// The overview counted every such refusal of the hour as pending, so after the
// grant was issued and the agent's retry ran, the tile went on saying "2 need
// grant" in amber beside the grant list that held both: a queue that doing
// what it asked for did not clear. Answered either way — the retry got past
// the gate (it ran, failed, or was refused for something else), or a grant
// stands that the next try would meet. The second is asked without the
// plugin's digest, which a record does not keep: a grant for the capability,
// the agent, the profile and the records is the operator's answer, and a
// digest that no longer matches is refused again, as a refusal of its own.
func unanswered(refused, entries []agentlog.Entry, grants []grant.Grant, now time.Time) (pending []agentlog.Entry, answered int) {
	for _, r := range refused {
		if retriedPastTheGate(r, entries) || standingGrantCovers(r, grants, now) {
			answered++
			continue
		}
		pending = append(pending, r)
	}
	return pending, answered
}

// retriedPastTheGate reports whether a call of r's shape, made after it, was
// not refused for want of a grant.
func retriedPastTheGate(r agentlog.Entry, entries []agentlog.Entry) bool {
	for _, e := range entries {
		if e.Seq > r.Seq && sameCall(e, r) && kindOf(codeOf(e)) != refusedNeedsGrant {
			return true
		}
	}
	return false
}

// sameCall is two records of a call to the same capability, by the same
// agent, on the same profile and the same records.
func sameCall(a, b agentlog.Entry) bool {
	if a.Cap != b.Cap || a.Agent != b.Agent || a.Profile != b.Profile || len(a.Records) != len(b.Records) {
		return false
	}
	ra, rb := slices.Clone(a.Records), slices.Clone(b.Records)
	slices.Sort(ra)
	slices.Sort(rb)
	return slices.Equal(ra, rb)
}

// standingGrantCovers reports whether an active grant names r's capability
// (or its plugin), agent and profile, and covers every record r named — the
// call's own question, as the gate asks it, short of the digest.
func standingGrantCovers(r agentlog.Entry, grants []grant.Grant, now time.Time) bool {
	records := r.Records
	if len(records) == 0 {
		records = []string{""}
	}
	for _, record := range records {
		covered := slices.ContainsFunc(grants, func(g grant.Grant) bool {
			return g.Active(now) && (g.Target == r.Cap || g.Target == grant.Namespace(r.Cap)) &&
				g.Agent == r.Agent && g.Profile == r.Profile && grant.ScopeCovers(g.Scope, record)
		})
		if !covered {
			return false
		}
	}
	return true
}
