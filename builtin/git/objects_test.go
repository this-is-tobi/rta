package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A repository whose objects this reader cannot all see is refused, not
// answered.
//
// go-git finds packfiles by name and by nothing else: ObjectPacks keeps only
// `pack-<hash>.pack` and takes the hash from the filename itself. git makes
// no such promise. `git maintenance run --task=loose-objects` — which `git
// maintenance start` schedules, so a great many working repositories have
// run it — writes its pack as `loose-<hash>.pack`, and every object inside
// one is simply absent as far as this reader is concerned.
//
// Absent, and not an error, which is the whole problem: a tree that will not
// load reads as a tree that was never there. On this project's own checkout
// that turned a clean working tree into `git status` reporting three hundred
// and seventy-five files as newly staged, and it would truncate a log or a
// diff the same quiet way. Wrong-and-plausible about the state of somebody's
// repository is the one answer a boundary must not give.
func TestARepositoryWithAPackTheReaderSkipsIsRefused(t *testing.T) {
	dir, repo := testRepo(t)
	// A subdirectory, because the silent form of this needs a tree that
	// loads pointing at one that does not. With every object invisible the
	// reader fails outright and says so, which is the harmless shape; the
	// shape that shipped a wrong answer is a readable commit whose subtree
	// is in the pack nobody opens.
	commitFile(t, repo, dir, "sub/x.txt", "one\n", "initial")
	if err := repo.RepackObjects(&git.RepackConfig{}); err != nil {
		t.Fatal(err)
	}
	dropLooseObjects(t, dir)
	// The second commit writes a new root tree and a new commit as loose
	// objects, and reuses the subtree from the pack. Renaming the pack after
	// it is what leaves exactly the real arrangement: HEAD readable, sub/
	// not.
	commitFile(t, repo, dir, "top.txt", "two\n", "second")
	renamePackTheWayMaintenanceDoes(t, dir)

	v, err := runStatus(context.Background(), req(t, dir, nil))
	if err == nil {
		t.Fatalf("a repository whose objects are half invisible answered anyway: %+v", v)
	}
	verr := view.AsError(err, "git.status.failed")
	if verr.Code != "git.objects.unreadable" {
		t.Errorf("code = %q, want git.objects.unreadable (%v)", verr.Code, verr.Message)
	}
	// The one command that fixes it, since the operator cannot be expected to
	// know that a packfile's name is load-bearing.
	if !strings.Contains(verr.Hint, "git repack") {
		t.Errorf("hint = %q, want it to name the repack that renames the pack", verr.Hint)
	}
	// Named, because a repository can hold several and the operator is owed
	// the evidence rather than an assertion about their machine.
	if !strings.Contains(verr.Message, "loose-") {
		t.Errorf("message = %q, want it to name the packfile it will not open", verr.Message)
	}
}

// The three capabilities whose answers never come out of a packfile keep
// answering.
//
// `git config` reads .git/config, `git hooks` reads .git/hooks, and
// `git remotes` reads the refs and the configured remotes. A half-readable
// object database cannot make any of those wrong, so refusing them would
// report a fault in an answer that does not have one — the same discipline
// as the profile badge that stays silent unless it has something to say.
func TestTheCapabilitiesThatReadNoObjectsStillAnswer(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "sub/x.txt", "one\n", "initial")
	if err := repo.RepackObjects(&git.RepackConfig{}); err != nil {
		t.Fatal(err)
	}
	dropLooseObjects(t, dir)
	commitFile(t, repo, dir, "top.txt", "two\n", "second")
	renamePackTheWayMaintenanceDoes(t, dir)

	for name, h := range map[string]func(context.Context, plugin.Request) (view.View, error){
		"git.config":  runConfig,
		"git.hooks":   runHooks,
		"git.remotes": runRemotes,
	} {
		if _, err := h(context.Background(), req(t, dir, nil)); err != nil {
			t.Errorf("%s refused a repository whose objects it never reads: %v", name, err)
		}
	}
}

// And the guard stays off the ordinary repository, which is the half that
// makes it worth having: a pack under the name go-git expects is read, and
// packing objects is not itself a fault.
func TestAnOrdinarilyPackedRepositoryIsStillAnswered(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "hello\n", "initial")
	if err := repo.RepackObjects(&git.RepackConfig{}); err != nil {
		t.Fatal(err)
	}
	dropLooseObjects(t, dir)

	tbl := table(t, runStatus, req(t, dir, nil))
	if len(tbl.Rows) != 0 {
		t.Errorf("rows = %v, want none — the checkout is clean and its pack is readable", tbl.Rows)
	}
}

// dropLooseObjects removes every loose object, leaving the packs as the only
// place the repository's history lives — which is the state `git maintenance`
// leaves behind, and the state in which a skipped pack costs real objects.
func dropLooseObjects(t *testing.T, dir string) {
	t.Helper()
	objects := filepath.Join(dir, ".git", "objects")
	entries, err := os.ReadDir(objects)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		// The two-hex fan-out directories, and nothing else: pack/ and info/
		// are not loose objects.
		if !e.IsDir() || len(e.Name()) != 2 {
			continue
		}
		if err := os.RemoveAll(filepath.Join(objects, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
}

// renamePackTheWayMaintenanceDoes gives the packs the name `git maintenance
// run --task=loose-objects` gives the one it writes.
func renamePackTheWayMaintenanceDoes(t *testing.T, dir string) {
	t.Helper()
	packDir := filepath.Join(dir, ".git", "objects", "pack")
	entries, err := os.ReadDir(packDir)
	if err != nil {
		t.Fatal(err)
	}
	renamed := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "pack-") {
			continue
		}
		to := "loose-" + strings.TrimPrefix(name, "pack-")
		if err := os.Rename(filepath.Join(packDir, name), filepath.Join(packDir, to)); err != nil {
			t.Fatal(err)
		}
		renamed++
	}
	if renamed == 0 {
		t.Fatal("no pack to rename — the repack did not produce one")
	}
}
