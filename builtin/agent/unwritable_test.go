package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The overview is the screen an operator glances at, and a record that cannot
// be written is the state in which every call that needs a grant is refused:
// it is said there rather than found by an agent's refusal.
func TestTheOverviewSaysWhenTheRecordCannotBeWritten(t *testing.T) {
	isolate(t)
	if err := agentlog.Append(agentlog.Entry{Cap: "sys.cpu", Outcome: agentlog.Ran, Auth: agentlog.Open}); err != nil {
		t.Fatal(err)
	}
	v, err := run(t, "agent.overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range v.(view.KeyValue).Pairs {
		if p.Key == "recording" {
			t.Fatalf("a record that writes is reported as trouble: %q", p.Value)
		}
	}

	// The record reads and cannot take a write: the scratch file its writability
	// is tried with has a directory where it goes.
	if err := os.Mkdir(filepath.Join(filepath.Dir(agentlog.Path()), "agent-log.probe"), 0o700); err != nil {
		t.Fatal(err)
	}
	v, err = run(t, "agent.overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := overviewPair(t, v, "recording")
	if !strings.Contains(got, "cannot be written") || strings.Contains(got, agentlog.Path()) {
		t.Fatalf("recording = %q, want it to say the record cannot be written and name no path", got)
	}
}
