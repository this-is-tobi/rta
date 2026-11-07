package grant

import (
	"context"
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// asked is a command-line request with somebody there to answer: every card it
// is shown is kept, and answer is what they say to it.
func asked(values map[string]any, answer func(card string) bool) plugin.Request {
	return plugin.NewRequest(values, false, false).WithSurface(plugin.SurfaceCLI).WithConfirmer(answer)
}

// **A grant on a whole plugin was issued first and explained afterwards.** `grant
// allow kv` reads like one grant and is kv.rm and kv.get among the rest, and the
// lines that said so came in the receipt, with the clock already running. At a
// terminal they come first, and nothing stands until the person says yes.
func TestAWholePluginGrantAsksBeforeAnythingIsIssued(t *testing.T) {
	setup(t)
	var cards []string
	stood := -1
	r := asked(map[string]any{"target": "kv", "agent": "test"}, func(card string) bool {
		cards = append(cards, card)
		stood = len(standing(t))
		return true
	})
	if _, err := runApprove(context.Background(), r, storeCatalog, builtIn); err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("asked %d times, want once", len(cards))
	}
	if stood != 0 {
		t.Errorf("%d grants stood while the question was open", stood)
	}
	for _, want := range []string{"would let test call kv for 15m", "1 destructive (kv.rm)", "reveal stored values (kv.env, kv.get)"} {
		if !strings.Contains(cards[0], want) {
			t.Errorf("the question does not say %q:\n%s", want, cards[0])
		}
	}
	if gs := standing(t); len(gs) != 1 || gs[0].Target != "kv" {
		t.Errorf("a yes left %+v, want the grant asked about", gs)
	}
}

// Anything but yes issues nothing, and ends as the destructive prompt's no does,
// so the command line exits 3 and a `&&` chain stops there.
func TestANoToTheQuestionIssuesNothing(t *testing.T) {
	setup(t)
	r := asked(map[string]any{"target": "kv", "agent": "test"}, func(string) bool { return false })
	_, err := runApprove(context.Background(), r, storeCatalog, builtIn)
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "core.confirm.required" {
		t.Fatalf("err = %v, want core.confirm.required", err)
	}
	if !strings.Contains(verr.Hint, "name one capability of kv") {
		t.Errorf("the hint reads %q, want the narrower grant", verr.Hint)
	}
	if gs := standing(t); len(gs) != 0 {
		t.Errorf("a no left %+v standing", gs)
	}
}

// The question is for a grant whose reach the person may not have read, and only
// where somebody can answer it: everything else is issued as it always was.
func TestOnlyAWideGrantWithSomebodyToAnswerIsAsked(t *testing.T) {
	quiet := func() []plugin.Capability {
		return []plugin.Capability{{ID: "note.add", Safety: plugin.Write}, {ID: "note.list", Safety: plugin.Read}}
	}
	for name, tc := range map[string]struct {
		catalog func() []plugin.Capability
		req     func(map[string]any, func(string) bool) plugin.Request
		target  string
		issued  bool
	}{
		"with --yes": {storeCatalog, func(v map[string]any, a func(string) bool) plugin.Request {
			return plugin.NewRequest(v, false, true).WithSurface(plugin.SurfaceCLI).WithConfirmer(a)
		}, "kv", true},
		"on a dry run": {storeCatalog, func(v map[string]any, a func(string) bool) plugin.Request {
			return plugin.NewRequest(v, true, false).WithSurface(plugin.SurfaceCLI).WithConfirmer(a)
		}, "kv", false},
		"with nobody to ask": {storeCatalog, func(v map[string]any, _ func(string) bool) plugin.Request {
			return plugin.NewRequest(v, false, false).WithSurface(plugin.SurfaceCLI)
		}, "kv", true},
		"on one capability":                             {storeCatalog, asked, "kv.rm", true},
		"on a plugin that neither destroys nor reveals": {quiet, asked, "note", true},
	} {
		t.Run(name, func(t *testing.T) {
			setup(t)
			r := tc.req(map[string]any{"target": tc.target, "agent": "test"}, func(card string) bool {
				t.Errorf("asked:\n%s", card)
				return false
			})
			if _, err := runApprove(context.Background(), r, tc.catalog, builtIn); err != nil {
				t.Fatal(err)
			}
			if got := len(standing(t)) == 1; got != tc.issued {
				t.Errorf("issued = %v, want %v", got, tc.issued)
			}
		})
	}
}

// The window starts at the yes: the receipt says "for 15m (until …)", and a
// person who read the card for a minute was otherwise handed fourteen.
func TestAWindowStartsWhenThePersonSaysYes(t *testing.T) {
	setup(t)
	const reading = 30 * time.Millisecond
	var answered time.Time
	r := asked(map[string]any{"target": "kv", "agent": "test"}, func(string) bool {
		time.Sleep(reading)
		answered = time.Now()
		return true
	})
	if _, err := runApprove(context.Background(), r, storeCatalog, builtIn); err != nil {
		t.Fatal(err)
	}
	if answered.IsZero() {
		t.Fatal("nobody was asked")
	}
	gs := standing(t)
	if len(gs) != 1 {
		t.Fatalf("held %+v", gs)
	}
	if gs[0].Issued.Before(answered) {
		t.Errorf("issued at %s, before the yes at %s", gs[0].Issued, answered)
	}
	if got := gs[0].Expires.Sub(gs[0].Issued); got != core.DefaultTTL {
		t.Errorf("the window is %s wide, want %s", got, core.DefaultTTL)
	}
}

// With the guard on, the question comes before the passphrase: a no costs none,
// and a yes goes on to the guard, which here has no terminal to read it from.
func TestTheQuestionComesBeforeTheGuardsPassphrase(t *testing.T) {
	setup(t)
	guardOn(t, "correct horse battery staple")
	for answer, want := range map[bool]string{false: "core.confirm.required", true: "core.guard.passphrase.required"} {
		r := asked(map[string]any{"target": "kv", "agent": "test"}, func(string) bool { return answer })
		_, err := runApprove(context.Background(), r, storeCatalog, builtIn)
		if verr, ok := err.(*view.Error); !ok || verr.Code != want {
			t.Errorf("answered %v: err = %v, want %s", answer, err, want)
		}
	}
}
