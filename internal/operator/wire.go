package operator

import (
	"strings"
	"time"

	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/lockdown"
)

// The verbs a server dispatches, and each verb's result shape. They landed
// in deliberate order — reads first to prove the identity layer with
// nothing mintable riding on it, then revocation (fail-safe), then
// issuance, then consent answers — but every verb rides the same envelope,
// and the order no longer shows at runtime.
//
// A new verb starts refused for role=read enrollments: Role.Allows names
// its read set verb by verb, so declaring a constant here grants a
// read-only key nothing until that switch says otherwise.
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
	// VerbLockList reads the frozen principals — who is refused right now,
	// and why, which is dashboard material and so part of the read set.
	VerbLockList = "lock.list"
	// VerbLockAdd freezes one principal on the server, effective on its
	// next call — the instant path revocation needs when editing a roster
	// and restarting is too slow, and the reason a compromised operator
	// key does not get to keep talking until a redeploy.
	VerbLockAdd = "lock.add"
	// VerbLockRm lifts one lock. The expanding direction, so it stays a
	// full-operator verb like every other mutation.
	VerbLockRm = "lock.rm"
)

// Verbs is the wire's whole vocabulary, for the callers that need it
// closed: the ledger's recording rule classifies every verb as recorded
// or not, and its test walks this list so a new constant above cannot
// ship unclassified.
func Verbs() []string {
	return []string{
		VerbStatus, VerbGrantList, VerbGrantRevoke, VerbGrantPrepare, VerbGrantIssue,
		VerbConsentList, VerbConsentAnswer, VerbLockList, VerbLockAdd, VerbLockRm,
	}
}

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
	// Role narrows to the grants one `grant issue` issued, the way the
	// other selectors narrow: `--role dev` alone takes dev back from every
	// agent, `--role dev --agent claude` from one.
	Role   string `json:"role,omitempty"`
	DryRun bool   `json:"dryRun,omitempty"`
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
	Version          string         `json:"version"`
	Agent            string         `json:"agent,omitempty"`
	GuardOn          bool           `json:"guardOn"`
	GuardFingerprint string         `json:"guardFingerprint,omitempty"`
	Operators        []OperatorInfo `json:"operators"`
}

// OperatorInfo is one roster row as status reports it and the startup
// banner prints it: who, and what their enrollment covers.
type OperatorInfo struct {
	Label string `json:"label"`
	Role  Role   `json:"role"`
	// Expires is the date as the roster wrote it (YYYY-MM-DD), empty for
	// never. A string rather than a time both because omitempty cannot
	// elide a zero time.Time and because this is the display half — the
	// enforced half is the parsed time Verify hands the dispatch.
	Expires string `json:"expires,omitempty"`
}

// String renders a row for a human listing — an annotation only when it
// subtracts something, since a bare label is what enrollment has always
// meant. An already-past date says so outright: the row that reads
// "expired" on the status page is the one a person should go delete.
func (o OperatorInfo) String() string {
	var notes []string
	if o.Role == RoleRead {
		notes = append(notes, "read")
	}
	if o.Expires != "" {
		word := "expires "
		if t, err := time.ParseInLocation("2006-01-02", o.Expires, time.Local); err == nil && !time.Now().Before(t) {
			word = "expired "
		}
		notes = append(notes, word+o.Expires)
	}
	if len(notes) == 0 {
		return o.Label
	}
	return o.Label + " (" + strings.Join(notes, ", ") + ")"
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

// LockSpec asks the server to freeze one principal. TTL travels as the
// raw string the operator typed, for IssueSpec's reason: the server's
// parser is the one that binds. Empty means until removed.
type LockSpec struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Note string `json:"note,omitempty"`
	TTL  string `json:"ttl,omitempty"`
}

// LockRmSpec lifts one lock.
type LockRmSpec struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// LockList is VerbLockList's result: the live locks as the server's own
// store loads them, expired rows already dropped.
type LockList struct {
	Locks []lockdown.Lock `json:"locks,omitempty"`
}

// LockRmOutcome distinguishes "unlocked" from "nothing was locked" — an
// operator clearing an incident deserves to know which sentence is true.
type LockRmOutcome struct {
	Removed bool `json:"removed"`
}
