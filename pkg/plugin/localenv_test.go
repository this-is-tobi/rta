package plugin

import (
	"context"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func localCap() Capability {
	return Capability{
		ID: "pg.query", Summary: "q", Safety: Read,
		Inputs: []Field{
			{Name: "sql", Type: String, Required: true, Help: "sql"},
			{Name: "password", Type: Secret, Local: true, EnvFallback: true, Help: "the connection password"},
			// Local but not EnvFallback: a destination-shaped field
			// (TestALocalFieldWithoutEnvFallbackIsNeverFilledFromTheEnvironment's
			// subject), the shape kv.get's --out and kv.set's --file are in
			// the real registry.
			{Name: "ssl-mode", Type: String, Local: true, Help: "sslmode"},
		},
		Run: func(context.Context, Request) (view.View, error) { return view.Text{}, nil },
	}
}

// An external plugin had no way to obtain a credential. Its process inherits
// an allowlist of seven variable names and RTA_* is deliberately not among
// them, Config is refused on a Secret, an MCP caller's value is stripped, and
// nothing filled a Local input — so a plugin declaring one received "".
// Verified against a real plugin binary before this existed: PGPASSWORD empty,
// the Local input empty.
//
// kv had resolved RTA_KV_PASSPHRASE by hand since it was written, which is
// this convention already, available only to a built-in because a built-in
// sees rta's whole environment, and there are no second-class plugins.
func TestALocalInputIsFilledFromTheHostEnvironment(t *testing.T) {
	t.Setenv("RTA_PG_PASSWORD", "hunter2")
	got := Resolve(localCap(), Inputs{Caller: map[string]any{"sql": "select 1"}})
	if got["password"] != "hunter2" {
		t.Errorf("password = %v, want the value from RTA_PG_PASSWORD", got["password"])
	}
}

// A regression test for a real bug review caught: every
// Local field used to resolve from the environment, which is right for a
// credential and wrong for a field that only chooses a destination —
// kv.get's own --out is Local so a grant on kv.get cannot be read as "and
// write the value wherever you like", and an ambient RTA_KV_OUT in the
// server's environment defeated that the same way an explicit MCP argument
// would have. EnvFallback now has to be declared explicitly; ssl-mode
// (Local, no EnvFallback) must stay empty no matter what is exported.
func TestALocalFieldWithoutEnvFallbackIsNeverFilledFromTheEnvironment(t *testing.T) {
	t.Setenv("RTA_PG_SSL_MODE", "disable")
	got := Resolve(localCap(), Inputs{Caller: map[string]any{"sql": "select 1"}})
	if v, present := got["ssl-mode"]; present {
		t.Errorf("ssl-mode = %q, want it absent — Local without EnvFallback must not read the environment", v)
	}
}

// The name is derived from the plugin's own namespace and never declared,
// which is the security property: a declared Env field would let a hostile
// plugin name AWS_SECRET_ACCESS_KEY and have the host hand it over.
func TestALocalInputCannotReachAnotherNamespacesVariable(t *testing.T) {
	t.Setenv("RTA_KV_PASSPHRASE", "the kv store's passphrase")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "an unrelated credential")
	got := Resolve(localCap(), Inputs{Caller: map[string]any{"sql": "select 1"}})
	for _, leaked := range []any{"the kv store's passphrase", "an unrelated credential"} {
		for name, v := range got {
			if v == leaked {
				t.Errorf("input %q received %q, which belongs to something else", name, v)
			}
		}
	}
}

// Only Local inputs. Filling an ordinary one from the environment would mean
// an exported variable silently changing what a command does.
func TestANonLocalInputIsNeverFilledFromTheEnvironment(t *testing.T) {
	t.Setenv("RTA_PG_SQL", "drop table users")
	got := Resolve(localCap(), Inputs{Caller: map[string]any{"sql": "select 1"}})
	if got["sql"] != "select 1" {
		t.Errorf("sql = %v; the environment overwrote a non-Local input", got["sql"])
	}
}

// An explicitly typed credential beats an ambient one.
func TestACallerSuppliedLocalValueBeatsTheEnvironment(t *testing.T) {
	t.Setenv("RTA_PG_PASSWORD", "from the environment")
	got := Resolve(localCap(), Inputs{Caller: map[string]any{"sql": "x", "password": "typed"}})
	if got["password"] != "typed" {
		t.Errorf("password = %v, want the caller's value", got["password"])
	}
}

// An unset variable must not become an empty credential: "" and "absent" are
// different, and a store unlocked with an empty passphrase is not the same
// event as one that refused to unlock.
func TestAnUnsetVariableLeavesTheInputAbsent(t *testing.T) {
	got := Resolve(localCap(), Inputs{Caller: map[string]any{"sql": "x"}})
	if v, present := got["password"]; present {
		t.Errorf("password is present as %q with nothing set", v)
	}
}

func TestLocalEnvVarNaming(t *testing.T) {
	for _, tc := range []struct{ capID, input, want string }{
		{"pg.query", "password", "RTA_PG_PASSWORD"},
		{"kv.get", "passphrase", "RTA_KV_PASSPHRASE"}, // the name kv already uses
		{"kv.get", "identity", "RTA_KV_IDENTITY"},
		{"pg.table.list", "ssl-mode", "RTA_PG_SSL_MODE"},
	} {
		if got := LocalEnvVar(tc.capID, tc.input); got != tc.want {
			t.Errorf("LocalEnvVar(%q, %q) = %q, want %q", tc.capID, tc.input, got, tc.want)
		}
	}
}
