package plugin

import (
	"strings"
	"testing"
)

// A Piped input is text the CLI may read from a pipe when it is left out,
// and one every other surface requires. A declaration under which the pipe
// could never be read, or the requirement never met, is refused where its
// author is.
func TestAPipedInputIsOptionalTextNothingElseFills(t *testing.T) {
	declare := func(f Field) error {
		return Plugin{Name: "demo", Summary: "demo", Capabilities: []Capability{{
			ID: "demo.decode", Summary: "decode", Safety: Read, Run: noop, Inputs: []Field{f},
		}}}.Validate()
	}
	for _, f := range []Field{
		{Name: "token", Type: Secret, Positional: true, Piped: true},
		{Name: "input", Type: Text, Positional: true, Piped: true},
		{Name: "words", Type: Secret, Piped: true},
		{Name: "body", Type: String, Piped: true},
	} {
		if err := declare(f); err != nil {
			t.Errorf("%+v was refused: %v", f, err)
		}
	}
	for _, tc := range []struct {
		f    Field
		says string
	}{
		{Field{Name: "token", Type: Secret, Piped: true, Required: true}, "Required"},
		{Field{Name: "token", Type: String, Piped: true, Default: "x"}, "Default"},
		{Field{Name: "token", Type: String, Piped: true, Config: "token"}, "Config"},
		{Field{Name: "token", Type: Secret, Piped: true, Local: true}, "Local"},
		{Field{Name: "count", Type: Int, Piped: true}, "a pipe carries text"},
		{Field{Name: "keys", Type: StringSlice, Piped: true}, "a pipe carries text"},
	} {
		if err := declare(tc.f); err == nil || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%+v: %v, want a refusal naming %q", tc.f, err, tc.says)
		}
	}
}

// What a built-in may declare, a plugin may not: its process never sees the
// CLI's pipe. The out-of-process check is the one the host and the SDK's
// Serve both run, so the author is refused by the same words the host uses.
func TestAPipedInputIsForBuiltInsAlone(t *testing.T) {
	p := Plugin{Name: "demo", Summary: "demo", Capabilities: []Capability{{
		ID: "demo.decode", Summary: "decode", Safety: Read, Run: noop,
		Inputs: []Field{{Name: "token", Type: Secret, Positional: true, Piped: true}},
	}}}
	if err := p.Validate(); err != nil {
		t.Fatalf("a built-in's Piped input was refused: %v", err)
	}
	err := p.ValidateOutOfProcess()
	if err == nil || !strings.Contains(err.Error(), "only a built-in") || !strings.Contains(err.Error(), "demo.decode") {
		t.Fatalf("a plugin's Piped input: %v, want a refusal naming it", err)
	}
	p.Capabilities[0].Inputs[0].Piped = false
	if err := p.ValidateOutOfProcess(); err != nil {
		t.Fatalf("the same declaration without Piped was refused: %v", err)
	}
}
