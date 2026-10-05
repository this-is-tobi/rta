package agent

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/builtin/kv"
	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func codeOfErr(t *testing.T, err error) string {
	t.Helper()
	verr, ok := err.(*view.Error)
	if !ok {
		t.Fatalf("want a *view.Error, got %T: %v", err, err)
	}
	return verr.Code
}

// With one call parked the question has one possible subject, and an eight-digit
// id copied off a notification that has already gone is the step that gets
// skipped.
func TestWithOneCallParkedAnswersNeedNoId(t *testing.T) {
	isolate(t)
	r := park(t, "note.rm", "3")
	shown, err := run(t, "agent.show", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first := shown.(view.Sections).Items[0].View.(view.KeyValue).Pairs[0]; first.Value != "note.rm" {
		t.Errorf("agent show with no id showed %+v, want the parked call", first)
	}
	if _, err := run(t, "agent.deny", nil); err != nil {
		t.Fatalf("agent deny with no id: %v", err)
	}
	if _, ok := consent.Find(r.ID); ok {
		t.Error("the one parked call was denied with no id and is still waiting")
	}
	again := park(t, "note.rm", "4")
	q := terminal(t, "y")
	v, err := allowAt(t, nil, false, false)
	if err != nil {
		t.Fatalf("agent allow with no id at a terminal: %v", err)
	}
	if q.count != 1 || !strings.Contains(q.card, "note.rm 4") {
		t.Errorf("the one parked call was allowed without being shown: asked %d times, card %q", q.count, q.card)
	}
	if got := v.(view.KeyValue).Pairs[0].Value; got != "note.rm 4" {
		t.Errorf("allowed %q, want the call that was parked (%s)", got, again.ID)
	}
}

// A bare allow guesses the one call that is parked only for a person who is
// shown it. --yes and a script skip the card, so nothing would ever put the call
// they meant beside the call they answered: they name it, and the refusal says
// which call is waiting and how to allow it.
func TestABareAllowThatNothingShowsTheCallForNamesTheCallInstead(t *testing.T) {
	isolate(t)
	r := parkedRemoval(t)
	for name, run := range map[string]func() error{
		"no terminal": func() error { _, err := allowAt(t, nil, false, false); return err },
		"--yes": func() error {
			terminal(t, "y")
			_, err := allowAt(t, nil, false, true)
			return err
		},
	} {
		err := run()
		if err == nil || codeOfErr(t, err) != "agent.request.unnamed" {
			t.Fatalf("%s: err = %v, want agent.request.unnamed", name, err)
		}
		verr := err.(*view.Error)
		if !strings.Contains(verr.Message, "note.rm 3") || !strings.Contains(verr.Hint, "rta agent allow "+r.ID) {
			t.Errorf("%s: the refusal does not name the call and the command: %+v", name, verr)
		}
		if _, ok := consent.Find(r.ID); !ok {
			t.Fatalf("%s: a call nobody named was answered", name)
		}
	}
	if _, err := allowAt(t, nil, true, false); err != nil {
		t.Errorf("a dry run names the call it would allow and needs no id: %v", err)
	}
}

// Allowing the wrong one of two is not a mistake the next command takes back,
// so several parked is a list to choose from and never a guess.
func TestWithSeveralCallsParkedAnAnswerWithNoIdListsThemAndAnswersNone(t *testing.T) {
	isolate(t)
	a := park(t, "kv.get", "db-password")
	b := park(t, "note.rm", "3")
	for _, id := range []string{"agent.allow", "agent.deny", "agent.show"} {
		_, err := run(t, id, nil)
		if err == nil {
			t.Fatalf("%s with two calls waiting and no id answered one of them", id)
		}
		if got := codeOfErr(t, err); got != "agent.request.several" {
			t.Errorf("%s: code %q, want agent.request.several", id, got)
		}
		for _, want := range []string{a.ID, b.ID, "kv.get db-password", "note.rm 3"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the list does not carry %q: %v", id, want, err)
			}
		}
	}
	for _, r := range []consent.Request{a, b} {
		if _, ok := consent.Find(r.ID); !ok {
			t.Errorf("request %s was answered by a call that named none", r.ID)
		}
	}
}

func TestAnAnswerWithNoIdAndNothingWaitingSaysSo(t *testing.T) {
	isolate(t)
	_, err := run(t, "agent.allow", nil)
	if err == nil {
		t.Fatal("agent allow with nothing parked succeeded")
	}
	if got := codeOfErr(t, err); got != "agent.request.none" {
		t.Errorf("code %q, want agent.request.none", got)
	}
}

// A remote answer is signed over what the operator's own machine read, so the
// request is named, never picked.
func TestARemoteAnswerStillNamesTheRequest(t *testing.T) {
	isolate(t)
	_, err := run(t, "agent.allow", map[string]any{"server": "lab"})
	if err == nil || codeOfErr(t, err) != "agent.request.unnamed" {
		t.Fatalf("agent allow --server with no id: %v", err)
	}
}

type asked struct {
	card, prompt string
	count        int
}

// terminal puts a person at the other end: the question is recorded and
// answered with answer, as a line typed.
func terminal(t *testing.T, answer string) *asked {
	t.Helper()
	was, wasPut := atTerminal, putQuestion
	t.Cleanup(func() { atTerminal, putQuestion = was, wasPut })
	atTerminal = func(plugin.Request) bool { return true }
	got := &asked{}
	putQuestion = func(card, prompt string) (string, error) {
		got.card, got.prompt = card, prompt
		got.count++
		return answer, nil
	}
	return got
}

func allowAt(t *testing.T, values map[string]any, dryRun, yes bool) (view.View, error) {
	t.Helper()
	c := capability(t, "agent.allow")
	return c.Run(context.Background(), plugin.NewRequest(
		plugin.Resolve(c, plugin.Inputs{Caller: values}), dryRun, yes).WithSurface(plugin.SurfaceCLI))
}

func parkedRemoval(t *testing.T) consent.Request {
	t.Helper()
	p, err := consent.Ask(consent.Call{
		Cap: "note.rm", Safety: "destructive", Scopes: []string{"3"}, Agent: "claude",
		Args:    map[string]any{"id": 3},
		Why:     "no active grant for note.rm 3",
		Preview: "would remove note 3: tui test note",
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p.Request
}

// `agent allow 6904368d` copied off a notification approved a note removal that
// was never on screen. At a terminal the call is put in front of the person
// first, with what the capability's own dry run said it would do.
func TestAtATerminalAllowShowsTheCallAndWaitsForAYes(t *testing.T) {
	isolate(t)
	r := parkedRemoval(t)
	q := terminal(t, "y")
	v, err := allowAt(t, map[string]any{"id": r.ID}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if q.count != 1 {
		t.Fatalf("asked %d times, want once", q.count)
	}
	for _, want := range []string{"note.rm 3", "claude asked", "id=3", "would remove note 3: tui test note"} {
		if !strings.Contains(q.card, want) {
			t.Errorf("the card does not carry %q:\n%s", want, q.card)
		}
	}
	if q.prompt != "Allow once? [y/N] " {
		t.Errorf("prompt = %q", q.prompt)
	}
	if got := v.(view.KeyValue).Pairs[0]; got.Key != "allowed" {
		t.Errorf("a yes was answered with %+v", got)
	}
}

// Anything but a yes is a no, a closed input included, and a no leaves the call
// where it was, so the person can still refuse it now or let it run out.
// The card is written straight to the terminal, and what is in it came from the
// agent: a cursor movement in a preview must reach the screen as nothing.
func TestTheCardCarriesNoControlSequence(t *testing.T) {
	isolate(t)
	p, err := consent.Ask(consent.Call{
		Cap: "note.rm", Safety: "destructive", Scopes: []string{"3"}, Agent: "claude",
		Why: "no active grant", Preview: "would remove note 3\x1b[2J\x1b[1;1Hnothing at all\rallowed",
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	q := terminal(t, "n")
	if _, err := allowAt(t, map[string]any{"id": p.Request.ID}, false, false); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(q.card, "\x1b\r") {
		t.Errorf("the card reached the terminal with a control character in it: %q", q.card)
	}
}

func TestAnythingButYesLeavesTheCallWaiting(t *testing.T) {
	for _, answer := range []string{"", "n", "N", "no", "yep", "ye", "y please"} {
		t.Run("answer "+answer, func(t *testing.T) {
			isolate(t)
			r := parkedRemoval(t)
			terminal(t, answer)
			v, err := allowAt(t, map[string]any{"id": r.ID}, false, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := consent.Find(r.ID); !ok {
				t.Fatal("a call that was not allowed is no longer waiting")
			}
			kvv := v.(view.KeyValue)
			if kvv.Pairs[0].Key != "not allowed" || !strings.Contains(kvv.Pairs[1].Value, "rta agent deny "+r.ID) {
				t.Errorf("the refusal to answer says %+v, want how to refuse it now", kvv.Pairs)
			}
		})
	}
}

// The prompt is for the person, not a gate: a script says it means it, and a
// dry run changes nothing to be sure about.
func TestScriptsAndDryRunsAreNeverAsked(t *testing.T) {
	isolate(t)
	r := parkedRemoval(t)
	q := terminal(t, "n")
	if _, err := allowAt(t, map[string]any{"id": r.ID}, true, false); err != nil {
		t.Fatal(err)
	}
	if q.count != 0 {
		t.Error("a dry run asked a question")
	}
	if _, err := allowAt(t, map[string]any{"id": r.ID}, false, true); err != nil {
		t.Fatal(err)
	}
	if q.count != 0 {
		t.Error("--yes asked a question")
	}
	if _, ok := consent.Find(r.ID); ok {
		t.Error("--yes did not answer the call")
	}
}

func TestWithoutATerminalAllowKeepsItsOldBehaviour(t *testing.T) {
	isolate(t)
	r := parkedRemoval(t)
	was := putQuestion
	t.Cleanup(func() { putQuestion = was })
	putQuestion = func(string, string) (string, error) {
		t.Error("a question was put with no terminal to read the answer")
		return "", nil
	}
	if _, err := allowAt(t, map[string]any{"id": r.ID}, false, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := consent.Find(r.ID); ok {
		t.Error("the call was not allowed")
	}
}

func TestTheQuestionSaysWhatAYesGoesBeyondTheOneCall(t *testing.T) {
	for _, c := range []struct{ role, ttl, want string }{
		{"", "", "Allow once?"},
		{"", "15m", "Allow this call, and keep allowing it for 15m?"},
		{"dev", "", "Allow this call, and issue the role dev to claude?"},
	} {
		if got := allowQuestion(c.role, c.ttl, "claude"); got != c.want {
			t.Errorf("role %q ttl %q: %q, want %q", c.role, c.ttl, got, c.want)
		}
	}
}

// Allowing releases the call, and the call then fails: the agent reads
// kv.passphrase.missing a minute after the operator said yes. Said before.
func TestAParkedKvCallWarnsWhenNothingHereOpensTheStore(t *testing.T) {
	isolate(t)
	// A home of its own: kv looks for a key file in the user's config
	// directory before it falls back to a passphrase, and a test must neither
	// read the real one nor have its answer depend on there being one.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("RTA_KV_IDENTITY", "")
	t.Setenv("RTA_KV_PASSPHRASE", "hunter2")
	if verr := kv.Store("db-password", "s3cret", "", "test"); verr != nil {
		t.Fatal(verr)
	}
	r := park(t, "kv.get", "db-password")

	if w := kvWarning(r); w != "" {
		t.Errorf("a shell that can open the store was warned: %q", w)
	}
	t.Setenv("RTA_KV_PASSPHRASE", "")
	w := kvWarning(r)
	if !strings.Contains(w, "kv.passphrase.missing") {
		t.Fatalf("no warning for a locked store and a shell with no passphrase: %q", w)
	}
	v, err := run(t, "agent.show", map[string]any{"id": r.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(pairValues(v.(view.Sections).Items[0].View.(view.KeyValue)), "\n"), w) {
		t.Error("agent show does not carry the warning")
	}
	q := terminal(t, "n")
	if _, err := allowAt(t, map[string]any{"id": r.ID}, false, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.card, w) {
		t.Errorf("the question does not carry the warning:\n%s", q.card)
	}
	if other := park(t, "note.rm", "3"); kvWarning(other) != "" {
		t.Error("a call that is not kv's was warned about the kv store")
	}
}

func pairValues(kv view.KeyValue) []string {
	out := make([]string, len(kv.Pairs))
	for i, p := range kv.Pairs {
		out[i] = p.Value
	}
	return out
}

func TestNoWarningWhenThereIsNoStoreToUnlock(t *testing.T) {
	isolate(t)
	t.Setenv("RTA_KV_PASSPHRASE", "")
	if w := kvWarning(park(t, "kv.get", "db-password")); w != "" {
		t.Errorf("no store, and a warning about unlocking it: %q", w)
	}
}

func TestAClosedInputEndsTheQuestionsLine(t *testing.T) {
	var out bytes.Buffer
	answer, err := askLine(strings.NewReader(""), &out, "card\n", "Allow once? [y/N] ")
	if err == nil || answer != "" {
		t.Fatalf("a closed input answered %q, %v", answer, err)
	}
	if got, want := out.String(), "card\nAllow once? [y/N] \n"; got != want {
		t.Errorf("wrote %q, want %q: what follows would run on from the question", got, want)
	}
	out.Reset()
	if _, err := askLine(strings.NewReader("y\n"), &out, "", "Q? "); err != nil || out.String() != "Q? " {
		t.Errorf("an answered question wrote %q, %v: no line of its own is added", out.String(), err)
	}
}
