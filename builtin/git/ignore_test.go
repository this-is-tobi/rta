package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/util"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"

	"github.com/this-is-tobi/rta/builtin/internal/gitclone"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// lowerIgnoreBounds holds one status to the ignore files a test can write
// quickly.
func lowerIgnoreBounds(t *testing.T, bytes int64, patterns int) {
	t.Helper()
	oldBytes, oldPatterns := maxIgnoreBytes, maxIgnorePatterns
	maxIgnoreBytes, maxIgnorePatterns = bytes, patterns
	t.Cleanup(func() { maxIgnoreBytes, maxIgnorePatterns = oldBytes, oldPatterns })
}

// ignoreWarning is the status's warning about the ignore files it did not
// apply, nil where there is none.
func ignoreWarning(tbl view.Table) *view.Error {
	for i, w := range tbl.Warnings {
		if w.Code == "git.status.ignore" {
			return &tbl.Warnings[i]
		}
	}
	return nil
}

// Ignore files are read whole, every pattern kept and every directory and
// every change matched against all of them, and nothing bounded them: a
// planted one of a hundred thousand patterns cost a status of two thousand
// paths 15 s. Past the bytes one status reads, an ignore file is not applied,
// as git does not apply one past 100 MB, and the status says so: what it
// ignores is listed as untracked, and a warning names it.
func TestAnIgnoreFilePastTheBytesAStatusReadsIsNotAppliedAndNamed(t *testing.T) {
	lowerIgnoreBounds(t, 1024, 100)
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, "kept.log", "log\n")
	writeFile(t, dir, "secret.env", "TOKEN=hunter2\n")
	writeFile(t, dir, ".gitignore", "*.log\n")
	if tbl := table(t, runStatus, req(t, dir, nil)); ignoreWarning(tbl) != nil || len(tbl.Rows) != 2 {
		t.Fatalf("status within the bound = %v, %+v, want .gitignore and secret.env alone", tbl.Rows, tbl.Warnings)
	}

	writeFile(t, dir, ".gitignore", "*.log\n*.env\n"+strings.Repeat("# padding\n", 200))
	tbl := table(t, runStatus, req(t, dir, nil))
	rowFor(t, tbl, "Path", "kept.log")
	rowFor(t, tbl, "Path", "secret.env")
	w := ignoreWarning(tbl)
	if w == nil || !strings.Contains(w.Message, ".gitignore") || !strings.Contains(w.Message, "1.0 KiB") {
		t.Errorf("warning = %+v, want git.status.ignore naming .gitignore and the bound", w)
	}
}

// The patterns one status applies are held in all, in the order go-git reads
// the files: the one that would take it past the bound is not applied, and a
// later one that fits in what is left still is.
func TestTheIgnoreFilesPastThePatternsAStatusAppliesAreNotApplied(t *testing.T) {
	lowerIgnoreBounds(t, 1<<20, 3)
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, ".gitignore", "# two patterns\n*.log\n\n*.tmp\n")
	writeFile(t, dir, "sub/.gitignore", "*.out\n*.bak\n")
	writeFile(t, dir, "sub2/.gitignore", "*.out\n")
	for _, name := range []string{"a.log", "b.tmp", "sub/c.out", "sub/d.bak", "sub/e.log", "sub2/f.out"} {
		writeFile(t, dir, name, name+"\n")
	}
	tbl := table(t, runStatus, req(t, dir, nil))
	var got []string
	for _, r := range tbl.Rows {
		got = append(got, r[0])
	}
	if want := ".gitignore sub/.gitignore sub/c.out sub/d.bak sub2/.gitignore"; strings.Join(got, " ") != want {
		t.Errorf("rows = %q, want %q: the root's patterns and sub2's applied, sub's not", got, want)
	}
	if w := ignoreWarning(tbl); w == nil || !strings.Contains(w.Message, "sub/.gitignore") ||
		strings.Contains(w.Message, " .gitignore") || !strings.Contains(w.Message, "3 patterns") {
		t.Errorf("warning = %+v, want sub/.gitignore alone named, with the bound", w)
	}
}

// A file the status would not apply is still a file: one that is tracked,
// whose content has not changed since it was committed, is not listed as
// modified because its timestamp has, which is what go-git reads it again
// for. And the working tree's own .gitignore, which go-git's status is handed
// `*` for, is not either.
func TestATrackedIgnoreFileNotAppliedIsNotModifiedByATouch(t *testing.T) {
	lowerIgnoreBounds(t, 64, 100)
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, ".gitignore", "*.log\n"+strings.Repeat("# padding\n", 20), "initial")
	commitFile(t, repo, dir, "sub/.gitignore", "*.log\n", "second")
	writeFile(t, dir, "x.log", "log\n")
	later := time.Now().Add(time.Hour)
	for _, name := range []string{".gitignore", "sub/.gitignore"} {
		if err := os.Chtimes(filepath.Join(dir, name), later, later); err != nil {
			t.Fatal(err)
		}
	}
	tbl := table(t, runStatus, req(t, dir, nil))
	if len(tbl.Rows) != 1 || tbl.Rows[0][0] != "x.log" {
		t.Errorf("rows = %v, want x.log alone: each .gitignore was touched, not changed", tbl.Rows)
	}
	if ignoreWarning(tbl) == nil {
		t.Error("no warning naming the .gitignore that was not applied")
	}

	writeFile(t, dir, ".gitignore", "*.tmp\n"+strings.Repeat("# padding\n", 20))
	if row := rowFor(t, table(t, runStatus, req(t, dir, nil)), "Path", ".gitignore"); row[2] != "M" {
		t.Errorf(".gitignore row = %v, want it modified once its content changed", row)
	}
}

// The worktree's diff shows untracked files whole, and an untracked file an
// ignore file that was not applied would have ignored is one git never
// shows: a .env the repository's own .gitignore keeps out. Each untracked
// file under such an ignore file is named rather than shown, and the diff
// says which ignore files it did not apply; the overview counts them.
func TestTheDiffDoesNotShowWhatAnIgnoreFileNotAppliedMayIgnore(t *testing.T) {
	lowerIgnoreBounds(t, 1024, 100)
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	commitFile(t, repo, dir, "sub/b.txt", "v1\n", "second")
	writeFile(t, dir, "sub/.gitignore", "*.env\n"+strings.Repeat("# padding\n", 200))
	writeFile(t, dir, "sub/secret.env", "TOKEN=hunter2\n")
	writeFile(t, dir, "top.txt", "shown\n")
	writeFile(t, dir, "a.txt", "v2\n")

	body := text(t, runDiff, req(t, dir, nil))
	if strings.Contains(body, "hunter2") {
		t.Errorf("the diff showed a file the ignore file not applied may ignore:\n%s", body)
	}
	for _, want := range []string{"+shown", "+v2", "sub/secret.env changed, not diffed", "sub/.gitignore"} {
		if !strings.Contains(body, want) {
			t.Errorf("diff has no %q:\n%s", want, body)
		}
	}

	v, err := runOverview(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	var tree string
	for _, p := range v.(view.KeyValue).Pairs {
		if p.Key == "working tree" {
			tree = p.Value
		}
	}
	if !strings.Contains(tree, "1 ignore file not applied") {
		t.Errorf("working tree = %q, want the ignore file not applied counted", tree)
	}
}

// A repository cloned into memory, which a terminal may name by its URL, is
// read through the same bounds, from the filesystem it was checked out to.
func TestTheStatusOfAnInMemoryCloneIsReadThroughTheSameBounds(t *testing.T) {
	src, repo := testRepo(t)
	commitFile(t, repo, src, ".gitignore", "*.log\n"+strings.Repeat("# padding\n", 20), "initial")
	bare := filepath.Join(t.TempDir(), "bare.git")
	if _, err := git.PlainInit(bare, true); err != nil {
		t.Fatal(err)
	}
	remote, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{bare}})
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Push(&git.PushOptions{RefSpecs: []config.RefSpec{"refs/heads/master:refs/heads/master"}}); err != nil {
		t.Fatal(err)
	}
	for bound, want := range map[int64]string{1 << 20: "", 64: ".gitignore x.log"} {
		lowerIgnoreBounds(t, bound, 100)
		clone, verr := gitclone.InMemory(context.Background(), bare, gitclone.Options{})
		if verr != nil {
			t.Fatal(verr)
		}
		wt, err := clone.Worktree()
		if err != nil {
			t.Fatal(err)
		}
		if err := util.WriteFile(wt.Filesystem, "x.log", []byte("log\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		status, ignored, err := worktreeStatus(clone, wt, pathGateOf(req(t, ".", nil)))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, n := range ignored {
			got = append(got, n.path)
		}
		got = append(got, changedPaths(status)...)
		if strings.Join(got, " ") != want {
			t.Errorf("with %d bytes, not applied and changed = %q, want %q", bound, got, want)
		}
	}
}

// untracked is the paths a status lists as untracked, in order.
func untracked(t *testing.T, r plugin.Request) []string {
	t.Helper()
	var out []string
	for _, row := range table(t, runStatus, r).Rows {
		if row[2] == "?" {
			out = append(out, row[0])
		}
	}
	return out
}

// go-git's status read no core.excludesFile, not even git's default
// ~/.config/git/ignore: a file ignored there was listed as untracked, and
// git.diff showed it whole. It is read as git reads it, wherever git would
// find it, and before any other ignore file, so that each of them wins over
// it: the repository's info/exclude, then its .gitignore files.
func TestTheExcludesFileGitReadsIsApplied(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		home := machineConfig(t, "")
		writeFile(t, home, ".config/git/ignore", "*.env\n")
		dir, repo := testRepo(t)
		commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
		writeFile(t, dir, "secret.env", "TOKEN=hunter2\n")
		if got := untracked(t, req(t, dir, nil)); len(got) != 0 {
			t.Errorf("untracked = %q, want secret.env ignored by ~/.config/git/ignore", got)
		}
		if body := text(t, runDiff, req(t, dir, nil)); strings.Contains(body, "hunter2") {
			t.Errorf("the diff showed a file ~/.config/git/ignore ignores:\n%s", body)
		}
	})
	t.Run("XDG_CONFIG_HOME", func(t *testing.T) {
		home := machineConfig(t, "")
		writeFile(t, home, ".config/git/ignore", "*.env\n")
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
		writeFile(t, home, "xdg/git/ignore", "*.tmp\n")
		dir, repo := testRepo(t)
		commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
		writeFile(t, dir, "a.env", "x\n")
		writeFile(t, dir, "b.tmp", "x\n")
		if got := strings.Join(untracked(t, req(t, dir, nil)), " "); got != "a.env" {
			t.Errorf("untracked = %q, want a.env alone: $XDG_CONFIG_HOME/git/ignore is the default", got)
		}
	})
	t.Run("core.excludesFile", func(t *testing.T) {
		home := machineConfig(t, "[core]\n\texcludesFile = ~/my-ignores\n")
		writeFile(t, home, ".config/git/ignore", "*.env\n")
		writeFile(t, home, "my-ignores", "*.tmp\n")
		dir, repo := testRepo(t)
		commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
		writeFile(t, dir, "a.env", "x\n")
		writeFile(t, dir, "b.tmp", "x\n")
		if got := strings.Join(untracked(t, req(t, dir, nil)), " "); got != "a.env" {
			t.Errorf("untracked = %q, want a.env alone: core.excludesFile names the file", got)
		}
	})
	t.Run("everything after it wins", func(t *testing.T) {
		home := machineConfig(t, "")
		writeFile(t, home, ".config/git/ignore", "*.env\n*.tmp\n!c.log\n")
		dir, repo := testRepo(t)
		commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
		writeFile(t, dir, ".git/info/exclude", "!b.tmp\n*.log\n")
		commitFile(t, repo, dir, ".gitignore", "!a.env\n", "ignores")
		for _, name := range []string{"a.env", "z.env", "b.tmp", "z.tmp", "c.log"} {
			writeFile(t, dir, name, "x\n")
		}
		if got := strings.Join(untracked(t, req(t, dir, nil)), " "); got != "a.env b.tmp" {
			t.Errorf("untracked = %q, want a.env and b.tmp, which the files after the excludes file keep", got)
		}
		// Handed to go-git ahead of the .gitignore, which its comparison
		// reads through the same open to hash it once its timestamp changed:
		// the .gitignore is still not modified.
		later := time.Now().Add(time.Hour)
		if err := os.Chtimes(filepath.Join(dir, ".gitignore"), later, later); err != nil {
			t.Fatal(err)
		}
		if rows := table(t, runStatus, req(t, dir, nil)).Rows; len(rows) != 2 {
			t.Errorf("rows = %v, want a.env and b.tmp alone: .gitignore was touched, not changed", rows)
		}
	})
}

// go-git looked for info/exclude through its own working tree filesystem,
// which refuses a path through a .git, so a repository's info/exclude was
// never applied. It is read from the directory git keeps it in, which for a
// linked worktree is the one it shares with its main checkout.
func TestTheRepositorysInfoExcludeIsApplied(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "a\n", "initial")
	writeFile(t, dir, ".git/info/exclude", "*.tmp\n")
	writeFile(t, dir, "x.tmp", "x\n")
	writeFile(t, dir, "y.txt", "y\n")
	if got := strings.Join(untracked(t, req(t, dir, nil)), " "); got != "y.txt" {
		t.Errorf("in a checkout, untracked = %q, want y.txt alone: info/exclude ignores x.tmp", got)
	}

	root := t.TempDir()
	main := filepath.Join(root, "main")
	repo, err := git.PlainInit(main, false)
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, main, "a.txt", "a\n", "on the main checkout")
	writeFile(t, main, ".git/info/exclude", "*.tmp\n")
	linked := filepath.Join(root, "linked")
	admin := filepath.Join(main, ".git", "worktrees", "linked")
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(main, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, admin, "HEAD", head.Hash().String()+"\n")
	writeFile(t, admin, "commondir", "../..\n")
	writeFile(t, admin, "gitdir", filepath.Join(linked, ".git")+"\n")
	writeFile(t, admin, "index", string(index))
	writeFile(t, linked, ".git", "gitdir: "+admin+"\n")
	writeFile(t, linked, "a.txt", "a\n")
	writeFile(t, linked, "x.tmp", "x\n")
	writeFile(t, linked, "y.txt", "y\n")
	if got := strings.Join(untracked(t, req(t, linked, nil)), " "); got != "y.txt" {
		t.Errorf("in a linked worktree, untracked = %q, want y.txt alone: info/exclude ignores x.tmp", got)
	}
}

// A core.excludesFile the repository's own config names is a path a caller
// can write, and it could name any file on the machine, whose lines would
// then be read as patterns: over MCP it is put to the gate first, and one
// outside the root is not applied, and named. One the operator's own config
// names is theirs, read wherever it is.
func TestAnExcludesFileTheRepositoryNamesIsPutToTheGate(t *testing.T) {
	home := machineConfig(t, "")
	writeFile(t, home, "outside-ignores", "*.env\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, "a.env", "x\n")
	writeFile(t, dir, "b.tmp", "x\n")
	writeFile(t, dir, "inside-ignores", "*.tmp\n")
	commitFile(t, repo, dir, "inside-ignores", "*.tmp\n", "ignores")
	mcp := func() plugin.Request { return guarded(t, dir, dir).WithSurface(plugin.SurfaceMCP) }

	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n\texcludesFile = "+filepath.Join(home, "outside-ignores")+"\n")
	if got := strings.Join(untracked(t, req(t, dir, nil)), " "); got != "b.tmp" {
		t.Errorf("at a terminal, untracked = %q, want b.tmp alone", got)
	}
	tbl := table(t, runStatus, mcp())
	if w := ignoreWarning(tbl); w == nil || !strings.Contains(w.Message, "outside-ignores") ||
		!strings.Contains(w.Message, "path gate") {
		t.Errorf("over MCP, warning = %+v, want the excludes file outside the root named as refused", w)
	}
	if got := strings.Join(untracked(t, mcp()), " "); got != "a.env b.tmp" {
		t.Errorf("over MCP, untracked = %q, want both: the file outside the root is not read", got)
	}

	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n\texcludesFile = inside-ignores\n")
	if got := strings.Join(untracked(t, mcp()), " "); got != "a.env" {
		t.Errorf("over MCP, untracked = %q, want a.env alone: the file inside the root is read", got)
	}

	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n")
	writeFile(t, home, ".gitconfig", "[core]\n\texcludesFile = "+filepath.Join(home, "outside-ignores")+"\n")
	if got := strings.Join(untracked(t, mcp()), " "); got != "b.tmp" {
		t.Errorf("over MCP, untracked = %q, want b.tmp alone: the operator's own excludes file is read", got)
	}
}

// The excludes file and info/exclude are held to the bounds before any
// .gitignore, as git reads them before any: a .gitignore that would take the
// whole bound is the one not applied, never the operator's own excludes file.
func TestTheExcludesFileIsHeldToTheBoundsFirst(t *testing.T) {
	lowerIgnoreBounds(t, 1<<20, 3)
	home := machineConfig(t, "")
	writeFile(t, home, ".config/git/ignore", "*.env\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, ".gitignore", "*.x\n*.y\n*.z\n")
	writeFile(t, dir, "secret.env", "TOKEN=hunter2\n")
	writeFile(t, dir, "b.x", "x\n")
	tbl := table(t, runStatus, req(t, dir, nil))
	if got := strings.Join(untracked(t, req(t, dir, nil)), " "); got != ".gitignore b.x" {
		t.Errorf("untracked = %q, want .gitignore and b.x: the excludes file applied, the .gitignore not", got)
	}
	if w := ignoreWarning(tbl); w == nil || !strings.HasSuffix(w.Message, ": .gitignore (3 patterns, which with "+
		"those read before it are past the 3 patterns one status applies)") {
		t.Errorf("warning = %+v, want the .gitignore alone named", w)
	}
}

// The excludes file is held to the same bounds, and one past them reaches
// the whole working tree: every untracked file is named in the diff rather
// than shown.
func TestAnExcludesFilePastTheBoundsIsNamedAndReachesEverything(t *testing.T) {
	lowerIgnoreBounds(t, 64, 100)
	home := machineConfig(t, "")
	writeFile(t, home, ".config/git/ignore", "*.env\n"+strings.Repeat("# padding\n", 20))
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, "sub/secret.env", "TOKEN=hunter2\n")
	tbl := table(t, runStatus, req(t, dir, nil))
	if w := ignoreWarning(tbl); w == nil || !strings.Contains(w.Message, filepath.Join(home, ".config", "git", "ignore")) {
		t.Errorf("warning = %+v, want the excludes file named by its path", w)
	}
	body := text(t, runDiff, req(t, dir, nil))
	if strings.Contains(body, "hunter2") || !strings.Contains(body, "sub/secret.env changed, not diffed") {
		t.Errorf("the diff showed, or did not name, a file the excludes file may ignore:\n%s", body)
	}
}

// go-git reads an ignore file's bytes as they are, where git skips a byte
// order mark before them and ends a line at a NUL: a pattern after either
// was one go-git never matched, so git.status listed a file git ignores and
// git.diff showed it whole. Each is now read as git reads it, and a tracked
// file read so is not modified by a touch. go-git's reader also stopped at a
// line longer than 64 KiB, dropping every pattern after it, where git reads
// on, and so does this.
func TestAnIgnoreFileIsReadAsGitReadsIt(t *testing.T) {
	bom, nul := string(rune(0xFEFF)), string(rune(0))
	for name, content := range map[string]string{
		"a byte order mark": bom + "*.env\n",
		"a NUL in a line":   "*.env" + nul + "junk\n",
		"a long line":       "#" + strings.Repeat("x", 70000) + "\n*.env\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir, repo := testRepo(t)
			commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
			commitFile(t, repo, dir, ".gitignore", content, "ignores")
			writeFile(t, dir, "secret.env", "TOKEN=hunter2\n")
			if body := text(t, runDiff, req(t, dir, nil)); strings.Contains(body, "hunter2") {
				t.Errorf("the diff showed a file git ignores:\n%s", body)
			}
			later := time.Now().Add(time.Hour)
			if err := os.Chtimes(filepath.Join(dir, ".gitignore"), later, later); err != nil {
				t.Fatal(err)
			}
			tbl := table(t, runStatus, req(t, dir, nil))
			if len(tbl.Rows) != 0 || ignoreWarning(tbl) != nil {
				t.Errorf("rows %v, warnings %+v, want none: git ignores secret.env, and .gitignore was touched", tbl.Rows, tbl.Warnings)
			}
		})
	}
}

// git matches ignore patterns without regard to case where core.ignorecase
// is set, as `git init` sets it on macOS, and go-git never did: `*.ENV` in a
// .gitignore ignores a secret.env there, which git.status listed and
// git.diff showed whole. Where git's own matcher leaves a letter's case
// alone, inside a bracket expression or after a backslash, this does too:
// what is untracked here is what git 2.50 lists for the same files.
func TestIgnoreCaseMatchesPatternsAsGitDoes(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, ".gitignore", strings.Join([]string{
		"*.ENV", "Build/", "[A-C]x", "[a-c]y", "[A]z", `\Qw`, `\qu`, "[^A]v", "[^a]t", "*.kp", "!Mine.kp",
	}, "\n")+"\n")
	writeFile(t, dir, "secret.env", "TOKEN=hunter2\n")
	for _, name := range []string{"build/out", "bx", "By", "az", "qw", "Qu", "av", "At", "other.kp", "mine.kp"} {
		writeFile(t, dir, name, "x\n")
	}
	for setting, want := range map[string]string{
		"true":  ".gitignore At az mine.kp qw",
		"false": ".gitignore By Qu az build/out bx qw secret.env",
	} {
		writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n\tignorecase = "+setting+"\n")
		if got := strings.Join(untracked(t, req(t, dir, nil)), " "); got != want {
			t.Errorf("with core.ignorecase %s, untracked = %q, want %q", setting, got, want)
		}
		if body := text(t, runDiff, req(t, dir, nil)); setting == "true" && strings.Contains(body, "hunter2") {
			t.Errorf("with core.ignorecase, the diff showed a file git ignores:\n%s", body)
		}
	}
}

// gitMatchFixture is a working tree whose ignore files hold the patterns git
// reads otherwise than go-git did, each untracked file named for what it
// tests, and the untracked files git 2.50 lists of it with `git ls-files
// --others --exclude-standard`: what git does not ignore.
var gitMatchFixture = struct {
	ignores map[string]string
	files   []string
	want    string
}{
	ignores: map[string]string{
		".gitignore": strings.Join([]string{
			"neg[!0-9].txt", "br[]a]", "dash[a-]", "[[:upper:]]*.key", "star**b", "build/", "!build/keep.txt",
			"/rooted", "**/any", "a/**/z", `\!bang`, "trail.txt   ", "open[", "k[a-c-e]", "dir-only/",
		}, "\n") + "\n",
		"sub/.gitignore":    "*.log\ndeep/x.txt\n!neg1.txt\n",
		".git/info/exclude": "*.tmp\n!keep.tmp\n",
	},
	files: []string{
		"neg1.txt", "negA.txt", "br]", "bra", "brb", "dash-", "dasha", "dashb", "Prod.key", "dev.key",
		"starXXb", "starb", "build/keep.txt", "build/out.o", "rooted", "sub/rooted", "any", "sub/deep/any",
		"a/z", "a/m/n/z", "!bang", "trail.txt", "open[", "ka", "k-", "ke", "kd", "dir-only", "x.tmp", "keep.tmp",
		"sub/a.log", "sub/deep/x.txt", "deep/x.txt", "sub/neg1.txt", "sub/negB.txt", "a.log",
	},
	want: "a.log brb dashb deep/x.txt dev.key dir-only kd keep.tmp neg1.txt open[ sub/neg1.txt sub/rooted",
}

// go-git matched ignore patterns with filepath.Match: `[!0-9]` held a !, `[]a]`
// and `[a-]` matched nothing, `[[:upper:]]` was not a class, a ** beside
// anything but a slash matched nothing, and `!build/keep.txt` brought back a
// file under a directory git excludes, which git's own documentation says no
// pattern does. Each untracked file is now one git ignores or lists, as git
// 2.50 does, and where git is on PATH it is asked, so that the fixture's
// answer cannot drift from git's.
func TestIgnorePatternsMatchAsGitMatchesThem(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "tracked.txt", "v1\n", "initial")
	for name, content := range gitMatchFixture.ignores {
		writeFile(t, dir, name, content)
	}
	for _, name := range gitMatchFixture.files {
		writeFile(t, dir, name, "x\n")
	}
	var got []string
	for _, p := range untracked(t, req(t, dir, nil)) {
		if !strings.HasSuffix(p, ".gitignore") {
			got = append(got, p)
		}
	}
	if strings.Join(got, " ") != gitMatchFixture.want {
		t.Errorf("untracked = %q\nwant        %q", strings.Join(got, " "), gitMatchFixture.want)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	cmd := exec.Command("git", "-C", dir, "ls-files", "--others", "--exclude-standard")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var byGit []string
	for _, p := range strings.Fields(string(out)) {
		if !strings.HasSuffix(p, ".gitignore") {
			byGit = append(byGit, p)
		}
	}
	sort.Strings(byGit)
	if strings.Join(byGit, " ") != gitMatchFixture.want {
		t.Errorf("git lists %q\nthe fixture %q", strings.Join(byGit, " "), gitMatchFixture.want)
	}
}

// git reads a line of an ignore file for its trailing spaces as its
// trim_trailing_spaces does, and each pattern as parse_path_pattern does.
func TestAnIgnoreLineIsParsedAsGitParsesIt(t *testing.T) {
	for line, want := range map[string]ignorePattern{
		"*.log":        {text: "*.log", endsWith: true, basename: true},
		"!Build/":      {text: "Build", literal: 5, negative: true, dirOnly: true, basename: true},
		"/a/b*":        {text: "/a/b*", literal: 4},
		`a\ `:          {text: `a\ `, literal: 1, basename: true},
		"a  ":          {text: "a", literal: 1, basename: true},
		`a\\  `:        {text: `a\\`, literal: 1, basename: true},
		"*.[ch]":       {text: "*.[ch]", basename: true},
		"docs/**/*.md": {text: "docs/**/*.md", literal: 5},
	} {
		if got := parsePattern(trimTrailingSpaces(line)); got != want {
			t.Errorf("%q = %+v, want %+v", line, got, want)
		}
	}
	bom, nul := string(rune(0xFEFF)), string(rune(0))
	got := parseIgnore([]byte(bom + "# c\n\n.env" + nul + "junk\r\n \n!\nx\r\n/\nlast"))
	var texts []string
	for _, p := range got {
		texts = append(texts, p.text)
	}
	if strings.Join(texts, ",") != ".env,x,last" {
		t.Errorf("patterns = %q, want .env, x and last", texts)
	}
}

// git never follows a .gitignore that is a symbolic link, and warns of it:
// its lines are whatever file the link names, anywhere on the machine. go-git
// followed one inside the working tree. It is not applied, and named, and
// what it would have ignored is named in the diff rather than shown.
func TestASymlinkedGitignoreIsNotFollowed(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, "patterns", "*.env\n")
	writeFile(t, dir, "sub/secret.env", "TOKEN=hunter2\n")
	if err := os.Symlink("../patterns", filepath.Join(dir, "sub", ".gitignore")); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	tbl := table(t, runStatus, req(t, dir, nil))
	rowFor(t, tbl, "Path", "sub/secret.env")
	if w := ignoreWarning(tbl); w == nil || !strings.Contains(w.Message, "sub/.gitignore (a symbolic link, which git does not follow)") {
		t.Errorf("warning = %+v, want sub/.gitignore named as a link git does not follow", w)
	}
	if body := text(t, runDiff, req(t, dir, nil)); strings.Contains(body, "hunter2") {
		t.Errorf("the diff showed a file under a .gitignore it did not apply:\n%s", body)
	}
}
