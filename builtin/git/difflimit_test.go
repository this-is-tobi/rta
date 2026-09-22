package git

import (
	"strings"
	"testing"
)

// Both sides of a changed file are read whole to line-diff them, and nothing
// bounded either: a generated asset that changed put its whole size into the
// process twice. Over the bound the file is named in the diff rather than
// diffed, and the rest of the change is still shown.
func TestAChangedFileOverTheBoundIsNamedRatherThanRead(t *testing.T) {
	saved := maxDiffBytes
	maxDiffBytes = 64
	t.Cleanup(func() { maxDiffBytes = saved })

	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "small.txt", "v1\n", "initial")
	commitFile(t, repo, dir, "big.bin", "x\n", "asset")
	writeFile(t, dir, "small.txt", "v1\nv2\n")
	writeFile(t, dir, "big.bin", strings.Repeat("payload ", 40))

	body := text(t, runDiff, req(t, dir, nil))
	if !strings.Contains(body, "+v2") {
		t.Errorf("the small change is missing from the diff:\n%s", body)
	}
	if strings.Contains(body, "payload") {
		t.Errorf("the large file's content was read and diffed:\n%s", body)
	}
	if !strings.Contains(body, "big.bin changed, larger than") {
		t.Errorf("the large file is not named in the diff:\n%s", body)
	}
}
