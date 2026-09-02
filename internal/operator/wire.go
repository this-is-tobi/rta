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
)

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
