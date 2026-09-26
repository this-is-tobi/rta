package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// requiredServer is a server over one capability, db.query, whose required
// inputs arrive from three places: database from the caller or a profile,
// host from the operator alone, and sql from the caller. ran reports whether
// the handler ever ran.
func requiredServer(t *testing.T, yaml string) (call func(map[string]any) (string, bool), ran *bool, cfg config.Config) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)
	cfgPath := dir + "/config.yaml"
	if err := writeFile(cfgPath, yaml); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", cfgPath)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ran = new(bool)
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "db", Summary: "db", Capabilities: []plugin.Capability{{
		ID: "db.query", Summary: "a query", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Required: true, Local: true, Config: "host", Help: "server"},
			{Name: "database", Type: plugin.String, Required: true, Config: "database", Help: "database"},
			{Name: "sql", Type: plugin.String, Help: "query"},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			*ran = true
			return view.Text{Body: "database=" + req.String("database")}, nil
		},
	}}}); err != nil {
		t.Fatal(err)
	}
	resolver, _ := pluginconf.Resolve(cfg, reg.Origin)
	s := connectWith(t, reg, Options{Origin: reg.Origin, Config: resolver.For, Profiles: cfg})
	return func(args map[string]any) (string, bool) {
		res := callTool(t, s, "db_query", args)
		return contentText(t, res), res.IsError
	}, ran, cfg
}

// A required input a named profile fills is not refused before the profile is
// read. The profile is resolved after consent, on purpose, so the check made
// before the gate saw no database and answered that the call needed one — a
// call that, with the profile laid on, had it. Refused there, the one way an
// agent reaches a connection the operator configured for it was closed on
// every capability whose connection input is required.
func TestARequiredInputAProfileFillsIsNotRefusedBeforeTheProfile(t *testing.T) {
	call, ran, cfg := requiredServer(t, `
plugins:
  db:
    host: db.internal
profiles:
  stg:
    plugins:
      db:
        set:
          database: app
`)
	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: "db", Profile: "stg", ProfilePin: profile.ConnStampFor(cfg, "stg", "db"),
		Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	body, isErr := call(map[string]any{"profile": "stg"})
	if isErr || !*ran || !strings.Contains(body, "database=app") {
		t.Fatalf("a call whose profile fills the required database: %s", body)
	}
}

// A required input nothing filled is refused before the handler, as a refusal:
// the ledger says refused and the use is not spent. The one the bridge could
// not check sooner is a Local input, which only the operator can supply — so
// the refusal says that, and does not tell the agent to pass what its schema
// hides.
func TestARequiredInputOnlyTheOperatorGivesIsRefusedBeforeTheHandler(t *testing.T) {
	call, ran, _ := requiredServer(t, "{}\n")
	body, isErr := call(map[string]any{"database": "app"})
	if !isErr || *ran {
		t.Fatalf("the handler ran without its host: %s", body)
	}
	for _, want := range []string{"core.input.missing", "db.query needs host", "only the operator", "rta config"} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not say %q: %s", want, body)
		}
	}
	if strings.Contains(body, `pass "host"`) {
		t.Errorf("an agent was told to pass a Local input: %s", body)
	}
	entries, err := agentlog.Read(1)
	if err != nil || len(entries) != 1 || entries[0].Outcome != agentlog.Refused ||
		entries[0].Code != "core.input.missing" {
		t.Errorf("the ledger recorded %+v (%v), want a core.input.missing refusal", entries, err)
	}
}

// A required argument left out, or sent with nothing in it, is refused with
// the code and the words every other surface uses, naming the argument.
func TestAMissingOrEmptyRequiredArgumentIsCoreInputMissing(t *testing.T) {
	call, ran, _ := requiredServer(t, "plugins:\n  db:\n    host: db.internal\n")
	for _, args := range []map[string]any{{}, {"database": ""}} {
		body, isErr := call(args)
		if !isErr || *ran {
			t.Fatalf("%v: the handler ran without its database: %s", args, body)
		}
		for _, want := range []string{"core.input.missing", `db.query needs the argument \"database\"`,
			`pass \"database\" in the arguments`} {
			if !strings.Contains(body, want) {
				t.Errorf("%v: the refusal does not say %q: %s", args, want, body)
			}
		}
	}
	if body, isErr := call(map[string]any{"database": "app"}); isErr || !*ran {
		t.Errorf("a complete call was refused: %s", body)
	}
}
