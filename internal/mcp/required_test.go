package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// requiredServer is a server over one capability, db.query, whose inputs
// arrive from three places: database, required, from the caller, the config
// or a profile; host, required too, from the operator alone; and sql, which
// is not, from the caller. ran reports whether the handler ever ran, and s is
// the session, for what it lists.
func requiredServer(t *testing.T, yaml string) (
	call func(map[string]any) (string, bool), ran *bool, cfg config.Config, s *sdk.ClientSession) {
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
	s = connectWith(t, reg, Options{Origin: reg.Origin, Config: resolver.For, Profiles: cfg})
	return func(args map[string]any) (string, bool) {
		res := callTool(t, s, "db_query", args)
		return contentText(t, res), res.IsError
	}, ran, cfg, s
}

// requiredOf is the "required" list tools/list publishes for db_query.
func requiredOf(t *testing.T, s *sdk.ClientSession) []string {
	t.Helper()
	raw, err := json.Marshal(listTools(t, s)["db_query"].InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	return schema.Required
}

// The schema's "required" list is the one the call is held to. It listed every
// Required input, whatever the operator's config gave it, so a client that
// validates arguments against the schema refused to send db_query {} to a
// server whose config names the database — a call rta runs. What a profile
// fills stays required: which profile a call names is the call's to say, and
// the list is one list for every call, whichever profile it names and whether
// that profile sets the input at all.
func TestAnInputTheConfigFillsIsNotRequiredOfAnAgent(t *testing.T) {
	call, ran, _, s := requiredServer(t, "plugins:\n  db:\n    host: db.internal\n    database: app\n")
	if got := requiredOf(t, s); len(got) != 0 {
		t.Errorf("required = %v, want none: the config gives the database", got)
	}
	if body, isErr := call(map[string]any{}); isErr || !*ran || !strings.Contains(body, "database=app") {
		t.Fatalf("a call leaving out the database the config gives: %s", body)
	}

	for name, tc := range map[string]struct{ yaml, refusal string }{
		"not in the config": {"plugins:\n  db:\n    host: db.internal\n", "core.input.missing"},
		// Empty is nothing given, to the call as to the list.
		"empty in the config": {"plugins:\n  db:\n    host: db.internal\n    database: \"\"\n", "core.input.missing"},
		// A call naming no profile is refused sooner here, since the
		// namespace has one; what stays required is required of a call
		// naming stg as much as of one naming a profile setting nothing.
		"only a profile's": {"plugins:\n  db:\n    host: db.internal\n" +
			"profiles:\n  stg:\n    plugins:\n      db:\n        set:\n          database: app\n",
			"core.profile.required"},
	} {
		call, ran, _, s := requiredServer(t, tc.yaml)
		if got := requiredOf(t, s); !slices.Equal(got, []string{"database"}) {
			t.Errorf("%s: required = %v, want [database]", name, got)
		}
		if body, isErr := call(map[string]any{}); !isErr || *ran || !strings.Contains(body, tc.refusal) {
			t.Errorf("%s: a call leaving out the database was not refused as %s: %s", name, tc.refusal, body)
		}
	}
}

// A required input a named profile fills is not refused before the profile is
// read. The profile is resolved after consent, on purpose, so the check made
// before the gate saw no database and answered that the call needed one — a
// call that, with the profile laid on, had it. Refused there, the one way an
// agent reaches a connection the operator configured for it was closed on
// every capability whose connection input is required.
func TestARequiredInputAProfileFillsIsNotRefusedBeforeTheProfile(t *testing.T) {
	call, ran, cfg, _ := requiredServer(t, `
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
	call, ran, _, _ := requiredServer(t, "{}\n")
	body, isErr := call(map[string]any{"database": "app"})
	if !isErr || *ran {
		t.Fatalf("the handler ran without its host: %s", body)
	}
	for _, want := range []string{"core.input.missing", "the `db_query` tool needs host", "only the operator", "rta config"} {
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
	call, ran, _, _ := requiredServer(t, "plugins:\n  db:\n    host: db.internal\n")
	for _, args := range []map[string]any{{}, {"database": ""}} {
		body, isErr := call(args)
		if !isErr || *ran {
			t.Fatalf("%v: the handler ran without its database: %s", args, body)
		}
		for _, want := range []string{"core.input.missing", "the `db_query` tool needs the \\\"database\\\" argument",
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
