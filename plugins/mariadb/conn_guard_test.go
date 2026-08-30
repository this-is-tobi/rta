package main

import (
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// Every shared connection input must be Local. These fields together name
// which server a call reaches and as whom, and an MCP caller may not choose
// that — an agent that could would point rta at a server of its own and have
// the host supply the operator's credential beside it.
//
// Written against connFields() rather than against a list of names, so an
// input added here later is covered the day it is added. That is the only
// version of this test worth having: a list of names passes forever while the
// thing it is guarding grows past it.
func TestEveryConnectionInputIsLocal(t *testing.T) {
	for _, f := range connFields() {
		if !f.Local {
			t.Errorf("%s: connection input is not Local — an MCP caller could redirect this call", f.Name)
		}
	}
}

// Only a genuine credential opts into EnvFallback. A field that merely chooses
// a destination must not be fillable from an ambient variable the MCP server
// happened to inherit.
func TestOnlySecretsUseEnvFallback(t *testing.T) {
	for _, f := range connFields() {
		if f.EnvFallback && f.Type != plugin.Secret {
			t.Errorf("%s: non-secret input declares EnvFallback (%s); a destination must come from a caller or config",
				f.Name, f.Type)
		}
	}
}

// The password must be a Secret, not a String. The type is what makes rta
// redact it in the record and refuse to print it back — declaring it as an
// ordinary string would put a credential in the agent ledger.
func TestThePasswordIsDeclaredSecret(t *testing.T) {
	for _, f := range connFields() {
		if f.Name != "password" {
			continue
		}
		if f.Type != plugin.Secret {
			t.Errorf("password is %s, want Secret — the type is what redacts it", f.Type)
		}
		return
	}
	t.Fatal("connFields() has no password input")
}

// Every capability gets the connection inputs, because cap appends them. A
// capability that declared its own would drift from the rest, and the one that
// forgot would be unreachable against any server but the default.
func TestEveryCapabilityCarriesTheConnectionInputs(t *testing.T) {
	shared := map[string]bool{}
	for _, f := range connFields() {
		shared[f.Name] = true
	}
	for _, c := range Plugin().Capabilities {
		got := map[string]bool{}
		for _, f := range c.Inputs {
			got[f.Name] = true
		}
		for name := range shared {
			if !got[name] {
				t.Errorf("%s: missing connection input %q — was it declared without cap()?", c.ID, name)
			}
		}
	}
}
