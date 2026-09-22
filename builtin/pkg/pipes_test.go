package pkg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A manager that forks something and exits must not wedge the table.
//
// The child inherits this process's end of stdout, and with WaitDelay unset
// Wait blocks until every pipe sees EOF — forever, past listTimeout, because
// what is stuck is os/exec's copying goroutines rather than the process the
// deadline killed. The real runner is exercised here, not the fake the other
// tests install: the bound is a property of the real one.
func TestTheRealRunnerDoesNotWaitForAnOrphanedChild(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "manager")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n(sleep 30) &\necho answer\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var (
		out  string
		code int
		verr *view.Error
	)
	go func() {
		defer close(done)
		out, code, verr = run(context.Background(), script)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("run never returned: the manager exited and nothing bounded the wait for pipes an orphan is still holding")
	}
	// The answer survives the bound: the manager exited zero with it printed,
	// and ErrWaitDelay is not a reason to report a failure.
	if verr != nil || code != 0 || strings.TrimSpace(out) != "answer" {
		t.Errorf("out %q code %d verr %v, want the answer as a clean success", out, code, verr)
	}
}

// A --package beginning with a dash would reach the manager as a flag; it is
// refused before anything runs.
func TestAPackageBeginningWithADashIsRefusedBeforeAnythingRuns(t *testing.T) {
	f := &fake{bins: map[string]bool{"brew": true}}
	install(t, f)
	_, err := runUpgradeCapability(context.Background(), req(t, "pkg.upgrade", map[string]any{"target": "brew", "package": "--help"}))
	ve := view.AsError(err, "x")
	if ve == nil || ve.Code != "pkg.upgrade.package" {
		t.Fatalf("err = %v, want pkg.upgrade.package", err)
	}
	if len(f.upgrade) != 0 {
		t.Errorf("something ran: %v", f.upgrade)
	}
}
