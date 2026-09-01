package plugin

import (
	"context"
	"strings"
)

// The completion surface: what a field offers to be completed to, and the
// request such an offer is computed against — no credentials, and a surface
// marker so a Suggest can tell the tab key from a run.

// Candidates returns what a human surface may offer for this input:
// the closed set when there is one, otherwise whatever exists right now.
// Entries may carry a tab-separated description (see Field.Suggest).
//
// Options wins over Suggest: a field that declares both is saying "these are
// the values, and here are some of them", which the closed set already
// answers.
//
// A Live Suggest never answers here. This is the per-keystroke channel — the
// TUI re-evaluates it as siblings change, shell completion builds it a
// credential-less request — and a network read on that cadence is exactly
// what this channel exists to prevent. The deliberate channel calls f.Suggest itself,
// with LiveRequest, on a completion press. One gate at the fork rather than
// one per surface, for CompletionRequest's reason: a rule that lives in two
// places is a rule that will hold in one of them.
func (f Field) Candidates(ctx context.Context, req Request) []string {
	if f.Live {
		return nil
	}
	if len(f.Options) > 0 {
		return f.Options
	}
	if f.Suggest == nil {
		return nil
	}
	return f.Suggest(ctx, req)
}

// CompletionRequest is the request a Suggest is answered with: what has been
// supplied so far, minus every input this capability declares as a Secret.
//
// **One builder, because it is a rule and not a convenience.** Both human
// surfaces resolve the caller's values before asking — which is the point, a
// suggestion that depends on a sibling needs to see it — and resolving pulls a
// credential in from the environment fallback (Resolve's EnvFallback layer) or
// straight out of the masked box a person is typing into. So the shell shipped
// $RTA_S3_SECRET_KEY to the rta-s3 subprocess on `--content-type <tab>`, and
// the TUI shipped every *prefix* of a passphrase as it was typed, because huh
// re-evaluates a field's suggestions on every keystroke of its siblings.
//
// The receiving process is the one that would legitimately be handed that
// credential at Run, so this is early and unasked-for delivery rather than a
// new principal learning it — a form opened and abandoned, a plugin spawned
// only to answer a completion. That is still not something to do on a tab key,
// and the fix belongs here rather than at either call site: a rule that lives
// in two places is a rule that will hold in one of them.
func CompletionRequest(c Capability, values map[string]any) Request {
	secret := make(map[string]bool, len(c.Inputs))
	for _, f := range c.Inputs {
		if f.Type.Sensitive() {
			secret[f.Name] = true
		}
	}
	out := make(map[string]any, len(values))
	for k, v := range values {
		if secret[k] {
			continue
		}
		out[k] = v
	}
	return NewRequest(out, false, false).WithSurface(SurfaceCompletion)
}

// LiveRequest is the request a Live Suggest is answered with: everything the
// run would see, credentials included, already resolved through the same
// layers by the caller.
//
// The strip CompletionRequest performs is the rule for the keystroke channel,
// where delivery is early and unasked-for — a form abandoned, a prefix of a
// passphrase shipped mid-typing. A live completion is the case that
// write-up left open: a deliberate press, on a field whose plugin is the
// pinned binary that receives these exact values at Run. Same recipient,
// moments earlier, at the operator's explicit request — so the values cross
// whole. The surface still says completion, because a Suggest must stay
// read-only whatever it was handed, and a plugin that would prompt or take a
// visible moment has to be able to tell this call from a run.
func LiveRequest(values map[string]any) Request {
	return NewRequest(values, false, false).WithSurface(SurfaceCompletion)
}

// CandidateValue drops the description from a completion entry, leaving the
// value a caller would actually submit.
func CandidateValue(entry string) string {
	if i := strings.IndexByte(entry, '\t'); i >= 0 {
		return entry[:i]
	}
	return entry
}
