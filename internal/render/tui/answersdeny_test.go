package tui

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/consent"
)

// Allowing says which call it allowed ("allowed claude's note.rm 2 once") and
// denying said "agent.deny done": no agent, no capability, no record, and the row
// then left the list. The answer that cannot be taken back is the one that left
// the operator with no receipt of what it was.
func TestDenyingAParkedCallNamesTheCallItDenied(t *testing.T) {
	id := parkCall(t, "note.rm", "2", "would remove note 2: third note")
	queue := screenOf(t, "agent.pending", nil)
	if queue.armed != nil {
		t.Fatal("the fixture opened armed")
	}

	denied := pump(t, queue, keyMsg("d"))
	if _, waiting := consent.Find(id); waiting {
		t.Fatal("d did not deny the call")
	}
	if want := "denied claude's note.rm 2"; denied.flash != want {
		t.Errorf("the receipt is %q, want %q", denied.flash, want)
	}
	if denied.said != "" {
		t.Errorf("the receipt was left on the model: %q", denied.said)
	}
}

// The same receipt from the page of the one call, where d acts on the record the
// page is about.
func TestDenyingFromTheCallsOwnPageNamesTheCall(t *testing.T) {
	id := parkCall(t, "note.rm", "2", "would remove note 2: third note")
	page := screenOf(t, "agent.show", map[string]any{"id": id})
	if page.current.ID != "agent.show" {
		t.Fatalf("opened %q, want the call's page", page.current.ID)
	}
	denied := pump(t, page, keyMsg("d"))
	if _, waiting := consent.Find(id); waiting {
		t.Fatal("d did not deny the call")
	}
	if want := "denied claude's note.rm 2"; !strings.HasPrefix(denied.flash, want) {
		t.Errorf("the receipt is %q, want it to lead with %q", denied.flash, want)
	}
}

// A call that was answered elsewhere between the list being drawn and the key is
// not one the receipt can name, and the run is the declared one all the same.
func TestDenyingACallThatIsGoneStillRunsTheDeny(t *testing.T) {
	_ = parkCall(t, "note.rm", "2", "would remove note 2")
	queue := screenOf(t, "agent.pending", nil)
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	denied := pump(t, queue, keyMsg("d"))
	if strings.HasPrefix(denied.flash, "denied claude's") {
		t.Errorf("the receipt names a call that is not in the queue: %q", denied.flash)
	}
}
