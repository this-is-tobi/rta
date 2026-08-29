package consent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// The package comment makes one claim above all others: "the request is a
// display; the digest is the binding", and specifically that a local attacker
// who rewrites a request file so it reads `sys.cpu` while a `kv.get` waits
// "changes only what the operator is shown".
//
// These are the tests for that sentence. Every field a person reads while
// deciding is a field somebody else can write, because a request is
// deliberately not sealed — so the only thing that can make the sentence true
// is the deciding side deriving the digest from what it displayed, rather
// than copying one out of the file it was handed.

// doctor rewrites the request file the way an attacker with a write and no
// read would: the display becomes something harmless, the id stays (it names
// the file), and the digest is left as it was found.
func doctor(t *testing.T, id string, edit func(*Request)) {
	t.Helper()
	raw, err := os.ReadFile(requestPath(id))
	if err != nil {
		t.Fatal(err)
	}
	var r Request
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	edit(&r)
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requestPath(id), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestARewrittenRequestCannotAuthorizeTheCallItHides(t *testing.T) {
	// The whole attack in one test. A dangerous call parks; its display is
	// rewritten into a harmless one; the operator reads the harmless one and
	// says yes. If that yes reaches the dangerous call, the consent prompt is
	// decoration — the operator authorized `sys.cpu` and `kv.get db-password`
	// ran.
	isolate(t)
	parked, err := Ask(aCall(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()

	doctor(t, parked.Request.ID, func(r *Request) {
		r.Cap = "sys.cpu"
		r.Safety = "read"
		r.Scopes = nil
		r.Args = map[string]any{}
		r.Why = "reading the processor temperature"
		r.Preview = "would report the current CPU load"
	})

	// What the operator is shown is already a lie, and that much is expected:
	// nothing seals a request. What must not happen is the lie authorizing
	// anything.
	if err := Decide(parked.Request.ID, true, "cli"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if answer := parked.Wait(ctx); answer.Allowed {
			t.Fatal("the operator approved sys.cpu and kv.get db-password ran: " +
				"the display and the binding came out of the same file and only the display was checked")
		}
	}
}

func TestADoctoredRequestIsNotEvenOffered(t *testing.T) {
	// Refusing at Decide would be enough for the call, and not enough for the
	// person: a request whose display does not match its digest is a request
	// nobody should be reading, and it has to be gone from every surface at
	// once rather than from each one that remembers to ask.
	isolate(t)
	parked, err := Ask(aCall(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()
	doctor(t, parked.Request.ID, func(r *Request) { r.Cap = "sys.cpu" })

	pending, err := Pending()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range pending {
		if r.ID == parked.Request.ID {
			t.Fatalf("a request whose display does not match its digest is still on the queue: %+v", r)
		}
	}
	if _, ok := Find(parked.Request.ID); ok {
		t.Fatal("Find still hands out the doctored request")
	}
}

func TestEveryDisplayedFieldIsPartOfTheBinding(t *testing.T) {
	// The check above is only worth as much as the set of fields it covers.
	// A field that is displayed while deciding and left out of the digest is
	// a field an attacker may rewrite freely, so each one is edited on its own
	// here — a digest that quietly stopped covering args, say, would still
	// pass the test above as long as the capability was covered.
	//
	// Why and Preview are deliberately absent from this list: both are prose
	// *about* the call rather than part of it, and both are checked by the two
	// tests below instead.
	edits := map[string]func(*Request){
		"capability": func(r *Request) { r.Cap = "sys.cpu" },
		"safety":     func(r *Request) { r.Safety = "read" },
		"record":     func(r *Request) { r.Scopes = []string{"something-harmless"} },
		"no record":  func(r *Request) { r.Scopes = nil },
		"connection": func(r *Request) { r.Profile = "staging" },
		"pin":        func(r *Request) { r.Pin = "0000" },
		"argument":   func(r *Request) { r.Args = map[string]any{"key": "something-harmless"} },
		"no args":    func(r *Request) { r.Args = nil },
		// The cheapest thing a blind writer produces: a file with no binding
		// in it at all. An absent digest has to fail the check rather than
		// skip it, or the whole mechanism is opt-in for anybody willing to
		// leave a field out.
		"no digest": func(r *Request) { r.Digest = "" },
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			isolate(t)
			parked, err := Ask(aCall(), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer parked.Close()
			doctor(t, parked.Request.ID, edit)
			if _, ok := Find(parked.Request.ID); ok {
				t.Fatalf("rewriting the %s left the request answerable", name)
			}
			if err := Decide(parked.Request.ID, true, "cli"); err == nil {
				t.Fatalf("rewriting the %s left the request decidable", name)
			}
		})
	}
}

func TestAnEnormousRequestFileIsNotReadIntoMemory(t *testing.T) {
	// This directory's whole threat model is that somebody else can write
	// here, and every surface that shows the queue walks it — including
	// Ask(), which every parked call goes through. An unbounded ReadFile on
	// a file an attacker chose the size of is a way to take the operator's
	// terminal, or the server, out with one write.
	isolate(t)
	parked, err := Ask(aCall(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()

	// A *valid* request, padded past the bound. Junk bytes would prove
	// nothing: they fail to parse and get skipped whether or not a size
	// bound exists, so a test built on them passes against code that has
	// none. This one is real JSON, agrees with its own digest, and is
	// therefore offered by any version of Scan that will read it.
	second, err := Ask(aCall(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	doctor(t, second.Request.ID, func(r *Request) {
		r.Preview = strings.Repeat("x", maxRequest)
	})

	q, err := Scan()
	if err != nil {
		t.Fatalf("one oversized file made the whole queue unreadable: %v", err)
	}
	for _, r := range q.Waiting {
		if r.ID == second.Request.ID {
			t.Fatal("a request larger than the reader accepts was read and offered anyway")
		}
	}
	// And the genuine one beside it is untouched: a bad file must not take
	// the queue down with it.
	if len(q.Waiting) != 1 || q.Waiting[0].ID != parked.Request.ID {
		t.Fatalf("waiting = %+v, want the one request that is still readable", q.Waiting)
	}
}

func TestRtaNeverWritesARequestItWillNotReadBack(t *testing.T) {
	// The self-inflicted version of the same bound, and the reason it needs
	// its own test: no attacker is involved. A preview is a capability's own
	// dry-run output with nothing capping its length, so an enthusiastic one
	// would park a call that no surface lists and nobody can answer — it
	// would simply wait out its deadline while the operator saw nothing.
	isolate(t)
	c := aCall()
	c.Preview = strings.Repeat("would remove a great many things. ", maxRequest/10)
	parked, err := Ask(c, 5*time.Second)
	if err != nil {
		t.Fatalf("a long preview made the call unparkable: %v", err)
	}
	defer parked.Close()

	pending, err := Pending()
	if err != nil || len(pending) != 1 || pending[0].ID != parked.Request.ID {
		t.Fatalf("rta wrote a request it cannot read back: %v %+v", err, pending)
	}
	// Trimmed rather than refused, because a preview is prose about the call
	// and not part of it — and the operator is told that is what happened.
	if got := pending[0].Preview; got != "" {
		t.Fatalf("the oversized preview survived: %d bytes", len(got))
	}
	if !strings.Contains(pending[0].Why, "too long to show") {
		t.Fatalf("nothing tells the operator something was left out: %q", pending[0].Why)
	}
	// The call itself is unchanged, so the request still binds what it bound.
	if !pending[0].Honest() {
		t.Fatal("trimming prose changed what the request is bound to")
	}
	if err := Decide(parked.Request.ID, true, "cli"); err != nil {
		t.Fatalf("the trimmed request cannot be answered: %v", err)
	}
}

func TestACallTooLargeToShowIsRefusedRatherThanShownInPart(t *testing.T) {
	// The other side of the split. Arguments are part of what is authorized,
	// so they cannot be trimmed to fit — a prompt showing a fraction of what
	// is being approved is worse than no prompt, because it is believed.
	isolate(t)
	c := aCall()
	c.Args = map[string]any{"key": strings.Repeat("k", maxRequest)}
	if _, err := Ask(c, 5*time.Second); !errors.Is(err, ErrTooBig) {
		t.Fatalf("err = %v, want ErrTooBig", err)
	}
	pending, _ := Pending()
	if len(pending) != 0 {
		t.Fatalf("something was parked anyway: %+v", pending)
	}
}

func TestTheProseAroundTheCallIsStillFreeToChange(t *testing.T) {
	// The other half of the rule, and the reason the check is a recomputation
	// rather than a seal. Why and Preview are written by the asking process
	// and are not part of what is authorized; rta must not start refusing
	// requests because of them, or every wording change becomes a broken
	// queue.
	for _, tc := range []struct {
		name string
		edit func(*Request)
	}{
		{"why", func(r *Request) { r.Why = "different words entirely" }},
		{"preview", func(r *Request) { r.Preview = "would do something else" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			parked, err := Ask(aCall(), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer parked.Close()
			doctor(t, parked.Request.ID, tc.edit)
			if _, ok := Find(parked.Request.ID); !ok {
				t.Fatal("editing prose about the call made the call unanswerable")
			}
			if err := Decide(parked.Request.ID, true, "cli"); err != nil {
				t.Fatalf("editing prose about the call made it undecidable: %v", err)
			}
		})
	}
}
