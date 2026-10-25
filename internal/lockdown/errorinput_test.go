package lockdown

import (
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Every refusal of a lock's values is about one of them, and names it by the
// input lock.add declares for it: the local command, the remote pre-check and
// the operator channel's handler all build through here, so all three hand the
// same name back — the last of them across the wire to the operator's machine.
// A principal's name refused by the agent grammar names the name too, since
// grant.CheckAgent does not know which input it was given.
func TestARefusalNamesTheLockInputItIsAbout(t *testing.T) {
	fresh(t)
	built := func(kind, name, note, ttl string) *view.Error {
		_, verr := Build(kind, name, note, ttl, "terminal")
		return verr
	}
	_, kindRefused := CheckKind("agnet")
	for _, c := range []struct {
		name        string
		verr        *view.Error
		code, input string
	}{
		{"a kind nothing is", built("agnet", "claude", "", ""), "core.lock.kind", "kind"},
		{"no principal", built("agent", " ", "", ""), "core.lock.name", "name"},
		{"an agent of other letters", built("agent", "not a name", "", ""), "grant.agent.charset", "name"},
		{"an operator label too long", built("operator", strings.Repeat("o", grant.MaxAgentName+1), "", ""), "grant.agent.long", "name"},
		{"a padded credential", built("credential", "\tpadded", "", ""), "core.lock.name", "name"},
		{"a note that is a document", built("agent", "claude", strings.Repeat("n", maxNote+1), ""), "core.lock.note", "note"},
		{"a window that is none", built("agent", "claude", "", "soon"), "core.lock.ttl", "ttl"},
		{"a kind checked alone", kindRefused, "core.lock.kind", "kind"},
		{"a kind handed to Add", Add(Lock{Kind: Kind("root"), Name: "x", At: time.Now()}), "core.lock.kind", "kind"},
		{"no principal handed to Add", Add(Lock{Kind: KindAgent, Name: "\t ", At: time.Now()}), "core.lock.name", "name"},
	} {
		if c.verr == nil || c.verr.Code != c.code {
			t.Errorf("%s: %v, want %s", c.name, c.verr, c.code)
			continue
		}
		if c.verr.Input != c.input {
			t.Errorf("%s: %s names the input %q, want %q", c.name, c.code, c.verr.Input, c.input)
		}
	}
	if locks, _ := Load(); len(locks) != 0 {
		t.Errorf("a refusal stored %+v", locks)
	}
}
