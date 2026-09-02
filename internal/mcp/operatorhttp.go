package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/guard"
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
// Stage one serves reads only. When mutations join (revoke, issue, consent
// answers), each rides the same envelope, and ledgering into the agent log
// is designed against those verbs — for reads, the stderr line naming
// label, fingerprint and verb is the record.

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
	h := &operatorHandler{cfg: cfg, nonces: operator.NewNonces(cfg.NonceTTL)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /operator/v1/challenge", h.challenge)
	mux.HandleFunc("POST /operator/v1/call", h.call)
	return mux
}

type operatorHandler struct {
	cfg    OperatorConfig
	nonces *operator.Nonces
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
	label, ok := h.cfg.Roster.Verify(env, h.cfg.URL)
	if !ok {
		h.refuse(w, fmt.Sprintf("signature rejected for fingerprint %q, verb %q — a valid-looking envelope "+
			"may have been signed for a different server than %q", env.Fingerprint, env.Verb, h.cfg.URL))
		return
	}
	h.logf("%s (%s) called %s", label, env.Fingerprint, env.Verb)
	res, verr := h.dispatch(env)
	if verr != nil {
		// Past the signature the caller is a named operator, and owed the
		// real error — this is their server misbehaving, not a stranger
		// probing it.
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: verr})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *operatorHandler) dispatch(env operator.Envelope) (any, *view.Error) {
	switch env.Verb {
	case operator.VerbStatus:
		return operator.Status{
			Version:          h.cfg.Version,
			Agent:            h.cfg.Agent,
			GuardOn:          guard.Enabled(),
			GuardFingerprint: guard.Fingerprint(),
			Operators:        h.cfg.Roster.Labels(),
		}, nil
	case operator.VerbGrantList:
		grants, verr := grant.Load()
		if verr != nil {
			return nil, verr
		}
		return operator.GrantList{Grants: grants, Suppressed: grant.Suppressed()}, nil
	default:
		return nil, view.Errorf("core.operator.verb",
			"this server does not answer %q — its rta may be older than your client", env.Verb).
			WithHint("`rta operator status --server <name>` reports the server's version")
	}
}
