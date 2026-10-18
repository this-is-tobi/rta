package plugin

import (
	"maps"
	"testing"
)

// The values of a call's connection that the operator stated are the ones its
// config or a profile supplied for a Local input: not a default, not the
// caller's own, and not an input anybody may supply, which is nobody's
// statement about where the connection goes.
func TestOperatorStatedIsWhatTheConfigAndTheProfileSuppliedForALocalInput(t *testing.T) {
	c := Capability{
		ID: "pg.query", Summary: "s", Safety: Read,
		Inputs: []Field{
			{Name: "host", Type: String, Default: "localhost", Config: "host", Local: true},
			{Name: "port", Type: Int, Default: 5432, Config: "port", Local: true},
			{Name: "user", Type: String, Config: "user", Local: true},
			{Name: "database", Type: String, Default: "postgres", Config: "database", Local: true},
			{Name: "password", Type: Secret, Local: true, EnvFallback: true},
			{Name: "limit", Type: Int, Default: 15, Config: "limit"},
			{Name: "sql", Type: String},
		},
	}
	t.Setenv(LocalEnvVar(c.ID, "password"), "from the environment")

	t.Run("config", func(t *testing.T) {
		req := ResolveRequest(c, Inputs{
			Caller: map[string]any{"sql": "select 1"},
			Config: map[string]any{"host": "db.internal", "port": 6543, "limit": 5},
		}, false, false)
		want := map[string]any{"host": "db.internal", "port": 6543}
		if got := req.OperatorStated(c); !maps.Equal(got, want) {
			t.Errorf("OperatorStated = %v, want only the Local inputs the config set: %v", got, want)
		}
	})

	t.Run("a profile", func(t *testing.T) {
		req := ResolveRequest(c, Inputs{
			ProfileName: "staging",
			Profile:     map[string]any{"host": "staging.internal", "user": "app"},
			Config:      map[string]any{"database": "orders"},
		}, false, false)
		want := map[string]any{"host": "staging.internal", "user": "app", "database": "orders"}
		if got := req.OperatorStated(c); !maps.Equal(got, want) {
			t.Errorf("OperatorStated = %v, want %v", got, want)
		}
	})

	t.Run("nothing stated", func(t *testing.T) {
		req := ResolveRequest(c, Inputs{Caller: map[string]any{"sql": "select 1"}}, false, false)
		if got := req.OperatorStated(c); len(got) != 0 {
			t.Errorf("OperatorStated = %v for a call that only had defaults and the environment, want none", got)
		}
	})

	t.Run("a value overlaid on the request is not the operator's", func(t *testing.T) {
		req := ResolveRequest(c, Inputs{Config: map[string]any{"host": "db.internal"}}, false, false).
			With(map[string]any{"host": "elsewhere"})
		if got := req.OperatorStated(c); len(got) != 0 {
			t.Errorf("OperatorStated = %v after the value was replaced, want none", got)
		}
	})

	t.Run("a copy", func(t *testing.T) {
		req := ResolveRequest(c, Inputs{Config: map[string]any{"host": "db.internal"}}, false, false)
		req.OperatorStated(c)["host"] = "changed"
		if got := req.String("host"); got != "db.internal" {
			t.Errorf("the request's host is %q after the copy was changed", got)
		}
	})
}
