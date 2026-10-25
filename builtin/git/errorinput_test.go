package git

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// refusedInput is the code of err and the input it names, held to the
// declaration of the capability that answered it: a name the capability does
// not declare is one no surface acts on (plugin.Capability.ErrorInput), so it
// fails here rather than passing as a stamp that does nothing.
func refusedInput(t *testing.T, id string, err error) (code, input string) {
	t.Helper()
	verr := view.AsError(err, "test")
	if verr == nil {
		t.Fatalf("%s answered no error", id)
	}
	if verr.Input != "" {
		var declared bool
		for _, c := range Plugin().Capabilities {
			if c.ID == id {
				_, declared = c.ErrorInput(verr)
			}
		}
		if !declared {
			t.Errorf("%s: %s names the input %q, which %s does not declare", id, verr.Code, verr.Input, id)
		}
	}
	return verr.Code, verr.Input
}

// A refusal of the one input a call got wrong names it, so the form the call
// came from opens again on that box and an agent knows the argument to send
// again: a path that is no repository, or one with no working tree where a
// status needs one; a file outside the repository, not a file, not tracked or
// too large to blame; a first line past the file's end; a revision that names
// no commit.
//
// What no one box holds the fix for names none: an empty repository has no
// history to blame whatever is asked of it, a bare repository's diff is
// answered by a commit or by a checkout, and a remote over MCP is the policy's
// no, not a value's.
func TestARefusalOfOneInputNamesIt(t *testing.T) {
	saved := maxDiffBytes
	maxDiffBytes = 64
	t.Cleanup(func() { maxDiffBytes = saved })

	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "one\ntwo\n", "initial")
	commitFile(t, repo, dir, "deploy/values.yaml", "replicas: 2\n", "deploy")
	commitFile(t, repo, dir, "big.txt", strings.Repeat("line\n", 40), "big")
	outside := t.TempDir()
	bare := t.TempDir()
	if _, err := git.PlainInit(bare, true); err != nil {
		t.Fatal(err)
	}
	empty, _ := testRepo(t)
	in := func(name string) string { return filepath.Join(dir, name) }

	runs := map[string]plugin.Handler{
		"git.status": runStatus, "git.log": runLog, "git.blame": runBlame, "git.diff": runDiff,
	}
	for _, c := range []struct {
		name, id, path string
		values         map[string]any
		surface        plugin.Surface
		code           string
		input          string
	}{
		{"a directory in no repository", "git.status", outside, nil,
			plugin.SurfaceTUI, "git.notarepo", "path"},
		{"a directory that is not there", "git.log", filepath.Join(outside, "missing"), nil,
			plugin.SurfaceCLI, "git.path.notfound", "path"},
		{"a bare repository's status", "git.status", bare, nil,
			plugin.SurfaceTUI, "git.status.worktree", "path"},
		{"a log of a file outside the repository", "git.log", dir,
			map[string]any{"file": filepath.Join(outside, "notes.txt")},
			plugin.SurfaceCLI, "git.file.outside", "file"},
		{"a blame of a file outside the repository", "git.blame", dir,
			map[string]any{"file": filepath.Join(outside, "notes.txt")},
			plugin.SurfaceTUI, "git.file.outside", "file"},
		{"a blame of a directory", "git.blame", dir, map[string]any{"file": in("deploy")},
			plugin.SurfaceTUI, "git.blame.isdir", "file"},
		{"a blame of a file never committed", "git.blame", dir, map[string]any{"file": in("nope.txt")},
			plugin.SurfaceCLI, "git.blame.failed", "file"},
		{"a blame of a file over the bound", "git.blame", dir, map[string]any{"file": in("big.txt")},
			plugin.SurfaceCLI, "git.blame.toolarge", "file"},
		{"a first line past the end", "git.blame", dir, map[string]any{"file": in("a.txt"), "from": 3},
			plugin.SurfaceTUI, "git.blame.range", "from"},
		{"a name nothing goes by", "git.diff", dir, map[string]any{"commit": "zzz"},
			plugin.SurfaceTUI, "git.diff.unresolved", "commit"},
		{"a count past the first commit", "git.diff", dir, map[string]any{"commit": "HEAD~99"},
			plugin.SurfaceCLI, "git.diff.unresolved", "commit"},
		{"a blame in an empty repository", "git.blame", empty, map[string]any{"file": "a.txt"},
			plugin.SurfaceCLI, "git.blame.nohead", ""},
		{"a bare repository's diff", "git.diff", bare, nil,
			plugin.SurfaceCLI, "git.diff.worktree", ""},
		{"a remote over MCP", "git.status", "https://example.invalid/repo.git", nil,
			plugin.SurfaceMCP, "git.remote.mcp", ""},
	} {
		_, err := runs[c.id](context.Background(), req(t, c.path, c.values).WithSurface(c.surface))
		code, input := refusedInput(t, c.id, err)
		if code != c.code || input != c.input {
			t.Errorf("%s: %s naming %q, want %s naming %q (%v)", c.name, code, input, c.code, c.input, err)
		}
	}
}
