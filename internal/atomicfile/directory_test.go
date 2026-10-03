package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A directory where a file goes is not a handle somebody is about to close:
// waiting resolves nothing, and Replace waited out all of its retries for it.
// The record's end mark is replaced after every call an agent makes, so a
// directory left at its name cost each call nearly a second.
func TestAReplaceOntoADirectoryFailsAtOnce(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mark.json")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	err := Write(target, []byte("{}"), 0o600)
	took := time.Since(started)

	if err == nil {
		t.Fatal("a file was written over a directory")
	}
	if took > 300*time.Millisecond {
		t.Errorf("the refusal took %v: a directory is waited out like a handle that will be closed", took)
	}
	if info, statErr := os.Stat(target); statErr != nil || !info.IsDir() {
		t.Errorf("the directory is gone: %v", statErr)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the temporary file was left behind: %v", entries)
	}
}
