package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `git diff` of an empty file added or removed is its header and nothing
// after: there is no hunk for `--- /dev/null` and `+++ b/x` to name the sides
// of. The working-tree diff wrote them anyway, the commit's wrote a `Binary
// files differ` line that is false of a file with no content, and `git apply`
// refuses a patch of either for it.
func TestAnEmptyFileAddedOrRemovedIsAHeaderAlone(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "keep.txt", "kept\n", "first")
	commitFile(t, repo, dir, "gone.txt", "", "an empty file")
	writeFile(t, dir, "empty.txt", "")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}

	inTree := text(t, runDiff, req(t, dir, nil))
	for _, want := range []string{
		"diff --git a/empty.txt b/empty.txt\nnew file mode 100644\n",
		"diff --git a/gone.txt b/gone.txt\ndeleted file mode 100644\n",
	} {
		if !strings.Contains(inTree, want) {
			t.Errorf("the working-tree diff does not hold %q:\n%s", want, inTree)
		}
	}
	for _, line := range []string{"--- /dev/null", "+++ b/empty.txt", "--- a/gone.txt", "+++ /dev/null", "Binary files"} {
		if strings.Contains(inTree, line) {
			t.Errorf("the working-tree diff of an empty file holds %q:\n%s", line, inTree)
		}
	}

	commitFile(t, repo, dir, "second.txt", "", "another empty file")
	inCommit := text(t, runDiff, req(t, dir, map[string]any{"commit": "master"}))
	if !strings.Contains(inCommit, "diff --git a/second.txt b/second.txt\nnew file mode 100644\nindex ") {
		t.Errorf("the commit's diff has no header for the empty file:\n%s", inCommit)
	}
	for _, line := range []string{"--- /dev/null", "+++ b/second.txt", "Binary files"} {
		if strings.Contains(inCommit, line) {
			t.Errorf("the commit's diff of an empty file holds %q:\n%s", line, inCommit)
		}
	}
}
