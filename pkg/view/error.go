package view

import "fmt"

// Error is the first-class error contract. Renderers show Message and a
// styled Hint; JSON output emits it structurally; AI agents get a stable
// Code they can branch on and a Hint they can act on.
type Error struct {
	// Code is stable and namespaced, e.g. "pg.conn.refused".
	Code      string `json:"code"`
	Message   string `json:"message"`
	Hint      string `json:"hint,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func (*Error) isView() {}

// Errorf builds a coded Error with a formatted message and no hint.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
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
	if ve, ok := err.(*Error); ok {
		return ve
	}
	return &Error{Code: fallbackCode, Message: err.Error()}
}
