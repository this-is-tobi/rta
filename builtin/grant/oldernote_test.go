package grant

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/pkg/view"
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

// The roster is where somebody goes when a grant they can see is refused
// anyway, and until now it was the one surface that said nothing: the rows
// are read from the file and a server that started on another build decides
// by rules the file cannot describe.
//
// A warning rather than a row or a column, because it is not about any one
// grant — which of the open servers takes the next call is not something this
// can know. The count and the pid are what the reader acts on, so both are
// pinned, and so is the quiet case: every server on this build says nothing.
func TestTheRosterWarnsWhenAServerOnAnotherBuildWillDecide(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	session.SetSelf("v0.22.0")
	t.Cleanup(func() { session.SetSelf("") })

	now := time.Now()
	if err := session.Start(session.Record{
		ID: session.NewID(), Agent: "test", Since: now, PID: os.Getpid(), Version: "v0.22.0",
	}); err != nil {
		t.Fatal(err)
	}
	if verr := core.Save([]core.Grant{
		{Target: "kv.get", Issued: now, Expires: now.Add(time.Hour)},
	}); verr != nil {
		t.Fatal(verr)
	}

	quiet, verr := heldTable("", false)
	if verr != nil {
		t.Fatal(verr)
	}
	current, ok := quiet.(view.Table)
	if !ok {
		t.Fatalf("held table = %s, want a Table", view.TypeOf(quiet))
	}
	if len(current.Warnings) != 0 {
		t.Fatalf("warned while every open server is on this build: %+v", current.Warnings)
	}

	if err := session.Start(session.Record{
		ID: session.NewID(), Agent: "claude", Since: now, PID: 17188, Version: "v0.21.1",
	}); err != nil {
		t.Fatal(err)
	}
	loud, verr := heldTable("", false)
	if verr != nil {
		t.Fatal(verr)
	}
	stale, ok := loud.(view.Table)
	if !ok {
		t.Fatalf("held table = %s, want a Table", view.TypeOf(loud))
	}
	if len(stale.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want exactly the older-server one", stale.Warnings)
	}
	w := stale.Warnings[0]
	if w.Code != "core.grant.older.server" {
		t.Errorf("code = %q, want core.grant.older.server", w.Code)
	}
	if !strings.Contains(w.Message, "1 server is open on another build") {
		t.Errorf("message = %q, want the count in it", w.Message)
	}
	if !strings.Contains(w.Hint, "17188") {
		t.Errorf("hint = %q, want the pid to reconnect", w.Hint)
	}
	if strings.Contains(w.Hint, strconv.Itoa(os.Getpid())) {
		t.Errorf("hint = %q, want the server on this build left out", w.Hint)
	}

	// One set, named twice. The note `grant allow` prints at issue time and
	// the warning the roster carries are the same sentence about the same
	// servers; when they were written separately they drifted, which is how
	// the note above this one came to have a test at all.
	note := olderServerNote()
	if !strings.Contains(note, "17188") || !strings.Contains(w.Hint, "claude") {
		t.Errorf("the two surfaces name different sets: note %q, hint %q", note, w.Hint)
	}
}
