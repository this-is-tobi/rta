package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/agentlog"
	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/guard"
	"github.com/this-is-tobi/rule-them-all/internal/lockdown"
	"github.com/this-is-tobi/rule-them-all/internal/operator"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The operator channel's HTTP face: two POST routes beside the MCP handler,
// mounted only when the server was started with a roster. It is deliberately
// not MCP — builtin/agent's package doc closed that door with "the right
// answer is not 'with permission' but 'not here'": admin verbs on the
// agent's own channel would make the boundary a string match on a role
// claim, so the operator path is a different gate entirely. MCP callers
// never see these routes in tools/list, and these routes never run
// capabilities.
//
// It also deliberately sits outside the bearer wall. Its authentication is
// the envelope signature — stronger than possession of any bearer, and the
// only mechanism here, so an operator needs no agent token and an agent's
// token opens nothing: /challenge hands out proofs of nothing, and /call
// verifies an ed25519 signature over a nonce this process minted. What is
// shared with the bearer wall is the refusal discipline: an unauthenticated
// caller learns nothing from a refusal beyond its status code, and the real
// reason goes to stderr.
//
// Every verb — reads, grant mutations, consent answers — rides the same
// envelope. The mutations also land in the agent ledger, attributed
// operator:<label> — see mutationEntry for which and why — so the sealed
// file that shows an approved call also shows who approved it. Reads leave
// only the stderr line naming label, fingerprint and verb.

// OperatorConfig assembles an operator handler.
type OperatorConfig struct {
	Roster operator.Roster
	// URL is this server's canonical identity — the exact string operators
	// put in their remotes.yaml, from --operators-url. Every envelope's
	// signature covers it (operator.Verify reconstructs the message from
	// *this* value, never from anything a caller sent), which is what stops
	// a hostile server the operator also talks to from relaying their
	// signed calls here: an envelope aimed elsewhere names elsewhere.
	URL string
	// Version and Agent answer the status verb: the server's build and the
	// --as name its grants match against.
	Version string
	Agent   string
	// Stderr is where refusal reasons and the per-call operator log line go;
	// nil discards both.
	Stderr io.Writer
	// NonceTTL overrides the challenge lifetime; zero means operator.NonceTTL.
	// It exists for tests, like RemoteOptions.ShutdownGrace.
	NonceTTL time.Duration
	// Prepare and Revoke implement the grant-mutation verbs, wired from the
	// app layer out of builtin/grant — the kv.Reveal pattern: "this server
	// mutates grants for operators" is a line somebody typed, never a
	// transitive import. nil leaves the verb unoffered, refused post-auth
	// with a message naming what the server was started without.
	Prepare func(spec operator.IssueSpec, label string) (operator.Prepared, *view.Error)
	Revoke  func(spec operator.RevokeSpec, write bool) (operator.RevokeOutcome, *view.Error)
	// Pending and Answer implement the consent verbs, wired the same way
	// from builtin/agent: listing the queue and answering one parked call,
	// each attributed to the enrolled operator the envelope verified.
	Pending func() (operator.ConsentList, *view.Error)
	Answer  func(spec operator.AnswerSpec, label string) (operator.AnswerOutcome, *view.Error)
	// Consent says whether this process itself parks calls (--consent).
	// The consent verbs refuse without it, and the gate is load-bearing
	// rather than tidy: the queue on disk is machine-global — several
	// servers, one operator's attention — while a roster is one server's
	// trust decision. Without this, enrolling an operator on any server of
	// this uid would quietly make them an answerer for every co-resident
	// server's questions, under this process's policy context instead of
	// the asking server's. The stage-3 security review's one held finding.
	Consent bool
}

// maxEnvelopeBytes bounds a /call body. An envelope is a signature around a
// small JSON payload — the largest legitimate one is far under this — and
// the route is reachable unauthenticated, so the bound is what keeps a
// hostile body from being a memory bill.
const maxEnvelopeBytes = 1 << 20

// NewOperatorHandler builds the channel's handler, to be mounted by Serve
// under /operator/v1/. Patterns carry the full path because the mount does
// not strip the prefix.
func NewOperatorHandler(cfg OperatorConfig) http.Handler {
	h := &operatorHandler{cfg: cfg, nonces: operator.NewNonces(cfg.NonceTTL), locks: lockdown.NewPin()}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /operator/v1/challenge", h.challenge)
	mux.HandleFunc("POST /operator/v1/call", h.call)
	return mux
}

type operatorHandler struct {
	cfg    OperatorConfig
	nonces *operator.Nonces
	locks  *lockdown.Pin
}

func (h *operatorHandler) logf(format string, args ...any) {
	if h.cfg.Stderr != nil {
		fmt.Fprintf(h.cfg.Stderr, "rta: operator: "+format+"\n", args...)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type errorBody struct {
	Error *view.Error `json:"error"`
}

// refuse answers an unauthenticated caller: one generic body whatever
// actually failed, the real reason on stderr — the discipline Compose
// documents, for the same reason.
func (h *operatorHandler) refuse(w http.ResponseWriter, why string) {
	h.logf("request refused: %s", why)
	writeJSON(w, http.StatusUnauthorized, errorBody{
		Error: view.Errorf("core.operator.refused", "operator request refused"),
	})
}

func (h *operatorHandler) challenge(w http.ResponseWriter, _ *http.Request) {
	nonce, err := h.nonces.Issue()
	if err != nil {
		h.logf("issuing a challenge: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{
			Error: view.Errorf("core.operator.challenge", "this server could not mint a challenge"),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"nonce": nonce})
}

func (h *operatorHandler) call(w http.ResponseWriter, r *http.Request) {
	var env operator.Envelope
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEnvelopeBytes))
	if err != nil {
		h.refuse(w, fmt.Sprintf("reading the body: %v", err))
		return
	}
	if err := json.Unmarshal(body, &env); err != nil {
		h.refuse(w, fmt.Sprintf("the body does not parse as an envelope: %v", err))
		return
	}
	// The nonce burns before the signature is checked: a captured challenge
	// must not be a thing an attacker can brute-force signatures against at
	// leisure, and a legitimate caller's failed attempt costs one round trip
	// for a fresh one.
	if !h.nonces.Consume(env.Nonce) {
		h.refuse(w, fmt.Sprintf("nonce not honoured (spent, expired, or never issued) for fingerprint %q", env.Fingerprint))
		return
	}
	label, role, ok := h.cfg.Roster.Verify(env, h.cfg.URL)
	if !ok {
		h.refuse(w, fmt.Sprintf("signature rejected for fingerprint %q, verb %q — a valid-looking envelope "+
			"may have been signed for a different server than %q", env.Fingerprint, env.Verb, h.cfg.URL))
		return
	}
	h.logf("%s (%s) called %s", label, env.Fingerprint, env.Verb)
	// The row is written before the response leaves, never in a defer
	// after it: a mutation the caller has seen acknowledged must already
	// be in the record, or a crash between the two acknowledges an action
	// history never shows. The bridge has the same ordering — its record
	// runs before the handler returns the result to the SDK.
	rec := h.mutationEntry(env, label)
	status, answer := h.answer(env, label, role, rec)
	if rec != nil {
		record(*rec)
	}
	writeJSON(w, status, answer)
}

// answer runs one verified call, classifying the outcome into the wire
// answer and — when rec is non-nil — the ledger row.
func (h *operatorHandler) answer(env operator.Envelope, label string,
	role operator.Role, rec *agentlog.Entry) (int, any) {
	// The frozen check sits past the signature and before everything else,
	// the role gate included: a locked operator key gets no verb at all,
	// which is what makes locking the answer to a compromised key that
	// beats editing the roster and restarting. Post-auth, so the named
	// operator is owed the real reason — including the note whoever locked
	// them wrote for exactly this moment.
	if l, alarm := h.locks.Frozen(lockdown.KindOperator, label); l != nil || alarm != "" {
		if alarm != "" {
			h.logf("%s", alarm)
		}
		if l != nil {
			msg := "this operator key is locked on this server"
			if l.Note != "" {
				msg += ": " + l.Note
			}
			verr := view.Errorf("core.lock.operator", "%s", msg).
				WithHint("another full operator lifts it with `rta lock rm operator " + label +
					" --server <this server>`, or a person at the machine runs it directly")
			refusedBy(rec, verr)
			return http.StatusForbidden, errorBody{Error: verr}
		}
	}
	start := time.Now()
	res, verr := h.dispatch(env, label, role)
	if verr != nil {
		if rec != nil {
			// statusFor already sorts the channel's errors into "this server
			// working as configured" and "unexpected failure", and the ledger's
			// Refused/Failed split is the same line: a 4xx is a gate saying no
			// (the zero outcome, plus the reason), a 500 is a verb that was
			// allowed and whose work went wrong.
			if statusFor(verr.Code) == http.StatusInternalServerError {
				failedBy(rec, verr)
				rec.Auth = agentlog.Operator
				rec.Millis = time.Since(start).Milliseconds()
			} else {
				refusedBy(rec, verr)
			}
		}
		// Past the signature the caller is a named operator, and owed the
		// real error — this is their server misbehaving, not a stranger
		// probing it.
		return statusFor(verr.Code), errorBody{Error: verr}
	}
	if rec != nil {
		rec.Outcome, rec.Auth = agentlog.Ran, agentlog.Operator
		rec.Millis = time.Since(start).Milliseconds()
	}
	return http.StatusOK, res
}

// mutationEntry opens the ledger row for a verb that changes something,
// nil for everything else — which is the recording rule in one line. The
// channel's mutations are what the rest of the record is read against: a
// revoked grant explains the refusals after it, an issued one the calls it
// covers, an answered consent is the other half of the agent row marked
// auth=approved, and a lock explains a principal going quiet — so who did
// each belongs in the same sealed file, not only in stderr scrollback.
// Reads stay off the record because a watching dashboard polls them, and
// at one status call every few seconds a recorded poll is real history
// churned out of the ledger's retention.
//
// Opened only past Verify, so every row names an enrolled key. The route
// is reachable unauthenticated, and a stranger who could write rows by
// POSTing garbage would hold a disk-filling primitive against the one
// file that must outlive an incident. An enrolled key that is locked or
// role=read still writes its refused attempts — that is the incident
// evidence the row exists to keep, and the accepted edge of it: a key
// worth distrusting can still add rows until the roster edit that
// un-enrolls it, the same standing every bearer holder has against the
// bridge's own recording.
//
// Cap carries the verb under an "operator." prefix because the bare verb
// names collide with builtin capability IDs — grant.revoke is also a
// capability an agent can probe over MCP, and its refused row must not be
// grep-identical to an operator actually revoking.
//
// The zero outcome is the bridge's: Refused/Blocked, "refused before
// anything could authorize it", flipped only by the paths that allow.
func (h *operatorHandler) mutationEntry(env operator.Envelope, label string) *agentlog.Entry {
	args, mutates := mutationArgs(env)
	if !mutates {
		return nil
	}
	return &agentlog.Entry{
		Cap:        "operator." + env.Verb,
		Agent:      h.cfg.Agent,
		Credential: grant.FromOperatorPrefix + label,
		Outcome:    agentlog.Refused,
		Auth:       agentlog.Blocked,
		Args:       args,
	}
}

// mutationArgs decodes what a mutation verb asked for into the ledger's
// args column, and answers whether the verb mutates at all.
//
// Decoded beside dispatch rather than threaded through it — a second
// unmarshal of a small payload buys every case staying untouched. Only
// named fields are copied, never the raw payload: the payload is
// caller-controlled JSON and a ledger line is read back by people in
// terminals and by agents grepping it, so what reaches it gets the same
// selected, cleaned treatment auditArgs gives a tool call's arguments. A
// mutation payload that does not decode still records the attempt, with
// no arguments — dispatch is about to refuse it and the row carries that
// reason.
//
// A dry-run revoke is the one mutation verb that changes nothing, and it
// is skipped for the reads' reason: it is a preview.
func mutationArgs(env operator.Envelope) (map[string]any, bool) {
	args := map[string]any{}
	switch env.Verb {
	case operator.VerbGrantRevoke:
		var spec operator.RevokeSpec
		if err := json.Unmarshal(env.Payload, &spec); err != nil {
			return nil, true
		}
		if spec.DryRun {
			return nil, false
		}
		if spec.All {
			args["all"] = true
		}
		putArg(args, "target", spec.Target)
		putArg(args, "scope", spec.Scope)
		putArg(args, "profile", spec.Profile)
		putArg(args, "agent", spec.Agent)
	case operator.VerbGrantIssue:
		var g grant.Grant
		if err := json.Unmarshal(env.Payload, &g); err != nil {
			return nil, true
		}
		putArg(args, "target", g.Target)
		putArg(args, "scope", g.Scope)
		putArg(args, "profile", g.Profile)
		putArg(args, "agent", g.Agent)
	case operator.VerbConsentAnswer:
		var spec operator.AnswerSpec
		if err := json.Unmarshal(env.Payload, &spec); err != nil {
			return nil, true
		}
		putArg(args, "id", spec.ID)
		args["allow"] = spec.Allow
	case operator.VerbLockAdd:
		var spec operator.LockSpec
		if err := json.Unmarshal(env.Payload, &spec); err != nil {
			return nil, true
		}
		putArg(args, "kind", spec.Kind)
		putArg(args, "name", spec.Name)
		putArg(args, "note", spec.Note)
		putArg(args, "ttl", spec.TTL)
	case operator.VerbLockRm:
		var spec operator.LockRmSpec
		if err := json.Unmarshal(env.Payload, &spec); err != nil {
			return nil, true
		}
		putArg(args, "kind", spec.Kind)
		putArg(args, "name", spec.Name)
	default:
		return nil, false
	}
	if len(args) == 0 {
		args = nil
	}
	return args, true
}

// OperatorLedgerCaps is every capability ID the operator channel can
// write into the agent ledger — "operator." plus each mutation verb —
// derived from mutationArgs rather than restated, so it cannot drift
// from what is actually recorded. internal/app's reservation test walks
// it against the real registry: these IDs live inside the operator
// builtin's namespace, and a capability registered under one of them
// would make an agent's probe grep-identical to an enrolled operator's
// action in the sealed record.
func OperatorLedgerCaps() []string {
	var out []string
	for _, v := range operator.Verbs() {
		if _, mutates := mutationArgs(operator.Envelope{Verb: v, Payload: []byte("{}")}); mutates {
			out = append(out, "operator."+v)
		}
	}
	return out
}

// putArg records one string field, skipping empties so rows stay as tight
// as the specs that made them, and cleaned because every value here
// crossed the network inside a payload rta did not write.
func putArg(args map[string]any, key, val string) {
	if val == "" {
		return
	}
	args[key] = cleanValue(val)
}

// statusFor sorts a dispatch refusal into its HTTP class — and, because
// answer derives the ledger's Refused/Failed split from it, into the
// record's vocabulary too: a 4xx row is Refused, a 500 is Failed. One
// classifier for both readers is deliberate; drifting apart is how a
// probe pages the on-call while its ledger row claims the work merely
// errored.
//
// The named codes are this server working as configured — a role outside
// its allowlist, a verb it does not offer, a submission failing a stated
// expectation — and a read-only monitoring key hitting them must not page
// whoever watches this server's error rate as if the server were failing.
// The vocabulary reaches into the wired packages because the verbs do:
// grant grammar, the guard's signature gate, the consent answer's tamper
// defenses all refuse through here. Anything unnamed stays 500, because
// an unexpected failure is exactly what error rates exist to surface —
// which also means a new refusal code lands there until it is named
// here: the direction that alarms rather than hides. The client is
// deliberately status-agnostic (it reads the view.Error in the body), so
// the HTTP class is for the middleboxes and dashboards that never parse
// rta's own vocabulary.
func statusFor(code string) int {
	switch code {
	// Authorization and policy saying no — the guard refusing a submitted
	// grant's signature included, and the two consent tamper defenses (a
	// rewritten queue entry, a digest that no longer matches what the
	// operator's screen showed), which refuse the answer precisely
	// because the thing being answered is not what was read.
	case "core.operator.role", "core.operator.consent", "core.operator.verb", "core.operator.guard",
		"grant.policy.refused", "core.grant.guard.unsigned", "core.grant.guard.forged",
		"agent.request.tampered", "agent.answer.failed":
		return http.StatusForbidden
	// The request itself is the problem: malformed, mis-addressed, or
	// naming something that is not there to act on.
	case "core.operator.payload", "core.lock.kind", "core.lock.ttl", "core.lock.name", "core.lock.note",
		"grant.notarget", "agent.request.unknown", "agent.answer.elsewhere":
		return http.StatusBadRequest
	}
	// Families matched by prefix because they grow at their source: a new
	// checkSubmitted expectation, or a new grammar rule in the grant
	// package, is classified the day it is written instead of paging as a
	// server error until somebody notices.
	if strings.HasPrefix(code, "core.operator.issue.") ||
		strings.HasPrefix(code, "grant.agent.") ||
		strings.HasPrefix(code, "grant.scope.") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func (h *operatorHandler) dispatch(env operator.Envelope, label string, role operator.Role) (any, *view.Error) {
	// The role gate comes before everything, the unknown-verb answer
	// included: what a read-only key may do is a closed list, and the
	// refusal names that list rather than reasoning about a verb this
	// build may never have heard of. Past the signature this is a named
	// operator owed the real reason — the roster's own restriction on
	// their key is nothing the uniform refusal body exists to withhold.
	if !role.Allows(env.Verb) {
		return nil, view.Errorf("core.operator.role",
			"this key is enrolled role=read on this server — status, grant.list, consent.list and "+
				"lock.list are what that covers, and %q is not among them", env.Verb).
			WithHint("a roster line without the annotation enrolls a full operator; edit the server's " +
				"--operators file and restart it")
	}
	switch env.Verb {
	case operator.VerbStatus:
		return operator.Status{
			Version:          h.cfg.Version,
			Agent:            h.cfg.Agent,
			GuardOn:          guard.Enabled(),
			GuardFingerprint: guard.Fingerprint(),
			Operators:        h.cfg.Roster.Operators(),
		}, nil
	case operator.VerbGrantList:
		grants, verr := grant.Load()
		if verr != nil {
			return nil, verr
		}
		return operator.GrantList{Grants: grants, Suppressed: grant.Suppressed()}, nil
	case operator.VerbGrantRevoke:
		if h.cfg.Revoke == nil {
			return nil, verbUnoffered(env.Verb)
		}
		var spec operator.RevokeSpec
		if err := json.Unmarshal(env.Payload, &spec); err != nil {
			return nil, badPayload(env.Verb, err)
		}
		return h.cfg.Revoke(spec, !spec.DryRun)
	case operator.VerbGrantPrepare:
		if h.cfg.Prepare == nil {
			return nil, verbUnoffered(env.Verb)
		}
		// Preparation is refused outright unless this machine's guard is in
		// remote mode: an issued grant must carry a signature loadAll will
		// honour, and only a remote guard honours an operator's. Said at
		// prepare time — before the operator signs anything — because "your
		// signed grant was refused" is the wrong first message for a server
		// that was simply never provisioned for issuance.
		if !guard.Remote() {
			return nil, view.Errorf("core.operator.guard",
				"this server's guard does not trust remote operators, so nothing you sign would be honoured").
				WithHint("on the server: `rta grant guard remote <roster>` enrolls the operator keys; " +
					"revocation and listing work regardless")
		}
		var spec operator.IssueSpec
		if err := json.Unmarshal(env.Payload, &spec); err != nil {
			return nil, badPayload(env.Verb, err)
		}
		return h.cfg.Prepare(spec, label)
	case operator.VerbGrantIssue:
		// The same precheck as prepare, for the same message-quality reason:
		// a caller who skipped prepare on a never-provisioned server should
		// hear about the provisioning step, not about signatures.
		if !guard.Remote() {
			return nil, view.Errorf("core.operator.guard",
				"this server's guard does not trust remote operators, so nothing you sign would be honoured").
				WithHint("on the server: `rta grant guard remote <roster> --url <canonical>` enrolls the " +
					"operator keys; revocation and listing work regardless")
		}
		var g grant.Grant
		if err := json.Unmarshal(env.Payload, &g); err != nil {
			return nil, badPayload(env.Verb, err)
		}
		if verr := h.checkSubmitted(g, label); verr != nil {
			return nil, verr
		}
		// grant.Issue re-runs the guard's own gate: the signature must verify
		// under an enrolled key, inside the store's lock, whoever the network
		// caller was — the checks above exist for better error messages and
		// tighter invariants, not as the enforcement.
		if verr := grant.Issue(g, true); verr != nil {
			return nil, verr
		}
		return g, nil
	case operator.VerbConsentList:
		if verr := h.consentOffered(); verr != nil {
			return nil, verr
		}
		if h.cfg.Pending == nil {
			return nil, verbUnoffered(env.Verb)
		}
		return h.cfg.Pending()
	case operator.VerbConsentAnswer:
		if verr := h.consentOffered(); verr != nil {
			return nil, verr
		}
		if h.cfg.Answer == nil {
			return nil, verbUnoffered(env.Verb)
		}
		var spec operator.AnswerSpec
		if err := json.Unmarshal(env.Payload, &spec); err != nil {
			return nil, badPayload(env.Verb, err)
		}
		return h.cfg.Answer(spec, label)
	case operator.VerbLockList:
		locks, verr := lockdown.Load()
		if verr != nil {
			return nil, verr
		}
		return operator.LockList{Locks: locks}, nil
	case operator.VerbLockAdd:
		// Dispatched directly rather than wired from the app layer like the
		// grant and consent verbs, for the same reason grant.list is: locks
		// are core state with no capability behind them, so there is no
		// builtin whose absence could leave the verb half-configured.
		var spec operator.LockSpec
		if err := json.Unmarshal(env.Payload, &spec); err != nil {
			return nil, badPayload(env.Verb, err)
		}
		l, verr := lockdown.Build(spec.Kind, spec.Name, spec.Note, spec.TTL, grant.FromOperatorPrefix+label)
		if verr != nil {
			return nil, verr
		}
		if verr := lockdown.Add(l); verr != nil {
			return nil, verr
		}
		return l, nil
	case operator.VerbLockRm:
		var spec operator.LockRmSpec
		if err := json.Unmarshal(env.Payload, &spec); err != nil {
			return nil, badPayload(env.Verb, err)
		}
		kind, verr := lockdown.CheckKind(spec.Kind)
		if verr != nil {
			return nil, verr
		}
		removed, verr := lockdown.Remove(kind, spec.Name)
		if verr != nil {
			return nil, verr
		}
		return operator.LockRmOutcome{Removed: removed}, nil
	default:
		return nil, view.Errorf("core.operator.verb",
			"this server does not answer %q — its rta may be older than your client", env.Verb).
			WithHint("`rta operator status --server <name>` reports the server's version")
	}
}

// checkSubmitted holds a submitted grant to the shape prepare produced:
// attributed to the operator actually submitting it, timestamped by a clock
// this server agrees with, and within every ceiling. None of it replaces
// Issue's own signature check — it turns "refused" into a sentence naming
// which expectation broke.
func (h *operatorHandler) checkSubmitted(g grant.Grant, label string) *view.Error {
	if g.Sig == "" {
		return view.Errorf("core.operator.issue.unsigned",
			"the submitted grant carries no signature — sign what prepare returned, unchanged")
	}
	// The consumption bookkeeping is deliberately outside the signed
	// authority — the server rewrites it per counted call — which makes it
	// the one part of the struct a network caller could pre-load: a grant
	// signed with maxUses 1 arriving with Uses -1000000 would read as
	// tightly budgeted to every reviewer and spend a million times. Refused,
	// never zeroed: "sign what prepare returned, unchanged" is the contract.
	if g.Uses != 0 || len(g.Recent) != 0 || g.MaxUses < 0 {
		return view.Errorf("core.operator.issue.bookkeeping",
			"the submitted grant pre-loads its own consumption bookkeeping — sign what prepare returned, unchanged")
	}
	// The grammar checks prepare ran, re-run on submission: what they refuse
	// would be stored dead (fail closed) except the agent name, whose only
	// power is to *render* — a homoglyph agent column is exactly the
	// lookalike-row deception CheckAgent exists to kill, and one enrolled
	// operator must not be able to plant it against another's review.
	if verr := grant.CheckAgent(g.Agent); verr != nil {
		return verr
	}
	if verr := grant.CheckScope(g.Scope); verr != nil {
		return verr
	}
	if bound := guard.BoundServer(); g.Server != bound {
		return view.Errorf("core.operator.issue.server",
			"the submitted grant is bound to %q, and this server answers for %q", g.Server, bound)
	}
	if g.From != grant.FromOperatorPrefix+label {
		// The attribution is part of the signed authority, so an operator
		// cannot submit a grant prepared for — and signed by — someone else
		// and have the audit trail name the wrong person.
		return view.Errorf("core.operator.issue.from",
			"the submitted grant is attributed to %q, and this call was made by %q", g.From,
			grant.FromOperatorPrefix+label)
	}
	now := time.Now()
	if skew := now.Sub(g.Issued); skew < -2*time.Minute || skew > 2*time.Minute {
		return view.Errorf("core.operator.issue.skew",
			"the grant's issue time is %s from this server's clock — prepare, sign and submit "+
				"in one flow, and check both machines' clocks", skew.Round(time.Second)).
			WithHint("Retryable: run the command again")
	}
	if !g.Expires.After(now) {
		return view.Errorf("core.operator.issue.expired", "the submitted grant is already expired")
	}
	// The ceiling cannot rewrite a signed authority the way parseTTL clamps
	// a requested one, so anything over it is refused whole — prepare
	// already clamps, which makes this reachable only by bypassing prepare.
	// Both bounds apply, tightest first: rta's own absolute maximum, and the
	// team policy's if one stands (ClampTTL knows only the latter).
	limit := grant.MaxTTL
	if tighter, changed, _ := grant.ClampTTL(limit); changed {
		limit = tighter
	}
	if window := g.Expires.Sub(g.Issued); window > limit {
		return view.Errorf("core.operator.issue.ttl",
			"the submitted grant outlives this server's ceiling (%s allowed)", limit)
	}
	return grant.CheckCeiling(g.Target, g.Scope, g.Profile)
}

// consentOffered is the guard.Remote() precheck's reasoning one verb
// family over: a server that parks nothing has nothing its operators
// should answer, so both consent verbs refuse before touching the
// machine-global queue — which may well hold *other* servers' questions,
// and those belong to the servers that asked them (see
// OperatorConfig.Consent for the full argument).
func (h *operatorHandler) consentOffered() *view.Error {
	if h.cfg.Consent {
		return nil
	}
	return view.Errorf("core.operator.consent",
		"this server was started without --consent, so nothing parks here and there is nothing to answer").
		WithHint("`rta mcp serve --http --operators <roster> --consent` is the shape that parks and answers")
}

func verbUnoffered(verb string) *view.Error {
	return view.Errorf("core.operator.verb",
		"this server was started without %q — the serve command did not wire it", verb)
}

func badPayload(verb string, err error) *view.Error {
	return view.Errorf("core.operator.payload", "decoding the %s payload: %v", verb, err)
}
