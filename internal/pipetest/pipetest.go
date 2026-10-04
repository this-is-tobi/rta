// Package pipetest is what a test needs to show that a file rta reads is
// never waited on when something else has put a named pipe where it goes.
//
// open(2) on a FIFO waits for a writer and no context reaches into the
// syscall, so a read that opens a planted pipe holds its caller for good —
// and each one pins an OS thread. A test of that has to be able to give up on
// its own read, release the blocked open so no goroutine outlives the test,
// and say what was waited on; four packages needed exactly that.
package pipetest

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// Plant puts a named pipe at path, skipping the test where the filesystem has
// none.
func Plant(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
}

// Returns runs read, which opens the pipe at path, and fails the test if it
// has not returned within a few seconds. The blocked open is let go before the
// failure, so the goroutine does not outlive the test.
func Returns(t *testing.T, path, what string, read func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		read()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		if w, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close()
		}
		t.Fatalf("%s waited on a named pipe for a writer", what)
	}
}
