package operator

import "github.com/this-is-tobi/rule-them-all/internal/grant"

// The verbs a server dispatches, and each verb's result shape. Stage one of
// the channel is deliberately read-only: proving the identity layer end to
// end with nothing mintable riding on it. Mutations (revoke first, then
// issue and consent answers) join as their own stages, each behind the same
// envelope.
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

// RevokeSpec mirrors `grant revoke`'s selectors.
type RevokeSpec struct {
	All     bool   `json:"all,omitempty"`
	Target  string `json:"target,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Profile string `json:"profile,omitempty"`
	Agent   string `json:"agent,omitempty"`
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
