package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// parkCall parks a call for the test's lifetime, in the package's own data
// directory, and returns the id the queue knows it by.
func parkCall(t *testing.T, capID, record, preview string) string {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	parked, err := consent.Ask(consent.Call{
		Cap: capID, Safety: "destructive", Scopes: []string{record}, Agent: "claude",
		Preview: preview, Why: "no grant",
	}, time.Minute)
	if err != nil {
		t.Fatalf("parking a call: %v", err)
	}
	t.Cleanup(parked.Close)
	reqs, err := consent.Pending()
	if err != nil || len(reqs) == 0 {
		t.Fatalf("the parked call is not in the queue: %v %v", reqs, err)
	}
	return reqs[len(reqs)-1].ID
}

// screenOf runs a built-in read for real and shows its result as the screen,
// the way a tile opening it would.
func screenOf(t *testing.T, capID string, values map[string]any) Model {
	t.Helper()
	m := New(realRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	c := mustCap(t, m.reg, capID)
	v, err := c.Run(context.Background(), plugin.NewRequest(values, false, false).WithSurface(plugin.SurfaceTUI))
	if err != nil {
		t.Fatal(err)
	}
	sm := sized.(Model)
	sm.lastValues = values
	shown, _ := sm.Update(resultMsg{cap: c, view: v})
	return shown.(Model)
}

func barOf(m Model) string { return footerOf(m.resultView(), footerMaxLines) }

// `a` used to open a form that never said which call it was about, and three
// enters later it had allowed it. It now says the call, and waits.
func TestAOnAParkedCallSaysWhatItWouldAllowAndWaits(t *testing.T) {
	id := parkCall(t, "note.rm", "2", "would remove note 2: third note")
	queue := screenOf(t, "agent.pending", nil)
	if queue.current.ID != "agent.pending" || !queue.interactive() {
		t.Fatalf("the queue did not open as a list of calls: %q", queue.current.ID)
	}
	armed := press(t, queue, "a")
	if armed.armed == nil || armed.mode != modeResult {
		t.Fatalf("a left the screen %v with nothing armed: %+v", armed.mode, armed.armed)
	}
	if armed.armed.base["id"] != id {
		t.Errorf("armed for %v, want the call under the cursor, %s", armed.armed.base["id"], id)
	}
	bar := barOf(armed)
	for _, want := range []string{"enter", "allow claude's note.rm 2 once", "would remove note 2: third note", "A for a while", "esc cancel"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the bar lacks %q:\n%s", want, bar)
		}
	}
	if _, waiting := consent.Find(id); !waiting {
		t.Error("pressing a answered the call; it has to wait for enter")
	}
}

func TestEnterAllowsTheCallTheLineNamedAndNothingWider(t *testing.T) {
	id := parkCall(t, "note.rm", "2", "would remove note 2: third note")
	armed := press(t, screenOf(t, "agent.pending", nil), "a")
	// The queue refreshes under the cursor while the line is up; the call that
	// gets answered is the one that was named.
	armed.row = 5
	ran := press(t, armed, "enter")
	if ran.mode != modeRunning || ran.current.ID != "agent.allow" {
		t.Fatalf("enter left %v on %q, want agent.allow running", ran.mode, ran.current.ID)
	}
	if ran.lastValues["id"] != id || len(ran.lastValues) != 1 {
		t.Errorf("allowed with %v, want exactly the id %s and no ttl or role", ran.lastValues, id)
	}
	if ran.form != nil || ran.armed != nil {
		t.Error("a form or an armed answer survived the run")
	}
}

func TestAnyOtherKeyCancelsAndIsSpentDoingSo(t *testing.T) {
	id := parkCall(t, "note.rm", "2", "would remove note 2")
	queue := screenOf(t, "agent.pending", nil)
	for _, key := range []string{"esc", "d", "j", "q", "x"} {
		got := press(t, press(t, queue, "a"), key)
		if got.armed != nil {
			t.Errorf("%s left the answer armed", key)
		}
		if got.mode != modeResult || got.current.ID != "agent.pending" || got.row != queue.row {
			t.Errorf("%s did something besides cancel: mode %v on %q row %d", key, got.mode, got.current.ID, got.row)
		}
		if !strings.Contains(got.flash, "still waiting") {
			t.Errorf("%s cancelled without saying the call is still waiting: %q", key, got.flash)
		}
	}
	if _, waiting := consent.Find(id); !waiting {
		t.Error("cancelling answered the call")
	}
	if _, cmd := press2(press(t, queue, "a"), tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd == nil {
		t.Error("ctrl+c did not quit while an answer was armed")
	}
}

func press2(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func TestACallThatIsGoneIsSaidSoAndNothingIsArmed(t *testing.T) {
	_ = parkCall(t, "note.rm", "2", "would remove note 2")
	queue := screenOf(t, "agent.pending", nil)
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	got := press(t, queue, "a")
	if got.armed != nil || !strings.Contains(got.flash, "not waiting any more") {
		t.Errorf("armed %v flash %q, want the gone call said", got.armed, got.flash)
	}
}

func TestTheAllowFormNamesTheCallItIsAbout(t *testing.T) {
	_ = parkCall(t, "note.rm", "2", "would remove note 2: third note")
	form := press(t, screenOf(t, "agent.pending", nil), "A")
	if form.mode != modeForm {
		t.Fatalf("A left %v", form.mode)
	}
	if title := plain(form.formView()); !strings.Contains(title, "claude asks note.rm 2") {
		t.Errorf("the form's title does not say which call it allows:\n%s", firstLine(title))
	}
}

func TestTheFooterSaysWhatWasAllowedWhenItIsDone(t *testing.T) {
	_ = parkCall(t, "note.rm", "2", "would remove note 2: third note")
	ran := press(t, press(t, screenOf(t, "agent.pending", nil), "a"), "enter")
	next, _ := ran.Update(resultMsg{cap: ran.current, view: view.Text{Body: "allowed"}, seq: ran.runSeq})
	done := next.(Model)
	if !strings.Contains(done.flash, "allowed claude's note.rm 2 once") {
		t.Errorf("the footer says %q after the answer, want the call that was allowed", done.flash)
	}
	if done.said != "" {
		t.Error("what was said outlived the answer")
	}
}

// The page of one call answers the same way the list does: it is the same
// question, asked about the call it is showing.
func TestTheCallsOwnPageAnswersWithTheSameKeys(t *testing.T) {
	id := parkCall(t, "note.rm", "2", "would remove note 2: third note")
	page := screenOf(t, "agent.show", map[string]any{"id": id})
	if page.current.ID != "agent.show" {
		t.Fatalf("the page is %q", page.current.ID)
	}
	armed := press(t, page, "a")
	if armed.armed == nil || armed.armed.base["id"] != id {
		t.Fatalf("a on the page armed %+v, want the call it shows", armed.armed)
	}
	if !strings.Contains(barOf(armed), "allow claude's note.rm 2 once") {
		t.Errorf("the bar does not name the call:\n%s", barOf(armed))
	}
	if form := press(t, page, "A"); form.mode != modeForm || !formFields(form)["ttl"] {
		t.Errorf("A on the page left %v, want the ttl and role form", form.mode)
	}
	if line := firstLine(learn(t, page, oneCall()).View().Content); strings.Contains(line, "w to answer") {
		t.Errorf("the line over the call's own page points at the queue it came from: %s", line)
	}
}

// What is confirmed is what is answered: a call that is gone, or that is not
// the one the line named, is refused before anything runs.
func TestEnterRefusesACallThatIsGoneOrIsNotTheOneShown(t *testing.T) {
	_ = parkCall(t, "note.rm", "2", "would remove note 2: third note")
	armed := press(t, screenOf(t, "agent.pending", nil), "a")

	changed := armed
	changed.armed = &armedAnswer{act: armed.armed.act, base: armed.armed.base, line: "allow claude's kv.get once", done: "x"}
	if got := press(t, changed, "enter"); got.mode != modeResult || !strings.Contains(got.flash, "changed after it was shown") {
		t.Errorf("a call that is not the one named ran: mode %v flash %q", got.mode, got.flash)
	}

	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if got := press(t, armed, "enter"); got.mode != modeResult || !strings.Contains(got.flash, "not waiting any more") {
		t.Errorf("a call that is gone ran: mode %v flash %q", got.mode, got.flash)
	}
}
