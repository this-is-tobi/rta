package git

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// grow makes path size bytes long, as a sparse file where the filesystem
// keeps one: what planting one costs its writer.
func grow(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// go-git reads a repository's config, index, HEAD and packed-refs whole, or
// decodes the whole of them into memory, on every capability, and nothing
// bounded them: a sparse config of three gigabytes, which costs no disk,
// cost one git.config 7.2 GiB, and a planted index cost git.status 9.2 GiB.
// Past its bound each is refused by name, before anything reads it, on the
// capabilities that read objects and on the ones that do not.
func TestAFileGoGitReadsWholeIsRefusedPastItsBound(t *testing.T) {
	for name, limit := range map[string]int64{
		"config": maxConfigBytes, "index": maxIndexBytes, "packed-refs": maxPackedRefsBytes, "HEAD": maxPointerBytes,
	} {
		t.Run(name, func(t *testing.T) {
			dir, repo := testRepo(t)
			commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
			grow(t, filepath.Join(dir, ".git", name), limit+1)
			for capability, run := range map[string]plugin.Handler{"git.status": runStatus, "git.config": runConfig} {
				_, err := run(context.Background(), req(t, dir, nil))
				if code := errCode(err); code != "git.repository.toolarge" {
					t.Errorf("%s over a %s past its bound: %q, want git.repository.toolarge", capability, name, code)
				}
			}
		})
	}
}

// A working tree's .gitmodules is read whole by go-git's status, and parsed
// as config, and it is a file any commit can carry: one made sparse at 768 MiB
// cost one git.status 2.9 GiB. Past the config's bound it is refused by name
// before anything reads it, and the open reads no further than the bound.
func TestAGitmodulesPastTheConfigsBoundIsRefused(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, ".gitmodules", "[submodule \"x\"]\n\tpath = x\n\turl = ../x\n")
	for capability, run := range map[string]plugin.Handler{"git.status": runStatus, "git.diff": runDiff} {
		if _, err := run(context.Background(), req(t, dir, nil)); err != nil {
			t.Fatalf("%s over a .gitmodules within its bound: %v", capability, err)
		}
	}

	worktree := regularFiles{Filesystem: osfs.New(dir)}
	f, err := worktree.Open(".gitmodules")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	grow(t, filepath.Join(dir, ".gitmodules"), maxConfigBytes+1)
	if _, err := io.ReadAll(f); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("a .gitmodules grown past its bound after the open was read: %v", err)
	}
	for capability, run := range map[string]plugin.Handler{"git.status": runStatus, "git.diff": runDiff} {
		_, err := run(context.Background(), req(t, dir, nil))
		if code := errCode(err); code != "git.repository.toolarge" || !strings.Contains(err.Error(), ".gitmodules") {
			t.Errorf("%s over a .gitmodules past its bound: %v, want git.repository.toolarge naming it", capability, err)
		}
	}
}

// The open refuses the same files again and reads no further than the
// bound, whatever the file has become by the time it is read: the check
// before the open looks at a name, and a name can be swapped. A working
// tree's own file named config is somebody's file, and is read whole.
func TestAFileGoGitReadsWholeIsReadNoFurtherThanItsBound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config", "[core]\n")
	gitDir := regularFiles{Filesystem: osfs.New(dir), gitDir: true}
	f, err := gitDir.Open("config")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	grow(t, filepath.Join(dir, "config"), maxConfigBytes+10)
	if _, err := io.ReadAll(f); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("a config grown past its bound after the open was read: %v", err)
	}
	if _, err := gitDir.Open("config"); err == nil {
		t.Error("a config past its bound was opened")
	}

	worktree := regularFiles{Filesystem: osfs.New(dir)}
	g, err := worktree.Open("config")
	if err != nil {
		t.Fatalf("a working tree's file named config was refused: %v", err)
	}
	defer func() { _ = g.Close() }()
	if b, err := io.ReadAll(g); err != nil || int64(len(b)) != maxConfigBytes+10 {
		t.Errorf("a working tree's file named config was not read whole: %d bytes, %v", len(b), err)
	}
}

// The operator's own config files are read with the same reader, whole, and
// are held to the same bound.
func TestAMachineConfigPastTheBoundIsRefused(t *testing.T) {
	home := machineConfig(t, "")
	grow(t, filepath.Join(home, ".gitconfig"), maxConfigBytes+1)
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	_, err := runConfig(context.Background(), req(t, dir, nil))
	if code := errCode(err); code != "git.config.failed" || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("git.config over a ~/.gitconfig past the bound: %v, want git.config.failed saying why", err)
	}
}

// config.worktree is read whole too, and only where the repository's config
// asks git to read it, so its bound is met where it is read: a git.config
// fails naming it, and a repository whose config does not ask is not
// refused for it.
func TestAWorktreeConfigPastTheBoundFailsTheCallThatReadsIt(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	grow(t, filepath.Join(dir, ".git", "config.worktree"), maxConfigBytes+1)
	if _, err := runConfig(context.Background(), req(t, dir, nil)); err != nil {
		t.Fatalf("a config.worktree git does not read failed git.config: %v", err)
	}
	writeFile(t, dir, ".git/config", "[core]\n\trepositoryformatversion = 0\n\tbare = false\n"+
		"[extensions]\n\tworktreeConfig = true\n")
	_, err := runConfig(context.Background(), req(t, dir, nil))
	if code := errCode(err); code != "git.config.failed" || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("git.config over a config.worktree past the bound: %v, want git.config.failed saying why", err)
	}
}

// nameSwappedBack is a filesystem whose look at any name finds regular, a
// regular file: what a caller who swapped a pipe in for the open, and the
// file back straight after it, leaves the name saying to a look that follows
// the open.
type nameSwappedBack struct {
	billy.Filesystem
	regular os.FileInfo
}

func (n nameSwappedBack) Stat(string) (os.FileInfo, error) { return n.regular, nil }

// What the repository's filesystem opened is judged by the open file's own
// Stat, not by a look at its name after the open: a pipe opened in HEAD's
// place, with the file put back before the look, was read as the file —
// non-blocking, and holding the call for as long as anything held the pipe's
// other end. Under a root the open file can say what it is (rootFile), and
// it is asked.
func TestWhatTheRepositorysFilesystemOpenedIsJudgedByTheOpenFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "real", "ref: refs/heads/main\n")
	if err := mkfifo(filepath.Join(dir, "HEAD")); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	regular, err := os.Stat(filepath.Join(dir, "real"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	under := rootFiles{dir: boundDir{path: dir, root: root}, files: &repoFiles{}}
	gitDir := regularFiles{Filesystem: nameSwappedBack{Filesystem: under, regular: regular}, gitDir: true}
	f, err := gitDir.Open("HEAD")
	if err == nil {
		_ = f.Close()
		t.Fatal("a pipe was opened as the regular file its name said it was after the open")
	}
	if !errors.Is(err, errNotAFile) {
		t.Errorf("err = %v, want errNotAFile", err)
	}
}
