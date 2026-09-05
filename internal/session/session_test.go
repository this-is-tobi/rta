package session

import (
	"os"
	"testing"
	"time"
)

func TestAnOpenServerIsListedAndADeadOneIsForgotten(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	live := Record{ID: NewID(), Agent: "claude", Client: "Claude Code 2.1", Since: time.Now(), PID: os.Getpid(), Dir: "/work"}
	if err := Start(live); err != nil {
		t.Fatal(err)
	}
	// A pid the OS will not have handed out again in the life of this test.
	dead := Record{ID: NewID(), Agent: "cursor", Since: time.Now().Add(-time.Hour), PID: 1 << 30}
	if err := Start(dead); err != nil {
		t.Fatal(err)
	}
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != live.ID || got[0].Client != "Claude Code 2.1" {
		t.Fatalf("List = %+v, want only the live server", got)
	}
	if _, err := os.Stat(path(dead.ID)); !os.IsNotExist(err) {
		t.Error("the dead server's file was not removed")
	}
	if err := End(live.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := List(); len(got) != 0 {
		t.Errorf("after End, List = %+v", got)
	}
	if err := End("never-started"); err != nil {
		t.Errorf("ending a session that never started must be quiet: %v", err)
	}
}

func TestNothingRecordedIsAnEmptyListNotAnError(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	got, err := List()
	if err != nil || len(got) != 0 {
		t.Fatalf("List = %v, %v", got, err)
	}
	if len(NewID()) != 8 {
		t.Errorf("id = %q, want eight hex characters", NewID())
	}
}
