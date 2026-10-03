package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/agentlog"
)

// A server whose config does not read still starts and still answers, with a
// refusal for every capability a profile could change; readiness says so, with
// the file's own reason, and ends when the file reads again.
func TestReadinessFailsWhileTheConfigDoesNotRead(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)
	cfg := filepath.Join(dir, "config.yaml")
	t.Setenv("RTA_CONFIG", cfg)
	if err := serverReady(); err != nil {
		t.Fatalf("no config at all reported not ready: %v", err)
	}
	if err := os.WriteFile(cfg, []byte("profiles: [this is: not : yaml\n  - {\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := serverReady()
	if err == nil || !contains(err.Error(), "profile could change is refused") {
		t.Fatalf("a config that does not parse reported ready, or without its cost: %v", err)
	}
	if err := os.WriteFile(cfg, []byte("output: json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := serverReady(); err != nil {
		t.Fatalf("a config put right is still reported not ready: %v", err)
	}
}

// A directory that takes a file is not a record that takes an append, and a
// server whose record is something else — a directory, a file with its key
// gone — refuses every call that needs a grant. Readiness asks the same
// question, so the orchestrator stops sending traffic to it instead of leaving
// the operator to find out from an agent's refusal.
func TestReadinessFailsWhenTheRecordIsNotSomethingAnAppendGoesTo(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if err := agentlog.Append(agentlog.Entry{Cap: "sys.cpu", Outcome: agentlog.Ran, Auth: agentlog.Open}); err != nil {
		t.Fatal(err)
	}
	if err := recordWritable(); err != nil {
		t.Fatalf("a record that takes writes reported not ready: %v", err)
	}

	if err := os.Remove(agentlog.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(agentlog.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	err := recordWritable()
	if err == nil {
		t.Fatal("a directory where the record goes reported ready")
	}
	if !contains(err.Error(), "needs a grant is refused") {
		t.Errorf("the reason does not say what it costs: %v", err)
	}
}
