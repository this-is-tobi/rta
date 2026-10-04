package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

// A directory where a file goes is refused, and what is refused leaves nothing
// behind: the directory is still there and no temporary file sits beside it.
// The record's end mark is written after every call an agent makes, so a
// directory left at its name is a state a call has to survive.
func TestAWriteOntoADirectoryIsRefusedAndLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mark.json")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := Write(target, []byte("{}"), 0o600); err == nil {
		t.Fatal("a file was written over a directory")
	}
	if info, statErr := os.Stat(target); statErr != nil || !info.IsDir() {
		t.Errorf("the directory is gone: %v", statErr)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the temporary file was left behind: %v", entries)
	}
}
