package main

import (
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// Every shared connection input must be Local. These
// fields together name which server a call reaches and as whom, and an MCP
// caller may not choose that — an agent that could would point rta at a
// server of its own and have the host supply the operator's credential
// beside it, which is exactly what it could do before those inputs were made Local.
//
// Written against connFields() rather than against a list of names so that
// an input added here later is covered the day it is added, which is the
// only version of this test worth having.
func TestEveryConnectionInputIsLocal(t *testing.T) {
	for _, f := range connFields() {
		if !f.Local {
			t.Errorf("%s: connection input is not Local — an MCP caller could redirect this call", f.Name)
		}
	}
}

// Only a genuine credential opts into EnvFallback. A field that merely
// chooses a destination must not be fillable from an ambient variable the
// MCP server happened to inherit — the EnvFallback distinction this leans on.
func TestOnlySecretsUseEnvFallback(t *testing.T) {
	for _, f := range connFields() {
		if f.EnvFallback && f.Type != plugin.Secret {
			t.Errorf("%s: non-secret input declares EnvFallback (%s); a destination must come from a caller or config",
				f.Name, f.Type)
		}
	}
}
