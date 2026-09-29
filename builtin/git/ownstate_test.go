package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// rta's own state is refused by name wherever it sits, and a name is not
// what git opens: a hard link under a root to a file of it, or the file
// itself moved onto a name the gate judged, leaves every name the gate sees
// outside the data directory. Over MCP what git opens is held to the state's
// identity as well, and refused as the name is.

const stateSecret = "AGE-SECRET-KEY-1BYANYOTHERNAME"

// withBounds is the request an MCP call arrives as, confined to root with
// the bounds the bridge gives it — the gate and the bounds of one guard,
// made before anything in the test moves.
func withBounds(t *testing.T, root, path string, values map[string]any) plugin.Request {
	t.Helper()
	g, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return req(t, path, values).WithConfinement(g.Check).WithBounds(g.Bounds()).WithSurface(plugin.SurfaceMCP)
}

// stateAt puts rta's data directory at data, holding name with the secret in
// it, and its configuration out of the way; the file's path comes back.
func stateAt(t *testing.T, data, name string) string {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", data)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	writeFile(t, data, name, stateSecret+"\n")
	return filepath.Join(data, name)
}

func linkOrSkip(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Link(target, at); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
}

// An untracked hard link in a checkout to rta's age identity was diffed
// whole, as the new file it looked like by its name; it is named as the gate
// refuses it, and the rest of the diff is still the answer.
func TestADiffDoesNotShowAHardLinkToRtasOwnState(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, ".zshrc", "export A=1\n", "dotfiles")
	identity := stateAt(t, filepath.Join(t.TempDir(), "data"), "kv.identity")
	linkOrSkip(t, identity, filepath.Join(dir, "cache.bin"))
	writeFile(t, dir, ".zshrc", "export A=2\n")

	body := text(t, runDiff, withBounds(t, dir, dir, nil))
	if strings.Contains(body, stateSecret) {
		t.Fatalf("the diff showed rta's own state through a hard link:\n%s", body)
	}
	if want := "cache.bin changed, not diffed: the path gate refuses it (core.mcp.path.protected)"; !strings.Contains(body, want) {
		t.Errorf("the link is not named as refused:\n%s", body)
	}
	if !strings.Contains(body, "+export A=2") {
		t.Errorf("the change the caller may read is missing:\n%s", body)
	}
	// A person at a terminal reads their own files, by any name.
	if !strings.Contains(text(t, runDiff, req(t, dir, nil)), stateSecret) {
		t.Error("an unconfined diff withheld a file from the person it belongs to")
	}
}

// A file of rta's state moved onto a tracked file's name, before the diff
// looked, is that file for as long as it is there; the name was the gate's to
// judge and the file is not the caller's to read.
func TestADiffDoesNotShowRtasOwnStateMovedOntoATrackedFile(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "notes.txt", "a note\n", "notes")
	identity := stateAt(t, filepath.Join(dir, ".local", "share", "rta"), "kv.identity")
	r := withBounds(t, dir, dir, nil)
	if err := os.Rename(identity, filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	body := text(t, runDiff, r)
	if strings.Contains(body, stateSecret) {
		t.Fatalf("the diff showed rta's own state moved onto a tracked file:\n%s", body)
	}
	if want := "notes.txt changed, not diffed: the path gate refuses it (core.mcp.path.protected)"; !strings.Contains(body, want) {
		t.Errorf("the moved file is not named as refused:\n%s", body)
	}
}

// A versioned home directory holds rta's data directory untracked, and the
// bounds that refuse what is opened in it do not refuse the walk that lists
// it: git.status answers, naming what is there, and git.diff names each file
// as the gate refuses it. Refusing the directory itself failed go-git's walk
// of the working tree, and every status and diff of the home directory with
// it.
func TestAVersionedHomeHoldingRtasStateStillAnswersUnderBounds(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, ".zshrc", "export A=1\n", "dotfiles")
	stateAt(t, filepath.Join(dir, ".local", "share", "rta"), "kv.identity")
	writeFile(t, dir, ".zshrc", "export A=2\n")

	status := fmt.Sprint(table(t, runStatus, withBounds(t, dir, dir, nil)).Rows)
	if !strings.Contains(status, "kv.identity") || !strings.Contains(status, ".zshrc") {
		t.Errorf("the status did not name what changed: %s", status)
	}
	body := text(t, runDiff, withBounds(t, dir, dir, nil))
	if strings.Contains(body, stateSecret) || !strings.Contains(body, "+export A=2") {
		t.Errorf("the diff showed rta's state, or not the change the caller may read:\n%s", body)
	}
	if want := "kv.identity changed, not diffed: the path gate refuses it (core.mcp.path.protected)"; !strings.Contains(body, want) {
		t.Errorf("rta's state is not named as refused:\n%s", body)
	}
}

// A file an include names is read as config, and a hard link inside the roots
// to one of rta's own files was read as the repository's: over MCP it
// refuses the call, as an include naming the file itself does.
func TestAnIncludeOfAHardLinkToRtasOwnStateIsRefusedOverMCP(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	planted := stateAt(t, filepath.Join(t.TempDir(), "data"), "planted.cfg")
	writeFile(t, filepath.Dir(planted), "planted.cfg", "[x]\n\tplanted = "+stateSecret+"\n")
	linkOrSkip(t, planted, filepath.Join(dir, ".git", "linked.cfg"))
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\tpath = linked.cfg\n")
	for capability, run := range map[string]plugin.Handler{"git.config": runConfig, "git.hooks": runHooks} {
		v, err := run(context.Background(), withBounds(t, dir, dir, nil))
		if code := errCode(err); code != "core.mcp.path.protected" || strings.Contains(fmt.Sprint(v), stateSecret) {
			t.Errorf("%s over MCP = %v %v, want core.mcp.path.protected", capability, v, err)
		}
	}
	if keys := keyRows(table(t, runConfig, req(t, dir, nil)), "x.planted"); len(keys) != 1 {
		t.Errorf("at a terminal, x.planted = %v, want the included file's value", keys)
	}
}

// rta's data directory moved into the working tree by another name — one
// rename, by a caller who can write in the home directory holding it — was
// listed, and a file rta wrote in it after the server started was diffed
// whole as a new file: the guard knows such a file only by where the
// directory was. git sees nothing of the directory now, and the status and
// the diff still answer for the rest of the tree.
func TestADataDirectoryMovedIntoTheWorkingTreeShowsNothingOfIt(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, ".zshrc", "export A=1\n", "dotfiles")
	data := filepath.Join(dir, ".local", "share", "rta")
	stateAt(t, data, "kv.identity")
	// The requests the diff and the status arrive as, each with a guard made
	// while the directory was where rta keeps it, as the server's was.
	r, rs := withBounds(t, dir, dir, nil), withBounds(t, dir, dir, nil)
	writeFile(t, data, "grants.json", stateSecret+" written since\n")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(data, later, later); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(data, filepath.Join(dir, "backup")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, ".zshrc", "export A=2\n")

	body := text(t, runDiff, r)
	if strings.Contains(body, stateSecret) || strings.Contains(body, "backup/") {
		t.Errorf("the diff showed rta's data directory moved into the tree:\n%s", body)
	}
	if !strings.Contains(body, "+export A=2") {
		t.Errorf("the change the caller may read is missing:\n%s", body)
	}
	status := fmt.Sprint(table(t, runStatus, rs).Rows)
	if strings.Contains(status, "backup/") || !strings.Contains(status, ".zshrc") {
		t.Errorf("the status listed the moved directory, or not the change beside it: %s", status)
	}
}
