package consent

import (
	"strings"
	"testing"
	"time"
)

// Between the answer and the asker's next poll the request file is still
// on disk. It used to stay in the queue for that window, looking
// answerable, and a second answer "succeeded" too.
func TestAnAnsweredRequestLeavesTheQueueBeforeTheAskerCollectsIt(t *testing.T) {
	isolate(t)
	parked, err := Ask(aCall(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()
	if err := Decide(parked.Request.ID, true, "test"); err != nil {
		t.Fatal(err)
	}
	// Not yet collected: the asker has not polled, so both files stand.
	waiting, err := Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 0 {
		t.Fatalf("an answered request is still listed: %+v", waiting)
	}
	if _, ok := Find(parked.Request.ID); ok {
		t.Fatal("an answered request is still findable as waiting")
	}
	err = Decide(parked.Request.ID, false, "test")
	if err == nil || !strings.Contains(err.Error(), "already answered") {
		t.Fatalf("a second answer: %v, want a refusal naming the first", err)
	}
	// And the first answer is the one the asker gets.
	if a := parked.Wait(t.Context()); !a.Answered || !a.Allowed {
		t.Fatalf("the caller got %+v, want the first answer", a)
	}
}
