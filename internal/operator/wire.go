package operator

import (
	"github.com/this-is-tobi/rule-them-all/internal/consent"
	"github.com/this-is-tobi/rule-them-all/internal/grant"
)

// The verbs a server dispatches, and each verb's result shape. They landed
// in deliberate order — reads first to prove the identity layer with
// nothing mintable riding on it, then revocation (fail-safe), then
// issuance, then consent answers — but every verb rides the same envelope,
// and the order no longer shows at runtime.
const (
	// VerbStatus answers what a server is: version, agent name, guard state,
	// enrolled operators. The remote analogue of the doctor's glance.
	VerbStatus = "status"
	// VerbGrantList answers what standing authority the server holds.
	VerbGrantList = "grant.list"
	// VerbGrantRevoke takes authority back — the fail-safe mutation, and so
	// the first one the channel carried.
	VerbGrantRevoke = "grant.revoke"
	// VerbGrantPrepare asks the server to build the grant an IssueSpec
	// describes: validation, TTL clamping, profile pinning and origin
	// attribution all happen where the config and policy live, and what
	// comes back is exactly the authority the operator is about to sign —
	// the review-before-signing step, made structural.
	VerbGrantPrepare = "grant.prepare"
	// VerbGrantIssue submits a prepared grant carrying the operator's
	// signature over its authority bytes. The server re-validates and
	// stores it; the guard's own load-time enforcement then verifies that
	// signature on every read, like any other guard-signed row.
	VerbGrantIssue = "grant.issue"
	// VerbConsentList answers what is parked right now: the server's own
	// queue walk, honest requests only, so `rta agent pending --server`
	// reads the same rows the person at the machine would.
	VerbConsentList = "consent.list"
	// VerbConsentAnswer allows or denies one parked call. The answer names
	// the digest of the call as the operator's machine displayed it — the
	// same binding the local flow rests on, carried inside the signed
	// envelope so it survives the network.
	VerbConsentAnswer = "consent.answer"
)

// IssueSpec is what an operator asks to allow, in the raw strings they
// typed. Deliberately unparsed: the server's rules — its policy ceiling,
// its TTL grammar, its catalogue — are the ones that bind, and parsing
// client-side would let two versions disagree about what was requested.
type IssueSpec struct {
	Target  string `json:"target"`
	Scope   string `json:"scope,omitempty"`
	Profile string `json:"profile,omitempty"`
	Agent   string `json:"agent,omitempty"`
	TTL     string `json:"ttl,omitempty"`
	Note    string `json:"note,omitempty"`
	MaxUses int    `json:"maxUses,omitempty"`
	Rate    string `json:"rate,omitempty"`
}

// Prepared is the server's authoritative draft: the grant exactly as it
// would be stored, plus the notes the local flow would have printed —
// a clamped TTL, an inactive profile — worded by the machine that knows.
type Prepared struct {
	Grant grant.Grant `json:"grant"`
	Notes []string    `json:"notes,omitempty"`
}

// RevokeSpec mirrors `grant revoke`'s selectors. DryRun travels to the
// server rather than short-circuiting client-side, because the honest
// preview — "would revoke 3 grant(s), still covered by kv" — is computed
// from the server's store under the server's lock, and a guess made from
// here would be the thing dry runs exist to not be.
type RevokeSpec struct {
	All     bool   `json:"all,omitempty"`
	Target  string `json:"target,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Profile string `json:"profile,omitempty"`
	Agent   string `json:"agent,omitempty"`
	DryRun  bool   `json:"dryRun,omitempty"`
}

// RevokeOutcome is what one revocation decided, computed under the store's
// lock on the server: how many rows went, whether nothing was active at
// all, and the wider grant that still covers the target after the named
// rows are gone — the fact the local flow refuses to leave unsaid.
type RevokeOutcome struct {
	Revoked    int          `json:"revoked"`
	NoneActive bool         `json:"noneActive,omitempty"`
	Still      *grant.Grant `json:"still,omitempty"`
}

// Status is VerbStatus's result.
type Status struct {
	Version          string   `json:"version"`
	Agent            string   `json:"agent,omitempty"`
	GuardOn          bool     `json:"guardOn"`
	GuardFingerprint string   `json:"guardFingerprint,omitempty"`
	Operators        []string `json:"operators"`
}

// GrantList is VerbGrantList's result: the active grants as the server's own
// store loads them — ceiling applied, expired rows dropped — plus how many
// rows a tightened team policy is currently suppressing, the same number the
// local listing prints.
type GrantList struct {
	Grants     []grant.Grant `json:"grants"`
	Suppressed int           `json:"suppressed"`
}

// ConsentList is VerbConsentList's result: the queue as the server's own
// Scan walks it. Tampered carries the ids Scan kept off the queue, because
// a request rewritten under a *remote* server is exactly as alarming as one
// rewritten under a local one, and the operator reading this listing is the
// person the alarm is for.
type ConsentList struct {
	Waiting  []consent.Request `json:"waiting,omitempty"`
	Tampered []string          `json:"tampered,omitempty"`
}

// AnswerSpec is one consent decision on its way to the server. Digest is
// derived on the operator's machine from the fields it displayed — never
// copied out of what the server sent — so the answer authorizes exactly
// what was read, and a queue entry that changed in between, or a server
// that showed one call while parking another, produces a refusal instead
// of an approval.
type AnswerSpec struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
	Allow  bool   `json:"allow"`
}

// AnswerOutcome names what was just answered, so the confirmation the
// operator reads is the server's account of what it released rather than
// this machine's memory of what it asked.
type AnswerOutcome struct {
	Cap    string   `json:"capability"`
	Scopes []string `json:"scopes,omitempty"`
}
