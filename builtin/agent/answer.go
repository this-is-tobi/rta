package agent

import (
	"github.com/this-is-tobi/rule-them-all/internal/consent"
	"github.com/this-is-tobi/rule-them-all/internal/grant"
	operatorid "github.com/this-is-tobi/rule-them-all/internal/operator"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The server half of remote consent: the two functions the app layer wires
// into the operator channel (the kv.Reveal pattern — "this server answers
// consent for its operators" is a line somebody typed, never a transitive
// import). They run inside the same process that parked the call, so the
// sealed decision file is written by the one writer the seal key belongs
// to; what crossed the network is a signed answer naming a digest, never
// the authority itself.
//
// Both take the serving process's own --as name and scope to it, because
// the queue on disk is machine-global — several servers, one operator's
// attention — while a roster is one server's trust decision. The agent
// name is already the principal grants scope by (`--as` on the server,
// `--agent` on the grant, "both halves name the same thing on purpose"),
// so the channel answers exactly the questions asked under this server's
// own name: enrolling an operator here must not quietly make them an
// answerer for every co-resident server's queue, judged under this
// process's policy context instead of the asking server's.

// PendingRemote is the consent.list verb: the queue as the local surfaces
// walk it, narrowed to this server's own questions. Tampered ids travel
// whole — a rewritten request is a machine-level alarm, not one server's
// business, and the operator reading the listing is who the alarm is for.
func PendingRemote(agent string) func() (operatorid.ConsentList, *view.Error) {
	return func() (operatorid.ConsentList, *view.Error) {
		q, err := consent.Scan()
		if err != nil {
			return operatorid.ConsentList{}, view.Errorf("agent.pending.unreadable", "%v", err)
		}
		var mine []consent.Request
		for _, r := range q.Waiting {
			if r.Agent == agent {
				mine = append(mine, r)
			}
		}
		return operatorid.ConsentList{Waiting: mine, Tampered: q.Tampered}, nil
	}
}

// AnswerRemote is the consent.answer verb. label is the enrolled operator
// the envelope verified; it lands in the decision's By as
// "operator:<label>", the same attribution shape remote issuance writes
// into a grant's Origin.
func AnswerRemote(agent string) func(spec operatorid.AnswerSpec, label string) (operatorid.AnswerOutcome, *view.Error) {
	return func(spec operatorid.AnswerSpec, label string) (operatorid.AnswerOutcome, *view.Error) {
		r, ok := consent.Find(spec.ID)
		if !ok {
			return operatorid.AnswerOutcome{}, unknownRequest(spec.ID)
		}
		// The scope rule, enforced on the answer and not only on the listing
		// a well-behaved client fetched first.
		if r.Agent != agent {
			return operatorid.AnswerOutcome{}, view.Errorf("agent.answer.elsewhere",
				"request %q was parked under the agent name %q, and this server answers only for %q",
				spec.ID, r.Agent, agent).
				WithHint("reach it through the server that parked it")
		}
		// The team's ceiling binds a remote "yes" exactly as it binds the local
		// one — runAllow's reasoning verbatim, and it has to run on this
		// machine: the ceiling is this server's policy, and the operator's own
		// machine checking its local policy file would be checking the wrong
		// team's rules.
		if spec.Allow {
			scope := ""
			if len(r.Scopes) == 1 {
				scope = r.Scopes[0]
			}
			if verr := grant.CheckCeiling(r.Cap, scope, r.Profile); verr != nil {
				return operatorid.AnswerOutcome{}, verr
			}
		}
		// DecideBound, not Decide: the digest the operator's machine derived
		// from what it displayed is compared against the file under the same
		// load the decision is minted from. A queue entry swapped between their
		// read and this write — even for another honest request — refuses
		// instead of approving.
		if err := consent.DecideBound(spec.ID, spec.Digest, spec.Allow, grant.FromOperatorPrefix+label); err != nil {
			return operatorid.AnswerOutcome{}, view.Errorf("agent.answer.failed", "%v", err)
		}
		return operatorid.AnswerOutcome{Cap: r.Cap, Scopes: r.Scopes}, nil
	}
}
