package grant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rta/internal/grant"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// refusal is the coded refusal a call answered with.
func refusal(t *testing.T, err error) *view.Error {
	t.Helper()
	var verr *view.Error
	if err == nil || !asError(err, &verr) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	return verr
}

// namesInput holds a refusal to the one input it is about, by the name c
// declares it under: a name c does not declare is one every surface ignores,
// and "" is a refusal no one box holds the fix to.
func namesInput(t *testing.T, c plugin.Capability, verr *view.Error, code, input string) {
	t.Helper()
	switch {
	case verr == nil:
		t.Errorf("%s: no refusal, want %s", c.ID, code)
	case verr.Code != code:
		t.Errorf("%s: refused as %s (%s), want %s", c.ID, verr.Code, verr.Message, code)
	case verr.Input != input:
		t.Errorf("%s: %s names the input %q, want %q", c.ID, code, verr.Input, input)
	case input != "":
		if _, ok := c.ErrorInput(verr); !ok {
			t.Errorf("%s: %s names %q, which %s does not declare", c.ID, code, input, c.ID)
		}
	}
}

type inputCase struct {
	name   string
	values map[string]any
	code   string
	input  string
}

// What `grant allow` refuses of one value names that value's input, whether
// the value was typed for this machine or for the server that will build the
// grant: the form opens again on that box, and a script reads which one.
func TestAllowNamesTheInputItRefuses(t *testing.T) {
	setup(t)
	instancedConfig(t)
	allow := guardCap(t, "grant.allow")
	for _, c := range []inputCase{
		{"a target of white space", map[string]any{"target": " "}, "grant.notarget", "target"},
		{"a target nothing answers to", map[string]any{"target": "kv.gett"}, "grant.unknowntarget", "target"},
		{"a target that needs no grant", map[string]any{"target": "todo.list"}, "grant.needless", "target"},
		{"a window that is no duration", map[string]any{"target": "kv.get", "ttl": "soon"}, "grant.badttl", "ttl"},
		{"a window of nothing", map[string]any{"target": "kv.get", "ttl": "0"}, "grant.badttl", "ttl"},
		{"a negative bound", map[string]any{"target": "kv.get", "max-uses": -1}, "grant.badmaxuses", "max-uses"},
		{"a rate with no window", map[string]any{"target": "kv.get", "rate": "10"}, "grant.badrate", "rate"},
		{"a record of white space", map[string]any{"target": "kv.get", "scope": " "}, "grant.scope.blank", "scope"},
		{"a record of every record", map[string]any{"target": "kv.get", "scope": "/"}, "grant.scope.root", "scope"},
		{"a folder that climbs", map[string]any{"target": "kv.get", "scope": "prod/../"}, "grant.scope.traversal", "scope"},
		{"a record nothing scopes on", map[string]any{"target": "kv.env", "scope": "prod"}, "grant.scope.unscoped", "scope"},
		{"an agent of other letters", map[string]any{"target": "kv.get", "agent": "claude desktop"}, "grant.agent.charset", "agent"},
		{"an agent too long", map[string]any{"target": "kv.get", "agent": strings.Repeat("a", core.MaxAgentName+1)}, "grant.agent.long", "agent"},
		{"no such profile", map[string]any{"target": "kv.get", "profile": "prod"}, "grant.unknownprofile", "profile"},
		{"a profile of another plugin", map[string]any{"target": "todo.rm", "profile": "staging"}, "grant.profilemismatch", "profile"},
		{"no such connection", map[string]any{"target": "kv.get", "profile": "staging/missing"}, "grant.unknowninstance", "profile"},
		{"no connection named", map[string]any{"target": "kv.get", "profile": "staging"}, "grant.instancerequired", "profile"},
		{"a server with no agent", map[string]any{"target": "kv.get", "server": "lab"}, "grant.noagent", "agent"},
		{"a server and a record of white space", map[string]any{"target": "kv.get", "server": "lab", "agent": "claude", "scope": " "},
			"grant.scope.blank", "scope"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := allow.Run(context.Background(), req(c.values))
			namesInput(t, allow, refusal(t, err), c.code, c.input)
		})
	}
	if grants, _ := core.Load(); len(grants) != 0 {
		t.Errorf("a refusal left %+v", grants)
	}
}

// An agent left out is filled in only when this machine knows exactly one,
// and the refusal when it knows none or several is about the agent box.
func TestAGrantThatCannotGuessItsAgentNamesTheAgentInput(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	allow, issue := guardCap(t, "grant.allow"), guardCap(t, "grant.issue")
	_, err := allow.Run(context.Background(), req(map[string]any{"target": "kv.get"}))
	namesInput(t, allow, refusal(t, err), "grant.noagent", "agent")
	_, err = issue.Run(context.Background(), issueReq(map[string]any{"profile": "staging"}, false, false))
	namesInput(t, issue, refusal(t, err), "grant.noagent", "agent")

	for _, name := range []string{"claude", "codex"} {
		if err := session.Start(session.Record{
			ID: session.NewID(), Agent: name, Since: time.Now(), PID: os.Getpid(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = allow.Run(context.Background(), req(map[string]any{"target": "kv.get"}))
	namesInput(t, allow, refusal(t, err), "grant.whichagent", "agent")
}

// Renew and revoke name the record, the target and the window they refuse; a
// selector that conflicts with another, or a revoke naming none of them, names
// no input, since no one box holds the fix.
func TestRenewAndRevokeNameTheInputTheyRefuse(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "agent": "claude"})
	renew, revoke := guardCap(t, "grant.renew"), guardCap(t, "grant.revoke")
	for _, c := range []struct {
		inputCase
		cap plugin.Capability
	}{
		{inputCase{"renew a record of white space", map[string]any{"scope": " "}, "grant.scope.blank", "scope"}, renew},
		{inputCase{"renew exactly no target", map[string]any{"exact": true}, "grant.exact.target", "target"}, renew},
		{inputCase{"renew by a window that is no duration", map[string]any{"ttl": "soon"}, "grant.badttl", "ttl"}, renew},
		{inputCase{"revoke a record of white space", map[string]any{"target": "kv.get", "scope": " "}, "grant.scope.blank", "scope"}, revoke},
		{inputCase{"revoke exactly no target", map[string]any{"exact": true}, "grant.exact.target", "target"}, revoke},
		{inputCase{"revoke a name nothing answers to", map[string]any{"target": "kv.gett"}, "grant.unknowntarget", "target"}, revoke},
		{inputCase{"revoke one grant and every grant", map[string]any{"exact": true, "all": true}, "grant.exact.all", ""}, revoke},
		{inputCase{"revoke naming nothing", map[string]any{}, "grant.notarget", ""}, revoke},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.cap.Run(context.Background(), req(c.values))
			namesInput(t, c.cap, refusal(t, err), c.code, c.input)
		})
	}
	if grants, _ := core.Load(); len(grants) != 1 {
		t.Errorf("a refusal changed the store: %+v", grants)
	}
}

// The operator channel's server halves answer the capability the operator ran
// on their own machine, so what they refuse names that capability's input; and
// the client's review of a draft names the input whose value it cannot read.
func TestTheOperatorChannelsRefusalsNameTheRemoteCallsInput(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "agent": "claude"})
	allow, revoke := guardCap(t, "grant.allow"), guardCap(t, "grant.revoke")
	prepare := PrepareRemote(catalog, builtIn)
	for _, c := range []struct {
		spec        operatorid.IssueSpec
		code, input string
	}{
		{operatorid.IssueSpec{Target: "kv.gett", Agent: "claude"}, "grant.unknowntarget", "target"},
		{operatorid.IssueSpec{Target: "kv.get"}, "grant.noagent", "agent"},
		{operatorid.IssueSpec{Target: "kv.get", Agent: "claude", TTL: "soon"}, "grant.badttl", "ttl"},
		{operatorid.IssueSpec{Target: "kv.get", Agent: "claude", Scope: "/"}, "grant.scope.root", "scope"},
	} {
		_, verr := prepare(c.spec, "tobi")
		namesInput(t, allow, verr, c.code, c.input)
	}
	revokeRemote := RevokeRemote(catalog)
	for _, c := range []struct {
		spec        operatorid.RevokeSpec
		code, input string
	}{
		{operatorid.RevokeSpec{Exact: true}, "grant.exact.target", "target"},
		{operatorid.RevokeSpec{Target: "kv.get", Scope: " "}, "grant.scope.blank", "scope"},
		{operatorid.RevokeSpec{Target: "kv.gett"}, "grant.unknowntarget", "target"},
	} {
		_, verr := revokeRemote(c.spec, false)
		namesInput(t, revoke, verr, c.code, c.input)
	}

	server := "https://lab"
	spec := operatorid.IssueSpec{Target: "kv.get", Agent: "claude", Rate: "fast"}
	draft := core.Grant{Target: "kv.get", Agent: "claude", Server: server}
	namesInput(t, allow, checkPrepared(plugin.SurfaceCLI, spec, server, draft), "grant.badrate", "rate")
	spec = operatorid.IssueSpec{Target: "kv.get", Agent: "claude", TTL: "soon"}
	draft = core.Grant{Target: "kv.get", Agent: "claude", Server: server, TTL: "soon",
		From: core.FromOperatorPrefix + "tobi", Issued: time.Now()}
	namesInput(t, allow, checkPrepared(plugin.SurfaceCLI, spec, server, draft), "grant.badttl", "ttl")
}

// `grant issue` names the input it refuses, an environment's and a role's
// alike; a flag given beside a role names that flag. A role and an environment
// both, or neither, name no input.
func TestIssueNamesTheInputItRefuses(t *testing.T) {
	configDir, _ := envSetup(t)
	writeFile(t, filepath.Join(configDir, "config.yaml"), envConfig+"roles:\n  dev:\n    grants: [db.vacuum --profile staging]\n")
	issue := guardCapIn(t, "grant.issue")
	for _, c := range []inputCase{
		{"an unknown class", map[string]any{"profile": "staging", "safety": "everything"}, "grant.issue.safety", "safety"},
		{"a bad window", map[string]any{"profile": "staging", "ttl": "soon"}, "grant.issue.ttl", "ttl"},
		{"a negative bound", map[string]any{"profile": "staging", "max-uses": -1}, "grant.badmaxuses", "max-uses"},
		{"a bad rate", map[string]any{"profile": "staging", "rate": "fast"}, "grant.badrate", "rate"},
		{"an agent that is no name", map[string]any{"profile": "staging", "agent": "bad name"}, "grant.agent.charset", "agent"},
		{"no such environment", map[string]any{"profile": "prod"}, "grant.unknownprofile", "profile"},
		{"no such connection", map[string]any{"profile": "staging/nope"}, "grant.unknowninstance", "profile"},
		{"one connection", map[string]any{"profile": "staging/analytics"}, "grant.issue.instance", "profile"},
		{"a class beside a role", map[string]any{"role": "dev", "safety": "write"}, "grant.issue.bundleflag", "safety"},
		{"a rate beside a role", map[string]any{"role": "dev", "rate": "10/1h"}, "grant.issue.bundleflag", "rate"},
		{"a role and an environment", map[string]any{"role": "dev", "profile": "staging"}, "grant.issue.both", ""},
		{"neither", map[string]any{}, "grant.issue.nothing", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			namesInput(t, issue, bundleRefusal(t, issueReq(c.values, false, false)), c.code, c.input)
		})
	}

	// A class that reaches nothing the environment holds is refused with the
	// wider one that does, so the class is the box.
	writeFile(t, filepath.Join(configDir, "config.yaml"), "profiles:\n  ops:\n    plugins:\n      ops:\n        set: {dsn: main}\n")
	writes := func() []plugin.Capability {
		return []plugin.Capability{{ID: "ops.restart", Safety: plugin.Write,
			Inputs: []plugin.Field{{Name: "dsn", Type: plugin.String, Config: "dsn"}}}}
	}
	_, err := runIssue(issueReq(map[string]any{"profile": "ops"}, false, false), writes, builtIn)
	namesInput(t, issue, refusal(t, err), "grant.issue.empty", "safety")

	roles := guardCapIn(t, "grant.roles")
	_, err = roles.Run(context.Background(), req(map[string]any{"role": "nosuch"}))
	namesInput(t, roles, refusal(t, err), "role.unknown", "role")
	if len(standing(t)) != 0 {
		t.Errorf("a refusal left %q", held(t))
	}
}

// Enrolling a remote guard names the roster and the URL it refuses.
func TestEnrollingARemoteGuardNamesTheInputItRefuses(t *testing.T) {
	setup(t)
	enroll := guardCap(t, "grant.guard.remote")
	missing := filepath.Join(t.TempDir(), "operators")
	for _, c := range []inputCase{
		{"no roster", map[string]any{"url": "https://rta.example.com"}, "core.guard.remote.roster", "operators"},
		{"no URL", map[string]any{"operators": missing}, "core.guard.remote.server", "url"},
		{"a URL in the clear", map[string]any{"operators": missing, "url": "http://rta.example.com"}, "core.operator.insecure", "url"},
		{"a roster that is not there", map[string]any{"operators": missing, "url": "https://rta.example.com"},
			"core.guard.remote.roster", "operators"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := enroll.Run(context.Background(), req(c.values))
			namesInput(t, enroll, refusal(t, err), c.code, c.input)
		})
	}
}

// A read needs no grant on the base connection and one on every other, so a
// grant refused for a read that takes a connection is answered on the box that
// would have made it one, the profile, and for a read that takes none on the
// target.
func TestANeedlessGrantOnAReadNamesTheBoxThatWouldMakeItOne(t *testing.T) {
	reads := func() []plugin.Capability {
		return []plugin.Capability{
			{ID: "db.status", Summary: "s", Safety: plugin.Read,
				Inputs: []plugin.Field{{Name: "host", Type: plugin.String, Config: "host"}}},
			{ID: "clock.now", Summary: "n", Safety: plugin.Read},
		}
	}
	for target, want := range map[string]string{"db.status": "profile", "clock.now": "target"} {
		verr := grantNeeded(plugin.SurfaceCLI, reads, target, "")
		if verr == nil || verr.Code != "grant.needless" || verr.Input != want {
			t.Errorf("%s: refused as %+v, want grant.needless naming %s", target, verr, want)
		}
	}
}

// A roster that is there names its input for what it holds, and not for what
// keeps it from being read: one refused for its mode, or one this process may
// not read, is answered by a chmod and names none, while a file that holds no
// roster, or a directory, is the wrong path and names it.
func TestARosterThatIsThereNamesItsInputOnlyForWhatItHolds(t *testing.T) {
	setup(t)
	enroll := guardCap(t, "grant.guard.remote")
	dir := t.TempDir()
	roster := func(name, body string, mode os.FileMode) map[string]any {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		return map[string]any{"operators": path, "url": "https://rta.example.com"}
	}
	cases := []inputCase{
		{"a roster anyone can write", roster("open", "alice AAAA\n", 0o666), "core.guard.remote.roster", ""},
		{"a file that holds no roster", roster("junk", "not a roster line\n", 0o600), "core.guard.remote.roster", "operators"},
		{"a directory", map[string]any{"operators": t.TempDir(), "url": "https://rta.example.com"}, "core.guard.remote.roster", "operators"},
	}
	if os.Geteuid() != 0 {
		cases = append(cases, inputCase{"a roster this process may not read", roster("shut", "alice AAAA\n", 0o000),
			"core.guard.remote.roster", ""})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := enroll.Run(context.Background(), req(c.values))
			namesInput(t, enroll, refusal(t, err), c.code, c.input)
		})
	}
}
