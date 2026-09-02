package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/internal/consent"
	operatorid "github.com/this-is-tobi/rule-them-all/internal/operator"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The client half of remote consent: `rta agent pending/show/allow/deny
// --server <name>`, each a signed operator call. The one deliberate
// asymmetry from the local flow, stated where it is implemented: locally a
// bare `agent allow <id>` is passphrase-free because it releases a single
// call an agent with a shell could have run directly — and that
// shell-equivalence argument does not travel. A network caller is
// precisely not the person at the machine, so every remote answer costs
// the operator key's passphrase, one-shot included.

// remoteQueue dials one server for its parked queue, unlocking the
// operator key first. The unlocked client comes back with it so an answer
// can ride the same unlock — one passphrase, however many envelopes.
func remoteQueue(req plugin.Request, server string) (operatorid.ConsentList, operatorid.Client, *view.Error) {
	base, verr := operatorid.ServerURL(server)
	if verr != nil {
		return operatorid.ConsentList{}, operatorid.Client{}, verr
	}
	pass, verr := operatorid.PromptSecret(req, false)
	if verr != nil {
		return operatorid.ConsentList{}, operatorid.Client{}, verr
	}
	signer, verr := operatorid.Unlock(pass)
	if verr != nil {
		return operatorid.ConsentList{}, operatorid.Client{}, verr
	}
	client := operatorid.Client{URL: base, Signer: signer}
	var cl operatorid.ConsentList
	if verr := client.Call(operatorid.VerbConsentList, nil, &cl); verr != nil {
		return operatorid.ConsentList{}, operatorid.Client{}, verr
	}
	return cl, client, nil
}

// remotePending is `agent pending --server <name>`: the server's queue,
// rendered through the same table the local listing uses — the operator is
// about to make the same decisions about the same rows.
func remotePending(req plugin.Request, server string) (view.View, error) {
	if req.DryRun {
		return view.Text{Body: "would read the parked queue from " + server + " as a signed " +
			"operator call — the passphrase is asked first"}, nil
	}
	cl, _, verr := remoteQueue(req, server)
	if verr != nil {
		return nil, verr
	}
	table := pendingTable(cl.Waiting)
	if len(cl.Tampered) == 0 {
		return table, nil
	}
	return view.Sections{Items: []view.Section{
		{ID: "waiting", Title: "Waiting on " + server, View: table},
		{ID: "tampered", Title: "Kept off the queue", View: view.Text{
			Body: fmt.Sprintf("%d request(s) on %s do not describe the calls they are bound to: %s — "+
				"something on that machine rewrote them after rta parked them, and its `rta doctor` reports it.",
				len(cl.Tampered), server, strings.Join(cl.Tampered, ", "))}},
	}}, nil
}

// remoteShow is `agent show <id> --server <name>`: the request in full, so
// approving an outcome rather than an intention survives the distance.
func remoteShow(req plugin.Request, server, id string) (view.View, error) {
	if req.DryRun {
		return view.Text{Body: "would read request " + id + " from " + server + " as a signed " +
			"operator call — the passphrase is asked first"}, nil
	}
	cl, _, verr := remoteQueue(req, server)
	if verr != nil {
		return nil, verr
	}
	r, verr := findRemote(cl, server, id)
	if verr != nil {
		return nil, verr
	}
	return showView(r), nil
}

// remoteAnswer is `agent allow <id> --server` and `agent deny <id>
// --server`: fetch the request, derive the digest from what this machine
// read, and send the answer inside the signed envelope.
func remoteAnswer(req plugin.Request, server, id string, allow bool) (view.View, error) {
	verb := "deny"
	if allow {
		verb = "allow"
	}
	// --ttl mints a standing grant, which remotely is its own signed
	// prepare-and-issue flow with its own review step — folding it into an
	// answer would skip exactly the draft check that keeps a hostile server
	// from widening what gets signed.
	if allow && strings.TrimSpace(req.String("ttl")) != "" {
		return nil, view.Errorf("agent.remote.ttl",
			"--ttl does not combine with --server: a standing grant is its own signed flow").
			WithHint("answer this call first, then `rta grant allow " +
				"<target> --ttl ... --server " + server + "`")
	}
	if req.DryRun {
		return view.Text{Body: "would fetch request " + id + " from " + server + " and " + verb +
			" it as a signed operator call — the passphrase is asked first"}, nil
	}
	cl, client, verr := remoteQueue(req, server)
	if verr != nil {
		return nil, verr
	}
	r, verr := findRemote(cl, server, id)
	if verr != nil {
		return nil, verr
	}
	// Derived from the fields this machine received and will act on, never
	// copied from the server's own digest column. The server's Scan already
	// keeps self-disagreeing files off the queue, so a mismatch here means
	// the *server* sent a display that does not describe the call it binds
	// — the one party the local honesty check cannot vouch for.
	digest := r.Call().Digest()
	if digest != r.Digest {
		return nil, view.Errorf("agent.answer.dishonest",
			"the entry %s returned for %q does not describe the call it is bound to — refusing to answer it",
			server, id).
			WithHint("the server's queue said one thing and bound another; treat that machine as suspect")
	}
	var out operatorid.AnswerOutcome
	spec := operatorid.AnswerSpec{ID: id, Digest: digest, Allow: allow}
	if verr := client.Call(operatorid.VerbConsentAnswer, spec, &out); verr != nil {
		return nil, verr
	}
	what := strings.TrimSpace(out.Cap + " " + strings.Join(out.Scopes, " "))
	if allow {
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "allowed", Value: what},
			{Key: "for", Value: "this call only, on " + server},
		}}, nil
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "denied", Value: what},
		{Key: "the agent", Value: "gets your answer rather than a timeout"},
	}}, nil
}

// findRemote is unknownRequest's remote cousin: the same three-way answer
// — waiting, tampered, or gone — read from the fetched queue rather than
// from this machine's disk.
func findRemote(cl operatorid.ConsentList, server, id string) (consent.Request, *view.Error) {
	for _, r := range cl.Waiting {
		if r.ID == id {
			return r, nil
		}
	}
	for _, bad := range cl.Tampered {
		if bad != id {
			continue
		}
		return consent.Request{}, view.Errorf("agent.request.tampered",
			"request %q on %s does not describe the call it is bound to, so it cannot be answered", id, server).
			WithHint("something rewrote it after rta parked it — whatever can write that server's " +
				"data directory is the thing to look at")
	}
	e := view.Errorf("agent.request.unknown", "no request %q is waiting on %s", id, server)
	if len(cl.Waiting) == 0 {
		return consent.Request{}, e.WithHint("nothing is waiting there — a parked call expires on its own, and the agent is told")
	}
	ids := make([]string, 0, len(cl.Waiting))
	for _, r := range cl.Waiting {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	return consent.Request{}, e.WithHint("waiting there right now: " + strings.Join(ids, ", "))
}
