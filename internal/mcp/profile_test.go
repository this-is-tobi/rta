package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/pluginconf"
	"github.com/this-is-tobi/rule-them-all/internal/profile"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// profileFixture builds a registry and an Options shaped like a real install
// with two configured connections, and reports what the handler saw.
type profileFixture struct {
	session *sdk.ClientSession
	sawHost *string
	sawPass *string
	sawSQL  *string
	// ranOther and ranGated record whether the unprofiled capabilities in the
	// second namespace actually ran.
	ranOther *bool
	ranGated *bool
	profiles config.Config
	// dir is the data directory this fixture ran against, so a test can stand
	// a second server over the same grants with a different config — which is
	// what "somebody edited the environment" actually looks like.
	dir string
}

func newProfileFixture(t *testing.T, yaml string) *profileFixture {
	t.Helper()
	return newProfileFixtureIn(t, yaml, t.TempDir())
}

func newProfileFixtureIn(t *testing.T, yaml, dir string) *profileFixture {
	t.Helper()
	var host, pass, sql string

	c := plugin.Capability{
		ID: "pg.query", Summary: "run a query", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Default: "localhost", Config: "host", Local: true},
			{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true},
			{Name: "sql", Type: plugin.String},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			host, pass, sql = req.String("host"), req.String("password"), req.String("sql")
			return view.Text{Body: "ok"}, nil
		},
	}
	// A second namespace, with no profile anywhere in any fixture config, so
	// that "the operator has an environment switched on and the agent calls
	// something unprofiled" is reachable at all. Its absence is exactly why the
	// bound could black out the entire tool surface with every test green: every
	// config carrying a selection also declared a pg profile, so R5 refused an
	// unprofiled pg call before the bound was ever consulted.
	var ran, ranGated bool
	other := plugin.Capability{
		ID: "sys.disk", Summary: "disks", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) {
			ran = true
			return view.Text{Body: "ok"}, nil
		},
	}
	// And a gated one in the same namespace, so the filter itself is reachable:
	// an ungated unprofiled call never enters Reserve at all (Required is
	// false), which makes it the wrong probe for whether a base grant survives
	// a switch.
	gated := plugin.Capability{
		ID: "sys.gated", Summary: "gated", Safety: plugin.Read, NeedsGrant: true,
		Run: func(context.Context, plugin.Request) (view.View, error) {
			ranGated = true
			return view.Text{Body: "ok"}, nil
		},
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "pg", Summary: "pg", Capabilities: []plugin.Capability{c},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(plugin.Plugin{
		Name: "sys", Summary: "sys", Capabilities: []plugin.Capability{other, gated},
	}); err != nil {
		t.Fatal(err)
	}

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

	resolver, _ := pluginconf.Resolve(cfg, reg.Origin)
	server := NewServer(reg, "test", Options{
		Origin: reg.Origin, Config: resolver.For, Profiles: cfg,
		// Wired the way internal/app wires it, so these tests exercise what a
		// real server does rather than the zero value's fallback.
		Reload: func() config.Config {
			live, err := config.Load()
			if err != nil {
				return config.Config{}
			}
			return live
		},
	})
	st, ct := sdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "agent", Version: "0"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return &profileFixture{session: session, sawHost: &host, sawPass: &pass, sawSQL: &sql,
		ranOther: &ran, ranGated: &ranGated, profiles: cfg, dir: dir}
}

// pin is the stamp of one of this fixture's connections, which is what a grant
// naming that profile has to carry.
//
// Spelled out in every test that issues a profiled grant rather than defaulted
// away, because the pin is the point: a grant that does not carry the
// connection's fingerprint is refused, and a fixture that quietly filled it in
// would make every one of these tests pass against a gate that was not there.
func (f *profileFixture) pin(name, namespace string) string {
	return profile.ConnStampFor(f.profiles, name, namespace)
}

// callGated invokes the grant-gated capability in that same namespace.
func (f *profileFixture) callGated(t *testing.T) *sdk.CallToolResult {
	t.Helper()
	res, err := f.session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "sys_gated", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// callOther invokes the capability in the namespace no profile mentions.
func (f *profileFixture) callOther(t *testing.T) *sdk.CallToolResult {
	t.Helper()
	res, err := f.session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "sys_disk", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (f *profileFixture) call(t *testing.T, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := f.session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "pg_query", Arguments: args,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (f *profileFixture) schema(t *testing.T) string {
	t.Helper()
	tools, err := f.session.ListTools(context.Background(), &sdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "pg_query" {
			raw, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			return string(raw)
		}
	}
	t.Fatal("pg_query was not exposed")
	return ""
}

const twoProfiles = `
plugins:
  pg:
    host: base.internal
profiles:
  staging:
    plugins:
      pg:
        set:
          host: staging.internal
  prod:
    plugins:
      pg:
        set:
          host: prod.internal
`

// An agent may name a connection, and naming one it has no grant for is
// refused — with no grant needed for anything else about the call.
//
// This is the feature in one test: pg.query is Safety Read with no NeedsGrant,
// so before profiles there was no way to gate any of it.
func TestAnAgentNeedsAGrantForEveryConnectionItNames(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)

	res := f.call(t, map[string]any{"profile": "staging", "sql": "select 1"})
	// The CODE, not merely IsError. An earlier version of this asserted only
	// that the call failed, and it passed while the argument was being refused
	// as unknown — a green test proving the gate worked, against a gate the
	// call never reached.
	assertCode(t, res, "core.grant.required")
	if *f.sawHost != "" {
		t.Errorf("the handler ran anyway, against %q", *f.sawHost)
	}

	// With consent, the same call works and reaches the named connection.
	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: "pg", Profile: "staging", ProfilePin: f.pin("staging", "pg"), Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	res = f.call(t, map[string]any{"profile": "staging", "sql": "select 1"})
	if res.IsError {
		t.Fatalf("a granted profile was refused: %s", contentText(t, res))
	}
	if *f.sawHost != "staging.internal" {
		t.Errorf("host = %q, want the profile's own host", *f.sawHost)
	}
	if *f.sawSQL != "select 1" {
		t.Errorf("sql = %q — the payload input stopped arriving", *f.sawSQL)
	}
}

// A grant for one connection does not open another.
func TestAGrantForOneProfileDoesNotOpenAnother(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)
	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: "pg", Profile: "staging", ProfilePin: f.pin("staging", "pg"), Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	assertCode(t, f.call(t, map[string]any{"profile": "prod", "sql": "select 1"}), "core.grant.required")
	if *f.sawHost != "" {
		t.Errorf("the handler ran against %q on a grant for a different connection", *f.sawHost)
	}
}

// While the operator is working in one environment, agents are in that
// environment and nowhere else.
//
// This is the half of `rta use` that is a security control rather than a
// convenience, and its direction is the whole argument for reading session
// state on this surface at all: it can only *subtract*. The grant stays issued,
// the agent still names the profile, the call is still filled from the name it
// gave — the switch only refuses. Something that can only refuse cannot make a
// person's consent and the connection actually touched disagree, which is what
// an expanding session input would do.
func TestWhileAnEnvironmentIsOnAgentsMayReachNoOther(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)
	now := time.Now()
	if verr := grant.Save([]grant.Grant{
		{Target: "pg", Profile: "staging", ProfilePin: f.pin("staging", "pg"), Issued: now, Expires: now.Add(time.Hour)},
		{Target: "pg", Profile: "prod", ProfilePin: f.pin("prod", "pg"), Issued: now, Expires: now.Add(time.Hour)},
	}); verr != nil {
		t.Fatal(verr)
	}

	// Nothing switched on: the grants alone decide, exactly as before.
	if res := f.call(t, map[string]any{"profile": "prod", "sql": "select 1"}); res.IsError {
		t.Fatalf("a granted profile was refused with nothing switched on: %s", contentText(t, res))
	}

	// Switched on to staging: prod is refused despite its grant.
	if verr := profile.SaveSelection(profile.Selection{Active: "staging"}); verr != nil {
		t.Fatal(verr)
	}
	*f.sawHost = ""
	assertCode(t, f.call(t, map[string]any{"profile": "prod", "sql": "select 1"}), "core.grant.required")
	if *f.sawHost != "" {
		t.Errorf("the handler ran against %q while another environment was switched on", *f.sawHost)
	}
	// …and staging still works, so this narrows rather than closes.
	if res := f.call(t, map[string]any{"profile": "staging", "sql": "select 1"}); res.IsError {
		t.Fatalf("the switched-on environment was refused: %s", contentText(t, res))
	}
	if *f.sawHost != "staging.internal" {
		t.Errorf("host = %q, want staging's", *f.sawHost)
	}

	// It cannot grant: a profile nobody granted stays refused while it is on.
	if verr := grant.Save(nil); verr != nil {
		t.Fatal(verr)
	}
	*f.sawHost = ""
	assertCode(t, f.call(t, map[string]any{"profile": "staging", "sql": "select 1"}), "core.grant.required")
	if *f.sawHost != "" {
		t.Error("switching an environment on authorized a call no grant covered")
	}
}

// The bound refuses in the same words as everything else, so it is not an
// oracle for what the operator happens to be working on.
func TestTheActiveBoundRefusesInTheSameWords(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)
	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: "pg", Profile: "prod", ProfilePin: f.pin("prod", "pg"), Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	ungrantedText := contentText(t, f.call(t, map[string]any{"profile": "staging", "sql": "select 1"}))

	if verr := profile.SaveSelection(profile.Selection{Active: "staging"}); verr != nil {
		t.Fatal(verr)
	}
	bounded := contentText(t, f.call(t, map[string]any{"profile": "prod", "sql": "select 1"}))

	if strings.ReplaceAll(bounded, "prod", "X") != strings.ReplaceAll(ungrantedText, "staging", "X") {
		t.Errorf("a bounded refusal reads differently from an ungranted one, so an agent can "+
			"read which environment the operator is in off it:\n  bounded:   %s\n  ungranted: %s",
			bounded, ungrantedText)
	}
}

// A lapsed switch bounds nothing: the deadline has to be enforced where the
// value is read, or an environment stays in force for as long as the server
// happens to keep running.
func TestALapsedSwitchStopsBounding(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)
	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: "pg", Profile: "prod", ProfilePin: f.pin("prod", "pg"), Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	past := now.Add(-time.Minute)
	if verr := profile.SaveSelection(profile.Selection{Active: "staging", Until: &past}); verr != nil {
		t.Fatal(verr)
	}
	if res := f.call(t, map[string]any{"profile": "prod", "sql": "select 1"}); res.IsError {
		t.Fatalf("a lapsed switch was still bounding: %s", contentText(t, res))
	}
}

// An unknown profile and an ungranted one are indistinguishable to an agent.
//
// Otherwise the refusal is an oracle: ask for a name, learn from the wording
// whether the operator has a connection by that name, and enumerate their
// whole inventory one call at a time. The gate runs before the lookup, which
// is what makes the two answers identical rather than merely similar.
func TestAnUnknownProfileAndAnUngrantedOneLookTheSame(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)

	real := f.call(t, map[string]any{"profile": "prod", "sql": "select 1"})
	fake := f.call(t, map[string]any{"profile": "does-not-exist", "sql": "select 1"})
	if !real.IsError || !fake.IsError {
		t.Fatal("one of these was not refused at all")
	}
	realText := strings.ReplaceAll(contentText(t, real), "prod", "X")
	fakeText := strings.ReplaceAll(contentText(t, fake), "does-not-exist", "X")
	if realText != fakeText {
		t.Errorf("the refusals differ once the name is masked, so an agent can tell a real\n"+
			"connection from an invented one:\n  configured: %s\n  invented:   %s", realText, fakeText)
	}
}

// The operator's connection names are never listed to an agent.
func TestTheSchemaNeverListsTheOperatorsConnections(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)
	schema := f.schema(t)
	if !strings.Contains(schema, `"profile"`) {
		t.Fatalf("the profile property is not offered at all:\n%s", schema)
	}
	for _, name := range []string{"staging", "prod", "staging.internal", "prod.internal"} {
		if strings.Contains(schema, name) {
			t.Errorf("the schema hands an ungranted agent the operator's connection inventory (%q):\n%s",
				name, schema)
		}
	}
	// And the connection inputs themselves stay gone — D94 is untouched.
	for _, gone := range []string{`"host"`, `"password"`} {
		if strings.Contains(schema, gone) {
			t.Errorf("the schema advertises %s again:\n%s", gone, schema)
		}
	}
}

// With no profiles configured, the surface is exactly what it was before.
func TestWithNoProfilesTheSurfaceIsUnchanged(t *testing.T) {
	f := newProfileFixture(t, "plugins:\n  pg:\n    host: base.internal\n")
	if schema := f.schema(t); strings.Contains(schema, `"profile"`) {
		t.Errorf("a profile property is offered on an install that has none:\n%s", schema)
	}
	// And an unprofiled call still runs with no grant — R5 costs nothing until
	// an operator opts in by writing a profile.
	if res := f.call(t, map[string]any{"sql": "select 1"}); res.IsError {
		t.Fatalf("an ordinary read was refused on an install with no profiles: %s", contentText(t, res))
	}
	if *f.sawHost != "base.internal" {
		t.Errorf("host = %q, want the configured base connection", *f.sawHost)
	}
}

// Once a namespace has profiles, an MCP call that names none is refused (R5).
//
// Without this the feature is one-sided: an operator who carefully grants
// "staging" leaves production sitting in plugins.pg:, reachable by any agent
// with no grant at all, and adopting profiles would have made their posture no
// better than before.
func TestOnceAProfileExistsAnUnprofiledAgentCallIsRefused(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)
	assertCode(t, f.call(t, map[string]any{"sql": "select 1"}), "core.profile.required")
	if *f.sawHost != "" {
		t.Errorf("the handler ran against the base connection %q", *f.sawHost)
	}
}

// A profile switches the namespace-wide credential variable off entirely.
//
// RTA_PG_PASSWORD is bound to the plugin, so it follows the connection
// wherever a profile points it. Pairing a destination somebody else named with
// a credential the operator exported for their own database is D94's redirect
// rebuilt one layer up — so under a profile the whole layer is skipped, and
// the profile carries its own credential or none.
func TestAProfileNeverInheritsTheNamespaceWideCredential(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)
	// The operator's own credential, exported for their own database, exactly
	// as `rta mcp serve` inherits it.
	t.Setenv(plugin.LocalEnvVar("pg.query", "password"), "operator-production-password")
	// staging deliberately supplies NO credential of its own. That is the case
	// that matters: where the profile also sets a password, the profile layer
	// sits above the environment layer and wins regardless, so a test that sets
	// both passes whether or not the environment layer was skipped at all.
	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: "pg", Profile: "staging", ProfilePin: f.pin("staging", "pg"), Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	if res := f.call(t, map[string]any{"profile": "staging", "sql": "select 1"}); res.IsError {
		t.Fatalf("the granted call failed: %s", contentText(t, res))
	}
	if *f.sawHost != "staging.internal" {
		t.Fatalf("host = %q — the call did not reach the profile at all", *f.sawHost)
	}
	if *f.sawPass == "operator-production-password" {
		t.Errorf("the operator's credential for their own database was sent to %q, "+
			"a destination the profile chose", *f.sawHost)
	}
	if *f.sawPass != "" {
		t.Errorf("password = %q, want empty — a profile that supplies no credential gets none",
			*f.sawPass)
	}

	// And a profile that does supply one gets exactly that one.
	t.Setenv(plugin.ProfileEnvVar("staging", "password"), "staging-only-password")
	if res := f.call(t, map[string]any{"profile": "staging", "sql": "select 1"}); res.IsError {
		t.Fatalf("the granted call failed: %s", contentText(t, res))
	}
	if *f.sawPass != "staging-only-password" {
		t.Errorf("password = %q, want the profile's own credential", *f.sawPass)
	}
}

// assertCode fails unless the call was refused with exactly this code.
//
// Asserting on IsError alone is how a test about a gate ends up green about a
// typo: any refusal, for any reason, satisfies it.
func assertCode(t *testing.T, res *sdk.CallToolResult, want string) {
	t.Helper()
	body := contentText(t, res)
	if !res.IsError {
		t.Fatalf("the call succeeded; wanted it refused with %s", want)
	}
	var e struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("refusal is not a view error envelope: %s", body)
	}
	if e.Code != want {
		t.Fatalf("refused with %q, want %q — the call did not reach the gate under test:\n%s",
			e.Code, want, body)
	}
}

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}

func contentText(t *testing.T, res *sdk.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// A switch bounds the profiles an agent may name. It bounds nothing else.
//
// This is the test that was missing, and its absence is the whole reason the
// first version of the bound shipped broken. It compared the call's profile
// against the active one without excluding the empty profile, so switching
// *anything* on refused every call that named no profile — which is most of the
// catalogue — and handed the operator a hint telling them to issue grants that
// could not possibly help. Every test was green: the fixture had one namespace,
// every config carrying a selection declared a profile for it, and R5 refused an
// unprofiled call before the bound was ever consulted.
//
// So this uses a namespace no profile anywhere mentions, which is the shape of
// almost every capability rta has.
func TestASwitchDoesNotBoundCallsThatNameNoProfile(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)

	if res := f.callOther(t); res.IsError {
		t.Fatalf("an unprofiled call was refused with nothing switched on: %s", contentText(t, res))
	}
	*f.ranOther = false

	if verr := profile.SaveSelection(profile.Selection{Active: "staging"}); verr != nil {
		t.Fatal(verr)
	}
	if res := f.callOther(t); res.IsError {
		t.Fatalf("switching an environment on refused a call that names no profile — "+
			"the whole tool surface goes dark: %s", contentText(t, res))
	}
	if !*f.ranOther {
		t.Error("the handler never ran")
	}

	// And the gated half, which is where the filter itself is reachable: a grant
	// for the base configuration must survive a switch. Dropping it would mean
	// that switching to an environment silently revoked every permission the
	// operator issued for everywhere else.
	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: "sys.gated", Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	if res := f.callGated(t); res.IsError {
		t.Fatalf("switching an environment on dropped a grant for the base configuration: %s",
			contentText(t, res))
	}
	if !*f.ranGated {
		t.Error("the gated handler never ran")
	}
}

// The bound narrows what a grant covers; it does not produce a second kind of
// refusal an agent could tell apart.
//
// A partially-covering grant is where the two would diverge: the ordinary check
// names only the scopes nothing covers, so a bound that refused the whole call
// with every scope listed would let an agent read "the operator is working
// somewhere else" off the difference. Passing the switch into the gate rather
// than checking beside it makes one sentence by construction.
func TestTheBoundProducesTheGateSOwnRefusal(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)
	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: "pg", Profile: "prod", ProfilePin: f.pin("prod", "pg"), Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	// No grant at all for staging, nothing switched on.
	ungrantedText := contentText(t, f.call(t, map[string]any{"profile": "staging", "sql": "select 1"}))

	// A real grant for prod, but the operator is in staging.
	if verr := profile.SaveSelection(profile.Selection{Active: "staging"}); verr != nil {
		t.Fatal(verr)
	}
	bounded := contentText(t, f.call(t, map[string]any{"profile": "prod", "sql": "select 1"}))

	if strings.ReplaceAll(bounded, "prod", "X") != strings.ReplaceAll(ungrantedText, "staging", "X") {
		t.Errorf("a bounded refusal reads differently from an ungranted one, so an agent can "+
			"read which environment the operator is in off it:\n  bounded:   %s\n  ungranted: %s",
			bounded, ungrantedText)
	}
}

// The whole control, through the real gate: consent is to a connection.
//
// An operator allows an agent to reach `staging`, which points at
// staging.internal. Somebody then edits that environment to point at
// production — a one-line change to a config file, the kind of thing done
// while tidying up or copying a block between environments. Before the pin,
// the grant followed the name: the same live grant, still reading "staging"
// in `rta grant list`, now authorized the identical call against production,
// and the handler ran with production's host and production's credential.
//
// Driven end to end rather than against Check, because that is the surface
// where nobody is watching. The assertion that matters is not only the
// refusal — it is that the handler never ran.
func TestRepointingAnEnvironmentStopsTheGrantThatNamedIt(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)

	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: "pg", Profile: "staging", ProfilePin: f.pin("staging", "pg"),
		Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	res := f.call(t, map[string]any{"profile": "staging", "sql": "select 1"})
	if res.IsError {
		t.Fatalf("the connection that was consented to was refused: %s", contentText(t, res))
	}
	if *f.sawHost != "staging.internal" {
		t.Fatalf("host = %q, want the connection the grant was issued against", *f.sawHost)
	}

	// The edit. A second fixture over the same data directory, so the grant
	// issued above is still on disk exactly as it was — which is the point:
	// nothing about the grant changed, only what its name now means.
	*f.sawHost = ""
	repointed := newProfileFixtureIn(t, `
plugins:
  pg:
    host: base.internal
profiles:
  staging:
    plugins:
      pg:
        set:
          host: prod.internal
`, f.dir)
	res = repointed.call(t, map[string]any{"profile": "staging", "sql": "select 1"})
	if !res.IsError {
		t.Fatalf("the grant followed the name to a different connection; the handler saw %q",
			*repointed.sawHost)
	}
	if *repointed.sawHost != "" {
		t.Errorf("the handler ran against %q", *repointed.sawHost)
	}
	assertCode(t, res, "core.grant.required")

	// Re-consenting to the connection as it now is restores the call — and it
	// is `grant allow`, a fresh decision, not `grant renew`, which moves the
	// deadline and deliberately leaves the pin alone.
	if verr := grant.Save([]grant.Grant{{
		Target: "pg", Profile: "staging", ProfilePin: repointed.pin("staging", "pg"),
		Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	if res = repointed.call(t, map[string]any{"profile": "staging", "sql": "select 1"}); res.IsError {
		t.Fatalf("re-consent did not restore the call: %s", contentText(t, res))
	}
	if *repointed.sawHost != "prod.internal" {
		t.Errorf("host = %q, want the connection just consented to", *repointed.sawHost)
	}
}

// Re-issuing a grant after editing an environment has to work.
//
// The gate compares a grant's pin against the connection this server would
// resolve, and `rta grant allow` stamps from the config file. While the server
// held a startup snapshot the two could be put permanently beyond reach of
// each other: edit a profile, follow the remedy `rta doctor` prints, and every
// call is still refused with the sentence an ungranted call gets — while
// `grant list` and `doctor`, which also read the file, report the grant
// healthy. The operator has done the documented thing, watched it not work,
// and there is nowhere for them to learn why.
//
// Resolving from the file is what closes it, and it is what every other input
// to the same decision already does: grants are loaded per call, and so is the
// operator's switched-on environment.
func TestAGrantIssuedAfterAnEditWorksWithoutRestartingTheServer(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)

	// Consent to staging as it stands, and confirm it reaches staging.
	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: "pg", Profile: "staging", ProfilePin: f.pin("staging", "pg"),
		Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	if res := f.call(t, map[string]any{"profile": "staging", "sql": "select 1"}); res.IsError {
		t.Fatalf("the connection consented to was refused: %s", contentText(t, res))
	}

	// The operator edits the environment. The server keeps running — nothing
	// about an editor, or the TUI profile form, restarts it.
	if err := writeFile(f.dir+"/config.yaml", `
plugins:
  pg:
    host: base.internal
profiles:
  staging:
    plugins:
      pg:
        set:
          host: edited.internal
`); err != nil {
		t.Fatal(err)
	}

	// The grant now names a connection that is not the one a call would reach,
	// so it is refused — that is the control working.
	*f.sawHost = ""
	if res := f.call(t, map[string]any{"profile": "staging", "sql": "select 1"}); !res.IsError {
		t.Fatalf("a repointed environment was still reached: host=%q", *f.sawHost)
	}
	if *f.sawHost != "" {
		t.Errorf("the handler ran against %q", *f.sawHost)
	}

	// `rta grant allow` re-consents, stamping from the file. The running
	// server must agree with it.
	fresh, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	reissued := profile.ConnStampFor(fresh, "staging", "pg")
	if verr := grant.Save([]grant.Grant{{
		Target: "pg", Profile: "staging", ProfilePin: reissued,
		Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	res := f.call(t, map[string]any{"profile": "staging", "sql": "select 1"})
	if res.IsError {
		t.Fatalf("a grant re-issued against the edited connection was refused, so the "+
			"documented remedy is a no-op: %s", contentText(t, res))
	}
	if *f.sawHost != "edited.internal" {
		t.Errorf("host = %q, want the connection just consented to", *f.sawHost)
	}

	// And the person-facing surfaces agree with the gate, which is what makes
	// `(changed)` and doctor's warning trustworthy rather than advisory.
	loaded, verr := grant.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if loaded[0].Stale(profile.ConnStampFor(fresh, "staging", "pg")) {
		t.Error("`rta grant list` would mark a grant the server honours as (changed)")
	}
}
