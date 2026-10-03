package app

import (
	"os"
	"testing"

	"github.com/this-is-tobi/rta/internal/agentlog"
)

// A record that cannot be written refuses every call that needs a grant, so
// it is an error in the health check — found here, not by an agent's refusal
// — and it names whose the fix is.
func TestDoctorReportsARecordThatCannotBeWritten(t *testing.T) {
	isolate(t)
	if err := agentlog.Append(agentlog.Entry{Cap: "sys.cpu", Outcome: agentlog.Ran, Auth: agentlog.Open}); err != nil {
		t.Fatal(err)
	}
	check(t, report(t), "agent log", "ok", "1 agent call recorded")

	if err := os.Remove(agentlog.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(agentlog.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	check(t, report(t), "agent log", "error", "cannot be written")
}

// A machine nothing has been recorded on is not a machine with a broken
// record, and looking must not create one.
func TestDoctorDoesNotCreateARecordToCheckIt(t *testing.T) {
	dataDir, _ := isolate(t)
	report(t)
	if _, err := os.Stat(agentlog.Path()); err == nil {
		t.Fatalf("doctor created %s", agentlog.Path())
	}
	if entries, _ := os.ReadDir(dataDir); len(entries) > 0 {
		for _, e := range entries {
			if e.Name() == "agent-log.key" {
				t.Fatalf("doctor minted the record's key")
			}
		}
	}
}
