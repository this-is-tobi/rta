package plugin

import "testing"

// The two questions a SecretSlice has to answer separately, and the reason it
// is a type rather than a convention.
//
// Sensitive is what every sink that must not write a caller's value down was
// actually asking when it tested `== Secret`. Repeatable is what every
// surface was asking when it tested `== StringSlice`. A type that answered
// only one of them would fail silently on the other: masked and truncated to
// its last element, or intact and written to the audit log in cleartext.
func TestSecretSliceIsBothCredentialAndList(t *testing.T) {
	for _, c := range []struct {
		t                     FieldType
		sensitive, repeatable bool
	}{
		{String, false, false},
		{Int, false, false},
		{Bool, false, false},
		{Float, false, false},
		{Text, false, false},
		{Path, false, false},
		{StringSlice, false, true},
		{Secret, true, false},
		{SecretSlice, true, true},
	} {
		t.Run(string(c.t), func(t *testing.T) {
			if got := c.t.Sensitive(); got != c.sensitive {
				t.Errorf("Sensitive() = %v, want %v", got, c.sensitive)
			}
			if got := c.t.Repeatable(); got != c.repeatable {
				t.Errorf("Repeatable() = %v, want %v", got, c.repeatable)
			}
		})
	}
}

// Every credential type must be refused as a scope. A scope is written into
// the grant file, printed by `rta grant list` and offered as a completion, so
// a credential there is on disk three times over — and it cannot work anyway,
// since a value that differs per call names a grant covering a call that has
// already happened.
func TestACredentialCannotBeAScope(t *testing.T) {
	for _, ft := range []FieldType{Secret, SecretSlice} {
		t.Run(string(ft), func(t *testing.T) {
			c := Capability{
				ID: "x.do", Summary: "s", Safety: Write, NeedsGrant: true, Scope: "token",
				Inputs: []Field{{Name: "token", Type: ft, Required: true, Help: "h"}},
				Run:    nil,
			}
			p := Plugin{Name: "x", Summary: "s", Version: "0.1.0",
				Capabilities: []Capability{c}}
			if err := p.Validate(); err == nil {
				t.Fatal("a credential was accepted as a grant scope")
			}
		})
	}
}
