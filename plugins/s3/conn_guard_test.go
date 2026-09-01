package main

import (
	"slices"
	"strings"
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

// A grant on an s3 capability is checked against one field — internal/grant's
// scopes() reads exactly the name in Scope — so any *other* field naming a
// place the call writes to is a destination the operator never approved. That
// is what let a grant scoped to one source key write into any bucket the
// credentials could reach.
//
// Written against the declaration rather than a list of names, so a
// destination added later is covered the day it is added.
func TestNoCallerChosenFieldNamesADestination(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		for _, f := range c.Inputs {
			if strings.HasPrefix(f.Name, "dest-") && f.Name != "dest-key" && !f.Local {
				t.Errorf("%s: %s names a destination and is not Local — a grant is checked "+
					"against %q alone, so an MCP caller could redirect the write",
					c.ID, f.Name, c.Scope)
			}
		}
	}
}

// A grant that cannot be narrowed is a grant nobody narrows. scopes() derives
// the record a call is checked against from the field Scope names, so a gated
// capability declaring no Scope derives "" — and `grant allow <cap> <record>`
// then matches nothing, leaving the capability-wide grant as the only one that
// works. s3.object.rm shipped that way: the one irreversible call here was the
// one that could not be bounded.
func TestEveryGatedCapabilityCanBeNarrowedToARecord(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if !c.NeedsGrant && c.Safety != plugin.Destructive {
			continue
		}
		if c.Scope == "" {
			t.Errorf("%s is gated (%s, grant=%v) but declares no Scope, so every grant on it "+
				"covers every record it can reach", c.ID, c.Safety, c.NeedsGrant)
			continue
		}
		if !slices.ContainsFunc(c.Inputs, func(f plugin.Field) bool { return f.Name == c.Scope }) {
			t.Errorf("%s scopes on %q, which is not one of its inputs", c.ID, c.Scope)
		}
	}
}
