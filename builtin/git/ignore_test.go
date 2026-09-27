package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/util"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"

	"github.com/this-is-tobi/rta/builtin/internal/gitclone"
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
// for.
func TestATrackedIgnoreFileNotAppliedIsNotModifiedByATouch(t *testing.T) {
	lowerIgnoreBounds(t, 64, 100)
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, ".gitignore", "*.log\n"+strings.Repeat("# padding\n", 20), "initial")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, ".gitignore"), later, later); err != nil {
		t.Fatal(err)
	}
	tbl := table(t, runStatus, req(t, dir, nil))
	if len(tbl.Rows) != 0 {
		t.Errorf("rows = %v, want none: .gitignore was touched, not changed", tbl.Rows)
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
		status, ignored, err := worktreeStatus(clone, wt)
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
