package mcp

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/profile"
)

// `sys_cpu {"profile": "staging"}` was refused as an ungranted call, and its
// hint sent the operator to `rta grant allow sys --profile staging`, which the
// operator's CLI rejects because no profile says anything about sys: an agent
// asked a person for something nobody could do, and the person ran a command
// that failed. Where the area has no connection the argument is not one the
// tool has, and the refusal says so with the list the schema publishes.
func TestAProfileNamedWhereTheAreaHasNoConnectionIsAnUnknownArgument(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)

	res := callTool(t, f.session, "sys_disk", map[string]any{"profile": "staging"})
	assertCode(t, res, "core.mcp.badargs")
	text := contentText(t, res)
	if !strings.Contains(text, `unknown argument: \"profile\"`) || !strings.Contains(text, "this tool takes no arguments") {
		t.Errorf("the refusal does not say the tool has no such argument: %s", text)
	}
	if strings.Contains(text, "grant") || strings.Contains(text, "rta ") {
		t.Errorf("the agent is sent to the operator for what no grant could give: %s", text)
	}
	entries, err := agentlog.Read(1)
	if err != nil || len(entries) != 1 || entries[0].Outcome != agentlog.Refused || entries[0].Code != "core.mcp.badargs" {
		t.Errorf("the ledger recorded %+v (%v), want a refusal as a bad argument", entries, err)
	}
}

// Where the area does have connections nothing changes: the argument is taken,
// and an unknown name and an ungranted one are still the same sentence
// (TestAnUnknownProfileAndAnUngrantedOneLookTheSame holds the words).
func TestAProfileNamedWhereTheAreaHasConnectionsReachesTheGate(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)
	for _, name := range []string{"staging", "no-such-profile"} {
		assertCode(t, f.call(t, map[string]any{"profile": name, "sql": "select 1"}), "core.grant.required")
	}
}

// The schema is sent once and the file is read for every call, so an operator
// who writes the first profile for an area under a running server has made a
// tool that took no profile one that requires it (R5 refuses a call naming
// none at once). The argument has to follow the file, or an agent told "name
// a profile" would be told next that the tool has no such argument.
func TestAProfileWrittenAfterTheServerStartedCanBeNamed(t *testing.T) {
	f := newProfileFixture(t, "plugins:\n  pg:\n    host: base.internal\n")
	assertCode(t, f.call(t, map[string]any{"profile": "staging", "sql": "select 1"}), "core.mcp.badargs")

	if err := writeFile(f.dir+"/config.yaml", twoProfiles); err != nil {
		t.Fatal(err)
	}
	assertCode(t, f.call(t, map[string]any{"sql": "select 1"}), "core.profile.required")
	assertCode(t, f.call(t, map[string]any{"profile": "staging", "sql": "select 1"}), "core.grant.required")

	now := time.Now()
	written, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if verr := grant.Save([]grant.Grant{{
		Target: "pg", Profile: "staging", ProfilePin: profile.ConnStampFor(written, "staging", "pg"),
		Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	if res := f.call(t, map[string]any{"profile": "staging", "sql": "select 1"}); res.IsError {
		t.Fatalf("a profile written after the server started and granted was refused: %s", contentText(t, res))
	}
	if *f.sawHost != "staging.internal" {
		t.Errorf("host = %q, want the new profile's own", *f.sawHost)
	}
}

// A file that cannot be read says nothing of which areas have connections, so a
// name is not refused as an argument the tool does not have on the strength of
// an empty set: it goes to the gate, as it did, which refuses what nobody
// granted.
func TestWhileTheConfigCannotBeReadANamedProfileIsNotAnUnknownArgument(t *testing.T) {
	f := newProfileFixture(t, twoProfiles, func(o *Options) {
		o.Profiles = config.Config{}
		o.ProfilesErr = errors.New("parsing config.yaml: unclosed")
	})
	if err := writeFile(f.dir+"/config.yaml", "profiles: [unclosed\n"); err != nil {
		t.Fatal(err)
	}
	assertCode(t, f.call(t, map[string]any{"profile": "staging", "sql": "select 1"}), "core.grant.required")
}
