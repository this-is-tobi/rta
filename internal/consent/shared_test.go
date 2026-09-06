package consent

import (
	"os"
	"testing"
	"time"
)

// An agent retrying a parked call asked the same question again, up to the
// queue's cap. Inside one process the retry joins the first question: one
// row, one answer, every attempt released.
func TestARetryOfAParkedCallJoinsTheSameQuestion(t *testing.T) {
	isolate(t)
	first, err := Ask(aCall(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Ask(aCall(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second.Request.ID != first.Request.ID {
		t.Fatalf("the retry parked a second question: %s and %s", first.Request.ID, second.Request.ID)
	}
	if waiting, _ := Pending(); len(waiting) != 1 {
		t.Fatalf("%d questions waiting, want 1", len(waiting))
	}
	other := aCall()
	other.Scopes = []string{"another"}
	third, err := Ask(other, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	if third.Request.ID == first.Request.ID {
		t.Fatal("a different call joined the first question")
	}
	if err := Decide(first.Request.ID, true, "test"); err != nil {
		t.Fatal(err)
	}
	if a := first.Wait(t.Context()); !a.Answered || !a.Allowed {
		t.Fatalf("first attempt got %+v", a)
	}
	// The first attempt is done and gone; the files stay for the second.
	first.Close()
	if _, err := os.Stat(requestPath(first.Request.ID)); err != nil {
		t.Fatal("the first attempt's Close removed the question the second still waits on")
	}
	if a := second.Wait(t.Context()); !a.Answered || !a.Allowed {
		t.Fatalf("second attempt got %+v", a)
	}
	second.Close()
	if _, err := os.Stat(requestPath(first.Request.ID)); err == nil {
		t.Fatal("the last attempt out left the files behind")
	}
	if _, err := Ask(aCall(), time.Minute); err != nil {
		t.Fatal(err)
	}
}
