package pathguard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tildeTest is the shape of a leading-tilde check: HasPrefix against "~" or
// "~/", or a direct comparison with "~". Paired with os.UserHomeDir in the
// same file, it is a home expansion, and paired with nothing it is something
// else — `audit.agents` abbreviates a resolved path back down to "~" for
// display, and `mcp install` prints "~/.cursor/mcp.json" as the name of a
// file rather than resolving it. Neither is flagged, and neither should be.
var tildeTest = regexp.MustCompile(`HasPrefix\([^,]+,\s*"~/?"\)|[!=]= "~"`)

// ExpandTilde is the only place a leading ~ becomes a home directory.
//
// **Five other packages had grown their own, and two of them were wrong.**
// builtin/kv and builtin/cert handled "~/x" and not a bare "~", so `rta kv
// set --file ~` and `rta cert inspect ~` looked for a file literally named
// "~" while `rta fs usage ~` and the TUI's path box resolved it — the same
// gesture, answered differently depending on which command a person had
// reached for. builtin/cert's copy sat in a file that already imported this
// package.
//
// The divergence was predicted in writing: builtin/keys' copy carried the
// comment "Duplicated from builtin/kv/crypt.go ... two built-ins, ten lines,
// no third caller yet to justify the seam." The third caller arrived, and
// then the fourth and fifth, and nothing went back to collapse them. This is
// what goes back.
//
// ~user is deliberately not supported here and must not be added by a copy
// elsewhere either — see ExpandTilde's own doc for why.
func TestOnlyPathguardExpandsALeadingTilde(t *testing.T) {
	root := repoRoot(t)
	self := filepath.Join("internal", "pathguard")

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "testdata" || name == "examples" || name == "proto" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		if strings.HasPrefix(rel, self) {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		src := string(body)
		if strings.Contains(src, "os.UserHomeDir()") && tildeTest.MatchString(src) {
			t.Errorf("%s expands a leading tilde itself — call pathguard.ExpandTilde, "+
				"which handles a bare ~ and says why ~user is left alone", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the test's working directory")
		}
		dir = parent
	}
}
