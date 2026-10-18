package mcp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// profileArgument reports whether the pg_query tool, as the server lists it
// now, offers a profile argument and asks for it.
func (f *profileFixture) profileArgument(t *testing.T) (offered, required bool) {
	t.Helper()
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal([]byte(f.schema(t)), &schema); err != nil {
		t.Fatal(err)
	}
	_, offered = schema.Properties["profile"]
	return offered, slices.Contains(schema.Required, "profile")
}

func (f *profileFixture) expectListChanged(t *testing.T) {
	t.Helper()
	select {
	case <-f.listChanged:
	case <-time.After(5 * time.Second):
		t.Fatal("the server advertises listChanged and sent no tools/list_changed")
	}
}

func (f *profileFixture) expectNoListChanged(t *testing.T, why string) {
	t.Helper()
	select {
	case <-f.listChanged:
		t.Fatalf("tools/list_changed was sent %s", why)
	case <-time.After(150 * time.Millisecond):
	}
}

// The tool list is sent when a client asks, and it is built from the profiles
// the server started with. An operator who writes the first profile for an
// area under a running server made every tool of that area require the
// argument the list the client held did not have, so a client validating
// calls against its list could not send the one argument the refusal asked
// for. The server advertises tools.listChanged; the call that is refused is
// what notices the file, and the list the client fetches next is the file's.
func TestAProfileWrittenAfterTheServerStartedIsOfferedToTheClient(t *testing.T) {
	f := newProfileFixture(t, "plugins:\n  pg:\n    host: base.internal\n")
	if offered, _ := f.profileArgument(t); offered {
		t.Fatal("an install with no profile offers the argument")
	}

	if err := writeFile(f.dir+"/config.yaml", twoProfiles); err != nil {
		t.Fatal(err)
	}
	res := f.call(t, map[string]any{"sql": "select 1"})
	assertCode(t, res, "core.profile.required")
	f.expectListChanged(t)
	if offered, required := f.profileArgument(t); !offered || !required {
		t.Errorf("after the file gained profiles the list offers the argument = %v and requires it = %v", offered, required)
	}
	f.expectNoListChanged(t, "for a list that no longer differs from the file")
}

// And the other way: the last profile removed leaves a tool that refuses a
// profile as an argument it does not have, so the list stops offering it. A
// client that asks again, without having called, is told as well.
func TestAProfileRemovedAfterTheServerStartedIsWithdrawnFromTheList(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)
	if offered, required := f.profileArgument(t); !offered || !required {
		t.Fatalf("a configured install offers the argument = %v and requires it = %v", offered, required)
	}
	f.expectNoListChanged(t, "before anything changed")

	if err := writeFile(f.dir+"/config.yaml", "plugins:\n  pg:\n    host: base.internal\n"); err != nil {
		t.Fatal(err)
	}
	if offered, _ := f.profileArgument(t); offered {
		t.Error("the list still offers the argument after the file lost its profiles")
	}
	f.expectListChanged(t)
	assertCode(t, f.call(t, map[string]any{"profile": "staging", "sql": "select 1"}), "core.mcp.badargs")
}

// A call to a tool no profile can change reads nothing it did not already
// read, and republishes nothing.
func TestACallToAToolNoProfileChangesDoesNotRepublishTheList(t *testing.T) {
	f := newProfileFixture(t, "plugins:\n  pg:\n    host: base.internal\n")
	if err := writeFile(f.dir+"/config.yaml", twoProfiles); err != nil {
		t.Fatal(err)
	}
	if res := f.callOther(t); res.IsError {
		t.Fatalf("a tool the profiles do not govern was refused: %s", contentText(t, res))
	}
	f.expectNoListChanged(t, "by a call to a tool no profile governs")
}

// A refusal that sends an agent to name a connection says which argument, and
// what to do when the tool it holds does not list one: the list it holds is
// older than the file.
func TestTheRefusalToANamelessCallNamesTheArgument(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)
	res := f.call(t, map[string]any{"sql": "select 1"})
	assertCode(t, res, "core.profile.required")
	var e struct {
		Hint string `json:"hint"`
	}
	if err := json.Unmarshal([]byte(contentText(t, res)), &e); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"profile"`, "ask the operator", "grant", "tool list", "restart"} {
		if !strings.Contains(e.Hint, want) {
			t.Errorf("the hint does not say %q: %s", want, e.Hint)
		}
	}
}
