package grant

import (
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/session"
)

// The note `grant allow` prints when a server that is open right now will
// decide this grant by rules it was built with.
//
// It had no test, which is how it shipped reading "note: are open on
// another build of rta": builtin/grant's local plural returned the word
// without its number, internal/app's returned both, and they had the same
// name and signature. The count is the whole point of the sentence — one
// stale server and five are different situations — so it is pinned here
// along with the pids, which are what the operator acts on.
func TestTheOlderServerNoteCountsThemInASentence(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	session.SetSelf("v0.22.0")
	t.Cleanup(func() { session.SetSelf("") })

	if got := olderServerNote(); got != "" {
		t.Fatalf("note with no server open = %q, want nothing said", got)
	}

	now := time.Now()
	for _, r := range []session.Record{
		{ID: session.NewID(), Agent: "claude", Since: now, PID: 4242, Version: "v0.22.0"},
		{ID: session.NewID(), Agent: "claude", Since: now, PID: 17188, Version: "v0.21.1"},
	} {
		if err := session.Start(r); err != nil {
			t.Fatal(err)
		}
	}
	one := olderServerNote()
	if !strings.HasPrefix(one, "note: 1 server is open on another build") {
		t.Errorf("one stale server reads %q", one)
	}
	if !strings.Contains(one, "17188") {
		t.Errorf("the note does not name the pid to reconnect: %s", one)
	}
	if strings.Contains(one, "4242") {
		t.Errorf("the server on this build was called stale: %s", one)
	}

	if err := session.Start(session.Record{
		ID: session.NewID(), Agent: "cursor", Since: now, PID: 22451, Version: "v0.20.0",
	}); err != nil {
		t.Fatal(err)
	}
	two := olderServerNote()
	if !strings.HasPrefix(two, "note: 2 servers are open on another build") {
		t.Errorf("two stale servers read %q", two)
	}
}
