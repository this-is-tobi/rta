package agent

import (
	"slices"
	"strings"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/pkg/format"
)

// What became of a call, in the few words a row has room for, and which of the
// operator's questions a refusal belongs to.
//
// **Display only.** The record keeps what it has always kept: an outcome, an
// authorization and the dotted code of whatever said no. The words and the
// split below are read back out of those at the moment a screen is drawn, so a
// row written last year reads the same as one written today and the chain
// carries no new vocabulary. A refusal for a misspelled argument and one for a
// missing grant are both `refused` there, and stay that way; what changes is
// that the two are told apart where a person scans for attempts to cross the
// boundary.

// refusalKind sorts a refusal by who can act on it.
type refusalKind int

const (
	// refusedOther is every refusal that is neither of the two below: a lock,
	// a path root, a connection, a pace, a decline. The boundary said no, and
	// the row's own words say which gate.
	refusedOther refusalKind = iota
	// refusedNeedsGrant is a call a person has to allow before it can run: the
	// one refusal the operator answers with a command.
	refusedNeedsGrant
	// refusedMalformed is a call that never named anything the boundary could
	// judge — an argument that does not exist, a tool that does not — which the
	// agent fixes and the operator cannot.
	refusedMalformed
)

func kindOf(code string) refusalKind {
	switch code {
	case "core.grant.required":
		return refusedNeedsGrant
	case "core.mcp.badargs", "core.mcp.unknown":
		return refusedMalformed
	}
	return refusedOther
}

// codeOf is the code of whatever refused or broke a call. A row written before
// the code and the reason were stored apart carries them glued, "code: message",
// in the reason, and is read the same way as one that does not.
func codeOf(e agentlog.Entry) string {
	if e.Code != "" {
		return e.Code
	}
	if head, _, glued := strings.Cut(e.Reason, ": "); glued && looksLikeCode(head) {
		return head
	}
	return ""
}

// looksLikeCode reports whether s is a dotted lower-case identifier, which is
// what every code is and what a sentence is not.
func looksLikeCode(s string) bool {
	if !strings.Contains(s, ".") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// refusalWords is the reason a refusal gives, as a row says it. A code this
// does not know is shown as it is, without its core. prefix: the code is what
// a script matches on, and an unrecognised one is still more use to a person
// than a vague "refused".
func refusalWords(code string) string {
	switch code {
	case "":
		return ""
	case "core.grant.required":
		return "no grant"
	case "core.grant.rate":
		return "grant paced out"
	case "core.mcp.badargs":
		return "bad argument"
	case "core.mcp.unknown":
		return "unknown tool"
	case "core.mcp.cancelled":
		return "cancelled"
	case "core.mcp.panic":
		return "crashed"
	case "core.consent.declined":
		return "you declined"
	case "core.consent.expired", "core.consent.abandoned":
		return "nobody answered"
	case "core.profile.required":
		return "needs a profile"
	case "core.record.unwritable":
		return "record unwritable"
	}
	switch {
	case strings.HasPrefix(code, "core.lock."):
		return "locked"
	case strings.HasPrefix(code, "core.mcp.path."):
		return "path outside the roots"
	case strings.HasPrefix(code, "core.mcp.result."):
		return "result withheld"
	case strings.HasPrefix(code, "core.profile."):
		return "connection unusable"
	case strings.HasPrefix(code, "core.grant."):
		return "grants unreadable"
	}
	return strings.TrimPrefix(code, "core.")
}

// resultPhrase is a row's outcome, its authorization and its code as the one
// phrase the compact log shows: "ran", "ran · you approved", "refused · no
// grant", "failed · sys.host.read".
//
// A call a standing grant covered says so, because the difference between a
// read that ran because it is free and a write that ran because a grant was
// issued is the one a person reads a log for. A failure shows its code, which
// is short and is what a search for the cause starts from; the sentence is in
// `--detail`.
func resultPhrase(e agentlog.Entry) string {
	if e.Outcome == agentlog.Ran && e.Revealed {
		// The one thing a log of reads is searched for, said where the eye
		// already is: a stored value went to the agent on this call.
		return outcomePhrase(e) + " · revealed"
	}
	return outcomePhrase(e)
}

func outcomePhrase(e agentlog.Entry) string {
	switch e.Outcome {
	case agentlog.Ran:
		switch e.Auth {
		case agentlog.Live:
			return "ran · you approved"
		case agentlog.Standing:
			if e.Role != "" {
				return "ran · by role " + e.Role
			}
			return "ran · by grant"
		case agentlog.Operator:
			if e.Credential != "" {
				return "ran · " + e.Credential
			}
			return "ran · operator"
		}
		return "ran"
	case agentlog.Failed:
		if code := codeOf(e); code != "" {
			return "failed · " + code
		}
		return "failed"
	case agentlog.Refused:
		if words := refusalWords(codeOf(e)); words != "" {
			return "refused · " + words
		}
		return "refused"
	}
	return string(e.Outcome)
}

// callList names calls the way a sentence lists them: each once, in the order
// it was first made, the first few and a count of the rest.
func callList(entries []agentlog.Entry) string {
	const most = 4
	var seen []string
	for _, e := range entries {
		if name := callNamed(e.Cap, e.Records); !slices.Contains(seen, name) {
			seen = append(seen, name)
		}
	}
	if len(seen) <= most {
		return strings.Join(seen, ", ")
	}
	return strings.Join(seen[:most], ", ") + " and " + format.Count(len(seen)-most, "other", "others")
}
