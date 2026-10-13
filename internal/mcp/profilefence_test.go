package mcp

import (
	"context"
	"encoding/json"
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

// A grant says an agent may reach a connection. It does not say every
// namespace the connection can see, and `set:` could not say it: it supplies a
// default and the caller wins. These tests drive a profile with an `only:`
// fence through the real bridge, grant gate and ledger, with a plugin shaped
// like cnpg — a scope an agent may name, and the switch that reads past it.

type fenceFixture struct {
	session  *sdk.ClientSession
	profiles config.Config
	dir      string
	ran      *int
	sawNS    *string
	sawAll   *bool
}

func newFenceFixture(t *testing.T, yaml, dir string) *fenceFixture {
	t.Helper()
	return newScopedFenceFixture(t, yaml, dir, "")
}

// newScopedFenceFixture is the fixture with the capability's Scope set: the
// input a grant is checked against, which no profile fills. Shaped like kube —
// a plugin where the namespace is the scope of its reads, and an input a profile
// may fill for the one capability that provisions in it — because a key that
// every reader takes as its scope is a profile the loader refuses outright.
func newScopedFenceFixture(t *testing.T, yaml, dir, scope string) *fenceFixture {
	t.Helper()
	var ran int
	var ns string
	var all bool
	caps := []plugin.Capability{{
		ID: "app.status", Summary: "status", Safety: plugin.Read, Scope: scope,
		Inputs: []plugin.Field{
			{Name: "namespace", Type: plugin.String, Config: "namespace"},
			{Name: "all-namespaces", Type: plugin.Bool, Config: "all-namespaces", Widens: "namespace"},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			ran++
			ns, all = req.String("namespace"), req.Bool("all-namespaces")
			return view.Text{Body: "ok"}, nil
		},
	}}
	if scope != "" {
		caps = append(caps, plugin.Capability{
			ID: "app.provision", Summary: "provision", Safety: plugin.Write, Scope: "name",
			Inputs: []plugin.Field{
				{Name: "name", Type: plugin.String, Positional: true, Required: true},
				{Name: "namespace", Type: plugin.String, Config: "namespace", Local: true},
			},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		})
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "app", Summary: "app", Capabilities: caps}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_DATA_DIR", dir)
	path := dir + "/config.yaml"
	if err := writeFile(path, yaml); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", path)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	resolver, _ := pluginconf.Resolve(cfg, reg.Origin)
	server := NewServer(reg, "test", Options{
		Origin: reg.Origin, Config: resolver.For, Profiles: cfg, Reload: config.Load,
	})
	st, ct := sdk.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "agent", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return &fenceFixture{session: session, profiles: cfg, dir: dir, ran: &ran, sawNS: &ns, sawAll: &all}
}

// messageOf is the sentence of a refusal, decoded: contentText is the JSON
// envelope, in which every quotation mark of the sentence is escaped.
func messageOf(t *testing.T, res *sdk.CallToolResult) string {
	t.Helper()
	var e struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(contentText(t, res)), &e); err != nil {
		t.Fatalf("not a refusal envelope: %v", err)
	}
	return e.Message
}

func (f *fenceFixture) call(t *testing.T, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := f.session.CallTool(context.Background(), &sdk.CallToolParams{Name: "app_status", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (f *fenceFixture) grantFor(t *testing.T, name string, maxUses int) {
	t.Helper()
	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: "app", Profile: name, ProfilePin: profile.ConnStampFor(f.profiles, name, "app"),
		Issued: now, Expires: now.Add(time.Hour), MaxUses: maxUses,
	}}); verr != nil {
		t.Fatal(verr)
	}
}

const fenced = `
profiles:
  staging:
    plugins:
      app:
        set:
          namespace: staging
        only:
          namespace: [staging, staging-jobs]
  prod:
    plugins:
      app:
        set:
          namespace: prod
        only:
          namespace: [prod]
  shared:
    plugins:
      app:
        only:
          namespace: [alpha, beta]
  pinned:
    plugins:
      app:
        only:
          namespace: [only-this]
`

// **The hole this closes**: an agent granted `staging` named any namespace the
// connection could see — including the one `prod` pins in the same config — and
// the host passed it through, because a profile's `set:` is a default and the
// caller wins. Refused before the handler runs, recorded with a code of its
// own, and without telling the agent what the connection does reach.
func TestAnAgentGrantedOneProfileCannotNameAnotherValueOfAFencedInput(t *testing.T) {
	f := newFenceFixture(t, fenced, t.TempDir())
	f.grantFor(t, "staging", 0)

	res := f.call(t, map[string]any{"profile": "staging", "namespace": "prod"})
	assertCode(t, res, "core.profile.fenced")
	if *f.ran != 0 {
		t.Fatalf("the handler ran with namespace %q on a connection fenced to staging", *f.sawNS)
	}

	// The agent is told which input and nothing the operator wrote: neither the
	// list, nor the profile it named, nor the values a sibling pins.
	text := contentText(t, res)
	for _, leaked := range []string{"staging", "staging-jobs", "alpha", "only-this"} {
		if strings.Contains(text, leaked) {
			t.Errorf("the refusal tells the agent %q:\n%s", leaked, text)
		}
	}
	if message := messageOf(t, res); !strings.Contains(message, `the "namespace" argument`) {
		t.Errorf("the refusal does not say which input went outside: %q", message)
	}
	// The way across that costs a terminal's user nothing is not one an agent
	// has: --no-profile is on no tool, so a refusal that offered it would name a
	// door that is not there.
	if strings.Contains(text, "no-profile") {
		t.Errorf("the refusal offers an agent a flag it cannot send:\n%s", text)
	}

	// And the row is what lets an operator see an agent probing a boundary: the
	// code, the sentence the agent heard, and in the note what it was not told.
	entries, err := agentlog.Read(1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ledger = %v, %v", entries, err)
	}
	e := entries[0]
	if e.Outcome != agentlog.Refused || e.Code != "core.profile.fenced" || e.Profile != "staging" {
		t.Errorf("the row = outcome %q code %q profile %q", e.Outcome, e.Code, e.Profile)
	}
	if !strings.Contains(e.Note, "staging-jobs") {
		t.Errorf("the row does not carry what the connection reaches, for the operator: note %q", e.Note)
	}
	if strings.Contains(e.Reason, "staging-jobs") {
		t.Errorf("the reason column carries the list the agent must not see: %q", e.Reason)
	}
}

// Nothing about a call that stays inside the fence changes.
func TestACallInsideTheFenceBehavesAsItDid(t *testing.T) {
	f := newFenceFixture(t, fenced, t.TempDir())
	f.grantFor(t, "staging", 0)

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"profile": "staging"}, "staging"},
		{map[string]any{"profile": "staging", "namespace": "staging"}, "staging"},
		{map[string]any{"profile": "staging", "namespace": "staging-jobs"}, "staging-jobs"},
	} {
		if res := f.call(t, tc.args); res.IsError {
			t.Fatalf("%v was refused: %s", tc.args, contentText(t, res))
		}
		if *f.sawNS != tc.want {
			t.Errorf("%v ran with namespace %q, want %q", tc.args, *f.sawNS, tc.want)
		}
	}
}

// The empty string is a value, and the caller's layer beats the profile's — so
// a fence that checked what `set:` says would let a blank lift the pin.
func TestABlankCannotLiftAPinnedValue(t *testing.T) {
	f := newFenceFixture(t, fenced, t.TempDir())
	f.grantFor(t, "staging", 0)

	assertCode(t, f.call(t, map[string]any{"profile": "staging", "namespace": ""}), "core.profile.fenced")
	if *f.ran != 0 {
		t.Fatalf("a blank namespace reached the handler: %q", *f.sawNS)
	}
}

// **A blank is not a value outside the list, and the agent is not told it is.**
// Leaving a fenced input out is the natural call — the schema says the context's
// own is used — and "lies outside what the connection reaches" says something
// the agent did not do, with a way out ("a grant on another one") that is no
// answer to a blank. The two causes need different action: a blank asks which,
// an outside value is not covered. Nothing the operator wrote is in either.
func TestABlankFencedInputIsToldApartFromOneOutsideTheList(t *testing.T) {
	f := newFenceFixture(t, fenced, t.TempDir())
	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: "app", Profile: "shared", ProfilePin: profile.ConnStampFor(f.profiles, "shared", "app"),
		Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}

	for name, args := range map[string]map[string]any{
		"left out":   {"profile": "shared"},
		"sent blank": {"profile": "shared", "namespace": ""},
	} {
		t.Run(name, func(t *testing.T) {
			res := f.call(t, args)
			assertCode(t, res, "core.profile.fenced")
			message := messageOf(t, res)
			if !strings.Contains(message, `the "namespace" argument is blank`) ||
				!strings.Contains(message, "has to be told which one") {
				t.Errorf("a blank is not said as one: %q", message)
			}
			if strings.Contains(message, "lies outside") {
				t.Errorf("a blank is told it lies outside: %q", message)
			}
			text := contentText(t, res)
			for _, leaked := range []string{"alpha", "beta", "shared"} {
				if strings.Contains(text, leaked) {
					t.Errorf("the refusal tells the agent %q:\n%s", leaked, text)
				}
			}
			if strings.Contains(text, "grant on another") {
				t.Errorf("a blank is sent to ask for a grant on another connection:\n%s", text)
			}
			if !strings.Contains(text, "name a value for it") {
				t.Errorf("the refusal does not say what to do about a blank:\n%s", text)
			}
		})
	}

	// A value that is there and is not covered keeps its own sentence.
	res := f.call(t, map[string]any{"profile": "shared", "namespace": "gamma"})
	assertCode(t, res, "core.profile.fenced")
	if message := messageOf(t, res); !strings.Contains(message, "lies outside what the connection it names reaches") {
		t.Errorf("a value outside the list lost its sentence: %q", message)
	}
	if *f.ran != 0 {
		t.Fatal("a refused call reached the handler")
	}
}

// An input a call names as its scope is never filled from a profile, so a
// connection that states `set: namespace: staging` and fences it still refuses a
// call that leaves the namespace out. What the operator is told in the record is
// what happened — the connection states the value, a call has to name it — and
// what the agent is told is that the argument is blank, with neither the value
// the profile states nor the profile's name: the line is the operator's.
func TestABlankScopeUnderAStatedValueIsExplainedToTheOperatorAndNotToTheAgent(t *testing.T) {
	f := newScopedFenceFixture(t, fenced, t.TempDir(), "namespace")
	f.grantFor(t, "staging", 0)

	res := f.call(t, map[string]any{"profile": "staging"})
	assertCode(t, res, "core.profile.fenced")
	if *f.ran != 0 {
		t.Fatal("the call reached the handler with the scope blank")
	}
	text := contentText(t, res)
	if message := messageOf(t, res); !strings.Contains(message, `the "namespace" argument is blank`) {
		t.Errorf("the agent was not told the argument is blank: %q", message)
	}
	for _, leaked := range []string{"staging", "states", "staging-jobs"} {
		if strings.Contains(text, leaked) {
			t.Errorf("the refusal tells the agent %q:\n%s", leaked, text)
		}
	}

	entries, err := agentlog.Read(1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ledger = %v, %v", entries, err)
	}
	if note := entries[0].Note; !strings.Contains(note, `profile "staging" states namespace: staging, but a call has to name it`) {
		t.Errorf("the record does not say what happened: note %q", note)
	}
	if strings.Contains(entries[0].Reason, "states namespace") {
		t.Errorf("the reason column carries what the agent must not see: %q", entries[0].Reason)
	}

	// Naming one of the values is what the connection asks for.
	if res := f.call(t, map[string]any{"profile": "staging", "namespace": "staging-jobs"}); res.IsError {
		t.Errorf("a call that names a value the fence holds was refused: %s", contentText(t, res))
	}
}

// The switch the plugin declares widens the input is a second way past the
// list that never passes through the input's own value.
func TestAWideningSwitchCannotReadPastAFencedInput(t *testing.T) {
	f := newFenceFixture(t, fenced, t.TempDir())
	f.grantFor(t, "staging", 0)

	res := f.call(t, map[string]any{"profile": "staging", "all-namespaces": true})
	assertCode(t, res, "core.profile.fenced.switch")
	if *f.ran != 0 {
		t.Fatal("the switch reached the handler on a fenced connection")
	}
	if message := messageOf(t, res); !strings.Contains(message, `"all-namespaces"`) ||
		!strings.Contains(message, `"namespace"`) || strings.Contains(message, "staging") {
		t.Errorf("the refusal names the switch and the input and nothing else: %q", message)
	}
	// Off is the same statement as not saying it.
	if res := f.call(t, map[string]any{"profile": "staging", "all-namespaces": false}); res.IsError {
		t.Errorf("the switch left off was refused: %s", contentText(t, res))
	}
}

// A one-entry list is the whole of the input, and a longer one asks the caller
// to say which — a blank is not an answer.
func TestAFenceWithOneValueFillsTheInputAndWithSeveralAsksWhich(t *testing.T) {
	f := newFenceFixture(t, fenced, t.TempDir())
	now := time.Now()
	if verr := grant.Save([]grant.Grant{
		{Target: "app", Profile: "pinned", ProfilePin: profile.ConnStampFor(f.profiles, "pinned", "app"), Issued: now, Expires: now.Add(time.Hour)},
		{Target: "app", Profile: "shared", ProfilePin: profile.ConnStampFor(f.profiles, "shared", "app"), Issued: now, Expires: now.Add(time.Hour)},
	}); verr != nil {
		t.Fatal(verr)
	}

	if res := f.call(t, map[string]any{"profile": "pinned"}); res.IsError {
		t.Fatalf("a connection fenced to one value was refused: %s", contentText(t, res))
	}
	if *f.sawNS != "only-this" {
		t.Errorf("the input resolved to %q, want the single allowed value", *f.sawNS)
	}

	*f.ran = 0
	assertCode(t, f.call(t, map[string]any{"profile": "shared"}), "core.profile.fenced")
	if *f.ran != 0 {
		t.Fatal("a call that named none of two allowed values ran")
	}
	if res := f.call(t, map[string]any{"profile": "shared", "namespace": "beta"}); res.IsError {
		t.Fatalf("an allowed value was refused: %s", contentText(t, res))
	}
}

// A call that never reached the handler is not a use: a one-use grant survives
// the probe, which is what the other refusals before the handler do.
func TestAFencedRefusalDoesNotSpendTheGrant(t *testing.T) {
	f := newFenceFixture(t, fenced, t.TempDir())
	f.grantFor(t, "staging", 1)

	assertCode(t, f.call(t, map[string]any{"profile": "staging", "namespace": "prod"}), "core.profile.fenced")
	if res := f.call(t, map[string]any{"profile": "staging"}); res.IsError {
		t.Fatalf("the one use was spent on a call that never ran: %s", contentText(t, res))
	}
}

// The fence is checked after the grant gate, so a call nobody granted is
// refused the way an unknown profile is: the fence is no way to learn that a
// profile exists, or what it reaches.
func TestTheFenceIsNoWayToLearnWhatAProfileReaches(t *testing.T) {
	f := newFenceFixture(t, fenced, t.TempDir())
	f.grantFor(t, "staging", 0)

	granted := f.call(t, map[string]any{"profile": "prod", "namespace": "anything"})
	unknown := f.call(t, map[string]any{"profile": "nowhere", "namespace": "anything"})
	assertCode(t, granted, "core.grant.required")
	assertCode(t, unknown, "core.grant.required")
}

// Editing the list changes what the grant was issued against, so it asks
// again — the same way editing `set:` does.
func TestEditingTheFenceStopsTheGrantThatNamedTheConnection(t *testing.T) {
	dir := t.TempDir()
	f := newFenceFixture(t, fenced, dir)
	f.grantFor(t, "staging", 0)
	if res := f.call(t, map[string]any{"profile": "staging"}); res.IsError {
		t.Fatalf("the connection that was consented to was refused: %s", contentText(t, res))
	}

	widened := strings.Replace(fenced, "[staging, staging-jobs]", "[staging, staging-jobs, prod]", 1)
	after := newFenceFixture(t, widened, dir)
	res := after.call(t, map[string]any{"profile": "staging"})
	assertCode(t, res, "core.grant.required")
	if *after.ran != 0 {
		t.Fatal("a grant issued against a narrower fence covered a wider one")
	}

	// Re-consent to the connection as it now is restores the call, and the
	// widened list is then what the call is held to.
	after.grantFor(t, "staging", 0)
	if res := after.call(t, map[string]any{"profile": "staging", "namespace": "prod"}); res.IsError {
		t.Fatalf("re-consent did not restore the call: %s", contentText(t, res))
	}
}
