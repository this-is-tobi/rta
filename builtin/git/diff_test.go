package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func TestDiffCommitShowsWhatThatCommitChanged(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "line one\n", "first")
	commitFile(t, repo, dir, "a.txt", "line one\nline two\n", "second")

	body := text(t, runDiff, req(t, dir, map[string]any{"commit": "master"}))
	if !strings.Contains(body, "+line two") {
		t.Errorf("diff does not show the added line:\n%s", body)
	}
	if !strings.Contains(body, "a.txt") {
		t.Errorf("diff does not name the changed file:\n%s", body)
	}
}

// The root commit is diffed against the empty tree, every file it holds
// shown added, as `git show` does.
//
// It answered a sentence saying it had no parent to diff against, so every
// format carried prose where a patch goes: `rta git diff --commit <root> >
// root.patch` wrote the sentence into the patch, and -o json handed it to a
// script as the diff of the one commit that changed the most.
func TestDiffOnTheRootCommitShowsEveryFileAdded(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "root")

	body := text(t, runDiff, req(t, dir, map[string]any{"commit": "master"}))
	for _, want := range []string{"diff --git a/a.txt b/a.txt", "new file mode", "+v1"} {
		if !strings.Contains(body, want) {
			t.Errorf("root commit's diff has no %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "root commit") {
		t.Errorf("root commit's diff is prose, not a patch: %q", body)
	}
}

// A commit whose patch is empty answers an empty patch, and says why only to
// a person: the sentence was the body, so `rta git diff --commit <empty> >
// x.patch` wrote it into the patch.
func TestDiffOfACommitThatChangedNothingIsAnEmptyPatch(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "first")
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	empty, err := wt.Commit("nothing", &git.CommitOptions{Author: signature(), AllowEmptyCommits: true})
	if err != nil {
		t.Fatal(err)
	}

	v, err := runDiff(context.Background(), req(t, dir, map[string]any{"commit": empty.String()}))
	if err != nil {
		t.Fatal(err)
	}
	txt, ok := v.(view.Text)
	if !ok || txt.Body != "" || !strings.Contains(txt.Empty, "changed nothing this can show") ||
		!strings.Contains(txt.Empty, shortHash(empty)) {
		t.Errorf("empty commit's diff = %#v, want an empty body and the sentence beside it", v)
	}
}

// A commit that only changes a file's mode is a patch, not the sentence: the
// sentence named a mode change as something go-git renders no patch for, and
// it renders one, `old mode` and `new mode`, as `git show` does.
func TestDiffOfAModeOnlyCommitIsAPatch(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "run.sh", "echo hi\n", "first")
	if err := os.Chmod(filepath.Join(dir, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("run.sh"); err != nil {
		t.Fatal(err)
	}
	mode, err := wt.Commit("executable", &git.CommitOptions{Author: signature()})
	if err != nil {
		t.Fatal(err)
	}

	body := text(t, runDiff, req(t, dir, map[string]any{"commit": mode.String()}))
	if !strings.Contains(body, "old mode 100644") || !strings.Contains(body, "new mode 100755") {
		t.Errorf("mode-only commit's diff = %q, want the mode change", body)
	}
}

func TestDiffWorktreeShowsUncommittedChanges(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "line one\n", "initial")
	writeFile(t, dir, "a.txt", "line one\nline two\n")

	body := text(t, runDiff, req(t, dir, nil))
	if !strings.Contains(body, "+line two") {
		t.Errorf("diff does not show the uncommitted addition:\n%s", body)
	}
}

// The files come in the order git prints them in, every time. The diff was
// built in the status map's iteration order, which Go randomises, so one
// working tree answered in a different order on every call.
func TestDiffWorktreeListsItsFilesInPathOrder(t *testing.T) {
	dir, repo := testRepo(t)
	names := []string{"a.txt", "b.txt", "b/c.txt", "d.txt", "e.txt", "f.txt", "g.txt", "h.txt"}
	for _, n := range names {
		commitFile(t, repo, dir, n, "v1\n", "add "+n)
		writeFile(t, dir, n, "v1\nv2\n")
	}

	var want []string
	for _, n := range names {
		want = append(want, "diff --git a/"+n+" b/"+n)
	}
	for range 5 {
		var got []string
		for _, line := range strings.Split(text(t, runDiff, req(t, dir, nil)), "\n") {
			if strings.HasPrefix(line, "diff --git ") {
				got = append(got, line)
			}
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("files in the diff:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}

// A clean tree's diff is an empty patch, and says so only to a person.
//
// The sentence was the body, so every format carried it: `rta git diff >
// x.patch` wrote "no uncommitted changes" into the patch, and -o json handed
// a script that sentence as the diff.
func TestDiffWorktreeOnACleanRepoIsAnEmptyPatch(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")

	v, err := runDiff(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	txt, ok := v.(view.Text)
	if !ok || txt.Body != "" || txt.Empty != "no uncommitted changes" {
		t.Errorf("clean diff = %#v, want an empty body and the sentence beside it", v)
	}
}

func TestDiffWorktreeShowsANewUntrackedFileAsEntirelyAdded(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, "new.txt", "brand new content\n")

	body := text(t, runDiff, req(t, dir, nil))
	if !strings.Contains(body, "+brand new content") {
		t.Errorf("diff does not show the new file's content as added:\n%s", body)
	}
	if !strings.Contains(body, "new file mode") {
		t.Errorf("diff does not mark new.txt as a new file:\n%s", body)
	}
}

func TestDiffWorktreeShowsADeletedFile(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "will be deleted\n", "initial")
	if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}

	body := text(t, runDiff, req(t, dir, nil))
	if !strings.Contains(body, "-will be deleted") {
		t.Errorf("diff does not show the deleted content:\n%s", body)
	}
	if !strings.Contains(body, "deleted file mode") {
		t.Errorf("diff does not mark a.txt as deleted:\n%s", body)
	}
}

// symlink makes a link at name, relative to dir, holding target as its text.
func symlink(t *testing.T, dir, target, name string) {
	t.Helper()
	full := filepath.Join(dir, name)
	_ = os.Remove(full)
	if err := os.Symlink(target, full); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
}

// git stops at a symlink on the way to a path: a tracked notes/a.txt whose
// directory became a link to other/ is deleted, as go-git's status says too.
// The diff read through the link instead, and showed other/a.txt's content
// as the change to notes/a.txt.
func TestDiffWorktreeDoesNotReadBehindADirectoryThatBecameALink(t *testing.T) {
	outside := t.TempDir()
	writeFile(t, outside, "a.txt", "outside the repository\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "notes/a.txt", "tracked\n", "notes")
	writeFile(t, dir, "other/a.txt", "other content\n")
	relink := func(t *testing.T, target string) {
		t.Helper()
		if err := os.RemoveAll(filepath.Join(dir, "notes")); err != nil {
			t.Fatal(err)
		}
		symlink(t, dir, target, "notes")
	}

	for name, r := range map[string]plugin.Request{
		"unconfined": req(t, dir, nil),
		"confined":   guarded(t, dir, dir),
	} {
		t.Run(name, func(t *testing.T) {
			relink(t, "other")
			body := text(t, runDiff, r)
			_, notes, _ := strings.Cut(body, "diff --git a/notes/a.txt b/notes/a.txt\n")
			notes, _, _ = strings.Cut(notes, "diff --git")
			if !strings.HasPrefix(notes, "deleted file mode 100644\n") || !strings.Contains(notes, "-tracked") {
				t.Errorf("notes/a.txt is not shown deleted, as git shows it:\n%s", body)
			}
			if strings.Contains(notes, "+other content") {
				t.Errorf("the diff read behind the link:\n%s", body)
			}
		})
	}

	// A link out of the root: whatever the gate makes of the path it leads
	// through, nothing behind it is read.
	relink(t, outside)
	if body := text(t, runDiff, guarded(t, dir, dir)); strings.Contains(body, "outside the repository") {
		t.Errorf("the diff read behind a link out of the root:\n%s", body)
	}
}

// git stores and diffs a symlink as the text it holds. The worktree diff read
// through one instead: a link retargeted from one file to another showed the
// second file's contents where git shows its name.
func TestDiffWorktreeShowsASymlinkByItsText(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "contents of a\n", "a")
	commitFile(t, repo, dir, "b.txt", "contents of b\n", "b")
	symlink(t, dir, "a.txt", "link")
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("link"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("link", &git.CommitOptions{Author: signature()}); err != nil {
		t.Fatal(err)
	}
	symlink(t, dir, "b.txt", "link")

	body := text(t, runDiff, req(t, dir, nil))
	for _, want := range []string{"-a.txt", "+b.txt", "120000"} {
		if !strings.Contains(body, want) {
			t.Errorf("the retargeted link's diff has no %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "contents of") {
		t.Errorf("the diff read through the link:\n%s", body)
	}
}

// A named pipe in the working tree is named, not opened. go-git's status
// lists one as untracked, and the diff opened it to read it: open(2) on a
// pipe with no writer blocks until one comes, which no context can
// interrupt, so git_diff never answered and each call held an OS thread for
// good. git does not track a pipe at all.
func TestDiffWorktreeNamesANamedPipeRatherThanWaitingOnIt(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, "a.txt", "v2\n")
	if err := mkfifo(filepath.Join(dir, "pipe")); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}

	type answer struct {
		v   view.View
		err error
	}
	done := make(chan answer, 1)
	go func() {
		v, err := runDiff(context.Background(), req(t, dir, nil))
		done <- answer{v, err}
	}()
	select {
	case a := <-done:
		if a.err != nil {
			t.Fatal(a.err)
		}
		body := a.v.(view.Text).Body
		if !strings.Contains(body, "+v2") {
			t.Errorf("the change beside the pipe is missing:\n%s", body)
		}
		if !strings.Contains(body, "pipe changed, not diffed: a named pipe") {
			t.Errorf("the pipe is not named in the diff:\n%s", body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the diff is waiting on a named pipe in the working tree")
	}
}

// One entry a diff cannot read through never costs the rest of it. An
// untracked link to a directory — bazel-out, a `current` pointing at a
// release — failed the whole diff, and so did one leading out of the
// repository, for files git diffs as one line each.
func TestDiffWorktreeShowsEveryOtherChangeBesideALinkItCannotFollow(t *testing.T) {
	outside := t.TempDir()
	writeFile(t, outside, "id_secret", "outside the repository\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, "a.txt", "v2\n")
	writeFile(t, dir, "d/f", "in d\n")
	symlink(t, dir, "d", "dirlink")
	symlink(t, dir, filepath.Join(outside, "id_secret"), "outlink")

	for name, r := range map[string]plugin.Request{
		"unconfined": req(t, dir, nil),
		"confined":   guarded(t, dir, dir),
	} {
		t.Run(name, func(t *testing.T) {
			body := text(t, runDiff, r)
			for _, want := range []string{"+v2", "+d\n", "+" + filepath.Join(outside, "id_secret")} {
				if !strings.Contains(body, want) {
					t.Errorf("the diff has no %q:\n%s", want, body)
				}
			}
			if strings.Contains(body, "outside the repository") {
				t.Errorf("the diff read through a link out of the repository:\n%s", body)
			}
		})
	}
}

// A submodule moved in the working tree is named by the commits it moved
// between, as a commit's diff names one and as git does, rather than as a
// directory where a file was.
func TestDiffWorktreeNamesAMovedSubmoduleByItsCommits(t *testing.T) {
	was := plumbing.NewHash("1111111111111111111111111111111111111111")
	now := plumbing.NewHash("2222222222222222222222222222222222222222")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "a\n", "initial")

	idx, err := repo.Storer.Index()
	if err != nil {
		t.Fatal(err)
	}
	link := idx.Add("lib")
	link.Mode, link.Hash = filemode.Submodule, was
	if err := repo.Storer.SetIndex(idx); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, dir, ".gitmodules",
		"[submodule \"lib\"]\n\tpath = lib\n\turl = https://example.invalid/lib.git\n", "lib at its first commit")
	cfg, err := repo.Config()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Submodules["lib"] = &config.Submodule{Name: "lib", Path: "lib", URL: "https://example.invalid/lib.git"}
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	module, err := git.PlainInit(filepath.Join(dir, ".git", "modules", "lib"), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := module.Storer.SetReference(plumbing.NewHashReference("refs/heads/master", now)); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "lib/.git", "gitdir: ../.git/modules/lib\n")

	body := text(t, runDiff, req(t, dir, nil))
	if !strings.Contains(body, "submodule lib 1111111 -> 2222222\n") {
		t.Errorf("the moved submodule is not named by its commits:\n%s", body)
	}
	if strings.Contains(body, "not diffed") {
		t.Errorf("the submodule is named as a file the diff could not read:\n%s", body)
	}
}

// linksToldAsUnder is a confined request whose surface tells a link's target
// as the MCP bridge tells it: as written where it names a place under root,
// and as leading outside otherwise (plugin.Request.LinkTarget).
func linksToldAsUnder(t *testing.T, root, path string) plugin.Request {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return guarded(t, root, path).WithSurface(plugin.SurfaceMCP).
		WithLinkTargets(func(dir, target string) string {
			if rel, err := filepath.Rel(resolved, against(dir, target)); err == nil && !climbsOut(rel) {
				return target
			}
			return "a path outside this server's roots"
		})
}

// A link's text is a name, and the worktree diff showed it whatever it named:
// a link inside the root holding a path outside it told an agent confined to
// the root a name outside it, which fs.tree withholds. Over MCP a link whose
// target names a place outside the roots is named, not shown; one naming a
// place under them is diffed by its text, as it is at a terminal.
func TestDiffWorktreeShowsALinkByItsTextOnlyWhereTheCallerMayReadTheName(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret-project-name")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "a\n", "initial")
	symlink(t, dir, "a.txt", "tracked")
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("tracked"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("link", &git.CommitOptions{Author: signature()}); err != nil {
		t.Fatal(err)
	}
	symlink(t, dir, secret, "tracked")
	symlink(t, dir, secret, "untracked")
	symlink(t, dir, "a.txt", "inside")

	body := text(t, runDiff, linksToldAsUnder(t, dir, dir))
	if strings.Contains(body, "secret-project-name") {
		t.Errorf("the diff told a name outside the roots:\n%s", body)
	}
	for _, want := range []string{
		"tracked changed, not diffed: a symbolic link to a path outside this server's roots",
		"untracked changed, not diffed: a symbolic link to a path outside this server's roots",
		"+a.txt",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the diff has no %q:\n%s", want, body)
		}
	}
	if body := text(t, runDiff, req(t, dir, nil)); !strings.Contains(body, "+"+secret) {
		t.Errorf("at a terminal, the diff does not show the link's text:\n%s", body)
	}
}
