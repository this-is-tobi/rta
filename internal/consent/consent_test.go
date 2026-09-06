package consent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)
	return dir
}

func aCall() Call {
	return Call{
		Cap: "kv.get", Safety: "write", Scopes: []string{"db-password"},
		Args: map[string]any{"key": "db-password"}, Why: "no active grant for kv.get db-password",
	}
}

// distinct hands out calls that differ in the record they name, for the
// tests about slots: an identical retry joins the question already parked
// and takes no slot of its own.
func distinct() func() Call {
	var mu sync.Mutex
	n := 0
	return func() Call {
		mu.Lock()
		defer mu.Unlock()
		n++
		c := aCall()
		c.Scopes = []string{fmt.Sprintf("record-%d", n)}
		c.Args = map[string]any{"key": c.Scopes[0]}
		return c
	}
}

func TestAnAnsweredRequestUnparksTheCall(t *testing.T) {
	isolate(t)
	parked, err := Ask(aCall(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()

	// The operator, in their own process, sees it and says yes.
	pending, err := Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("Pending: %v %d", err, len(pending))
	}
	if pending[0].Cap != "kv.get" || pending[0].Scopes[0] != "db-password" {
		t.Fatalf("the operator would read the wrong call: %+v", pending[0])
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		if err := Decide(parked.Request.ID, true, "cli"); err != nil {
			t.Errorf("Decide: %v", err)
		}
	}()
	answer := parked.Wait(context.Background())
	if !answer.Answered || !answer.Allowed {
		t.Fatalf("answer = %+v, want an allow", answer)
	}
}

func TestADenialIsAnAnswerAndNotATimeout(t *testing.T) {
	isolate(t)
	parked, err := Ask(aCall(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()
	if err := Decide(parked.Request.ID, false, "cli"); err != nil {
		t.Fatal(err)
	}
	answer := parked.Wait(context.Background())
	if !answer.Answered {
		t.Fatal("a denial read as nobody answering")
	}
	if answer.Allowed {
		t.Fatal("a denial allowed the call")
	}
}

func TestNobodyAnsweringIsNotADenialButRefusesAnyway(t *testing.T) {
	isolate(t)
	parked, err := Ask(aCall(), 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()
	start := time.Now()
	answer := parked.Wait(context.Background())
	if answer.Answered || answer.Allowed {
		t.Fatalf("answer = %+v, want nobody home", answer)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the deadline was not honoured")
	}
}

func TestCancellingTheCallStopsWaiting(t *testing.T) {
	isolate(t)
	parked, err := Ask(aCall(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	done := make(chan Answer, 1)
	go func() { done <- parked.Wait(ctx) }()
	select {
	case a := <-done:
		if a.Answered {
			t.Fatalf("a cancelled wait reported an answer: %+v", a)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Wait ignored the cancelled context")
	}
}

func TestARewrittenRequestCannotRedirectAnApproval(t *testing.T) {
	// The attack the digest exists for: a local write changes what the
	// operator is shown while a different call waits. Their approval then
	// names the displayed call, which nothing is waiting on.
	isolate(t)
	parked, err := Ask(aCall(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()

	raw, err := os.ReadFile(requestPath(parked.Request.ID))
	if err != nil {
		t.Fatal(err)
	}
	var shown Request
	if err := json.Unmarshal(raw, &shown); err != nil {
		t.Fatal(err)
	}
	// Rewritten to look harmless, digest included — the attacker controls
	// the whole file.
	shown.Cap = "sys.cpu"
	shown.Scopes = nil
	shown.Args = nil
	shown.Digest = Call{Cap: "sys.cpu"}.Digest()
	body, _ := json.MarshalIndent(shown, "", "  ")
	if err := os.WriteFile(requestPath(shown.ID), body, 0o600); err != nil {
		t.Fatal(err)
	}

	// The operator approves what they were shown. Two acceptable endings, and
	// this test deliberately accepts either: rta now refuses to answer a
	// request whose display and digest disagree (Request.Honest), and if that
	// filter is ever loosened the decision it writes still names the displayed
	// call, which nothing is waiting on. The attacker gets nowhere by the
	// second route because they rewrote only part of the file — the variant
	// where the whole display is made self-consistent is tamper_test.go's.
	if err := Decide(shown.ID, true, "cli"); err == nil {
		answer := parked.Wait(context.Background())
		if answer.Allowed {
			t.Fatal("an approval of a rewritten display released a different call")
		}
		if answer.Answered {
			t.Fatal("a mismatched digest was treated as an answer")
		}
	}
}

func TestAForgedDecisionIsIgnored(t *testing.T) {
	// The write-only attacker, one mechanism along: a confined
	// plugin drops an approval for itself, having never read the key.
	isolate(t)
	parked, err := Ask(aCall(), 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()

	forged := decision{
		ID: parked.Request.ID, Digest: parked.Request.Digest, Allow: true,
		At: time.Now().UTC(), Seal: "not the real mac",
	}
	body, _ := json.Marshal(forged)
	if err := os.WriteFile(decisionPath(parked.Request.ID), body, 0o600); err != nil {
		t.Fatal(err)
	}
	answer := parked.Wait(context.Background())
	if answer.Allowed || answer.Answered {
		t.Fatalf("a forged decision was honoured: %+v", answer)
	}
}

func TestADecisionFromAnotherKeyIsIgnored(t *testing.T) {
	// The same attack with more effort: a plausible-looking seal computed
	// under a key the attacker generated themselves.
	dir := isolate(t)
	parked, err := Ask(aCall(), 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()
	if err := Decide(parked.Request.ID, true, "cli"); err != nil {
		t.Fatal(err)
	}
	// Swap the key out from under the seal that was just written.
	other := make([]byte, 32)
	for i := range other {
		other[i] = byte(i + 1)
	}
	if err := os.WriteFile(filepath.Join(dir, keyFile), other, 0o600); err != nil {
		t.Fatal(err)
	}
	answer := parked.Wait(context.Background())
	if answer.Allowed || answer.Answered {
		t.Fatalf("a decision sealed under a different key was honoured: %+v", answer)
	}
}

func TestTheDigestCoversWhatIsBeingApproved(t *testing.T) {
	base := aCall()
	for _, tc := range []struct {
		name   string
		change func(*Call)
	}{
		{"a different record", func(c *Call) { c.Scopes = []string{"prod-password"} }},
		{"a different capability", func(c *Call) { c.Cap = "kv.rm" }},
		{"a different profile", func(c *Call) { c.Profile = "prod" }},
		{"a repointed connection", func(c *Call) { c.Pin = "beefbeef" }},
		{"a different argument", func(c *Call) { c.Args = map[string]any{"key": "other"} }},
		{"an extra argument", func(c *Call) { c.Args = map[string]any{"key": "db-password", "out": "/tmp/x"} }},
	} {
		changed := base
		changed.Scopes = append([]string(nil), base.Scopes...)
		changed.Args = map[string]any{}
		for k, v := range base.Args {
			changed.Args[k] = v
		}
		tc.change(&changed)
		if changed.Digest() == base.Digest() {
			t.Errorf("%s did not change the digest, so an approval would carry across it", tc.name)
		}
	}
	// And the same call twice is the same digest, whatever the map order.
	again := Call{
		Cap: "kv.get", Safety: "write", Scopes: []string{"db-password"},
		Args: map[string]any{"key": "db-password"}, Why: "different words entirely",
	}
	if again.Digest() != base.Digest() {
		t.Fatal("the same call digests differently — the display text must not be part of the binding")
	}
}

func TestPendingSweepsWhatNobodyIsWaitingFor(t *testing.T) {
	isolate(t)
	parked, err := Ask(aCall(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Age it well past the grace period by rewriting its deadline.
	raw, _ := os.ReadFile(requestPath(parked.Request.ID))
	var r Request
	json.Unmarshal(raw, &r)
	r.Deadline = time.Now().Add(-2 * time.Minute)
	body, _ := json.Marshal(r)
	os.WriteFile(requestPath(r.ID), body, 0o600)

	pending, err := Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("a long-expired request is still listed: %+v", pending)
	}
	if _, err := os.Stat(requestPath(r.ID)); !os.IsNotExist(err) {
		t.Fatal("the expired request was listed away but left on disk")
	}
}

func TestAFloodOfQuestionsIsCappedRatherThanQueued(t *testing.T) {
	// Consent fatigue is the attack on any ask-the-human control: a queue
	// nobody can read is a queue that gets cleared, and every answer after
	// that is a reflex. The cap is what keeps the list readable.
	isolate(t)
	next := distinct()
	var parked []*Parked
	for i := 0; i < MaxParked; i++ {
		p, err := Ask(next(), time.Minute)
		if err != nil {
			t.Fatalf("request %d of %d was refused: %v", i+1, MaxParked, err)
		}
		parked = append(parked, p)
	}
	defer func() {
		for _, p := range parked {
			p.Close()
		}
	}()
	if _, err := Ask(next(), time.Minute); !errors.Is(err, ErrTooMany) {
		t.Fatalf("the %dth request was accepted: %v", MaxParked+1, err)
	}
	// Answering one makes room for the next: the cap bounds the queue, and
	// does not lock the operator out of a server they are keeping up with.
	parked[0].Close()
	parked = parked[1:]
	p, err := Ask(next(), time.Minute)
	if err != nil {
		t.Fatalf("a closed slot was not reused: %v", err)
	}
	parked = append(parked, p)
}

func TestABurstCannotOutrunTheCap(t *testing.T) {
	// The version of the cap that counted first and wrote after passed the
	// sequential test above and let ten pipelined calls park all ten: the
	// go-sdk dispatches each tools/call in its own goroutine, so a bound
	// that only holds when calls arrive one at a time is not a bound. This
	// is the same shape as grant.Reserve's, and it is the shape a queue cap
	// has to be tested in.
	isolate(t)
	next := distinct()
	const burst = MaxParked * 3
	var (
		mu     sync.Mutex
		parked []*Parked
		full   int
		start  = make(chan struct{})
		wg     sync.WaitGroup
	)
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			p, err := Ask(next(), time.Minute)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, ErrTooMany):
				full++
			case err != nil:
				t.Errorf("Ask: %v", err)
			default:
				parked = append(parked, p)
			}
		}()
	}
	close(start)
	wg.Wait()
	defer func() {
		for _, p := range parked {
			p.Close()
		}
	}()

	if len(parked) != MaxParked {
		t.Fatalf("%d of %d simultaneous calls parked, cap is %d", len(parked), burst, MaxParked)
	}
	if full != burst-MaxParked {
		t.Fatalf("%d calls were told the queue was full, want %d", full, burst-MaxParked)
	}
	// And the operator's list agrees with what the callers were told: a cap
	// enforced in memory and not on disk would leave the questions behind.
	waiting, err := Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != MaxParked {
		t.Fatalf("the operator would read %d questions, cap is %d", len(waiting), MaxParked)
	}
}

func TestAnExpiredRequestDoesNotHoldASlot(t *testing.T) {
	// The failure this avoids: a burst of requests nobody answered, still
	// inside Pending's grace period, keeping the queue full for the one
	// call the operator is actually waiting to approve.
	isolate(t)
	next := distinct()
	for i := 0; i < MaxParked; i++ {
		p, err := Ask(next(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		// Age it past its deadline without reaching the sweep, which is where
		// a slot would otherwise stay taken.
		raw, _ := os.ReadFile(requestPath(p.Request.ID))
		var r Request
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		r.Deadline = time.Now().Add(-10 * time.Second)
		body, _ := json.Marshal(r)
		if err := os.WriteFile(requestPath(r.ID), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Ask(next(), time.Minute)
	if err != nil {
		t.Fatalf("expired requests held the queue shut: %v", err)
	}
	p.Close()
}

func TestDecidingSomethingUnknownSaysSo(t *testing.T) {
	isolate(t)
	if err := Decide("nosuchid", true, "cli"); err == nil {
		t.Fatal("deciding an unknown request succeeded")
	}
}

func TestCloseRemovesBothFiles(t *testing.T) {
	isolate(t)
	parked, err := Ask(aCall(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := Decide(parked.Request.ID, true, "cli"); err != nil {
		t.Fatal(err)
	}
	parked.Close()
	for _, p := range []string{requestPath(parked.Request.ID), decisionPath(parked.Request.ID)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s survived Close", p)
		}
	}
}
