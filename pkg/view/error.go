package view

import (
	"errors"
	"fmt"
)

// Error is the first-class error contract. Renderers show Message and a
// styled Hint; JSON output emits it structurally; AI agents get a stable
// Code they can branch on and a Hint they can act on.
type Error struct {
	// Code is stable and namespaced, e.g. "pg.conn.refused".
	Code      string `json:"code"`
	Message   string `json:"message"`
	Hint      string `json:"hint,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
	// Refusal separates "you may not ask this" from "the work broke", and
	// only the error's author knows which happened — by the time an error
	// reaches the host it is one opaque value, which is why the MCP bridge
	// used to seal every surface gate's no as an execution failure.
	//
	// Set it (via Refusef) on policy gates only: a handler declining the
	// call because of who is asking or from where — a localOnly capability
	// probed over MCP, a credential-minting verb an agent may never hold.
	// Not on validation ("this argument is malformed") and not on failures
	// ("the connection dropped"): a refusal is about the caller's standing,
	// and an operator grepping the ledger for refusals is asking what their
	// agent tried that policy would not let it do.
	Refusal bool `json:"refusal,omitempty"`
	// Advisory marks a warning that sits beside a complete answer rather than
	// accounting for a missing part of it. Warnings exist so a partial answer
	// says it is partial (Table.Warnings, Sections.Warnings), and a surface
	// heads a page carrying one as partial — which a listing whose every row
	// is there, carrying something a person should know about those rows,
	// is not: grant list beside a grant bound to a replaced plugin build, or
	// beside a server running another build of rta, read "partial" above a
	// roster missing nothing.
	//
	// Only on a warning, and only by its author, who alone knows whether
	// what it says is a gap. It changes how a surface heads the answer, not
	// whether the warning is shown: every format still carries it, coded.
	Advisory bool `json:"advisory,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func (*Error) isView() {}

// Errorf builds a coded Error with a formatted message and no hint.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Refusef builds a policy refusal: an Error marked Refusal, for the gates
// that decline a call over who is asking rather than over what went wrong.
// A separate constructor rather than a chained setter so that the policy
// gates are one grep away: `view.Refusef` lists every place a handler says
// no to a caller's standing.
func Refusef(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Refusal: true}
}

// WithHint returns a copy of the error carrying an actionable next step.
func (e *Error) WithHint(hint string) *Error {
	c := *e
	c.Hint = hint
	return &c
}

// AsError extracts a *view.Error from err, wrapping foreign errors under the
// given fallback code so every failure leaving the host is coded.
func AsError(err error, fallbackCode string) *Error {
	if err == nil {
		return nil
	}
	// Through wrapping: a handler that annotates a view.Error with %w on its
	// way out still returned a coded error, and a caller branching on the
	// code must see it rather than the fallback.
	//
	// Not through to a nil one: errors.As finds a *view.Error that is nil as
	// readily as any other, and an error of the handler's own wrapping one —
	// fmt.Errorf("listing: %w", err) around a helper's nil *view.Error — is a
	// failure its wrapper's words describe. Handed on as the nil pointer it
	// found, it reached every surface as a failure with no code and no
	// message, the wrapper's words dropped. An err that is itself a nil
	// *view.Error stays the nil it is, as it always has here.
	var ve *Error
	if errors.As(err, &ve) {
		if ve != nil {
			return ve
		}
		//nolint:errorlint // the error itself is the question, not anything it wraps
		if self, ok := err.(*Error); ok && self == nil {
			return nil
		}
	}
	return &Error{Code: fallbackCode, Message: err.Error()}
}
