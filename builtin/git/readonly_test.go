package git

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// snapshot is every path under dir with its mode and, for a file, its bytes.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entry := info.Mode().String()
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			entry += " " + string(data)
		}
		out[p] = entry
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A Read capability writes nothing. go-git's worktree status, for a
// submodule initialised in .git/config with no repository under
// .git/modules, opened it through Submodule.Repository, which initialises
// one there when it finds none: git.status, git.diff and git.overview each
// wrote a config, a HEAD, objects and refs under .git/modules/lib, and a .git
// file into lib/. The checkout is byte for byte what it was after each call,
// and the submodule reads as git reads one never cloned.
func TestStatusDiffAndOverviewWriteNothingForAnUnclonedSubmodule(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "a\n", "initial")
	idx, err := repo.Storer.Index()
	if err != nil {
		t.Fatal(err)
	}
	link := idx.Add("lib")
	link.Mode, link.Hash = filemode.Submodule, plumbing.NewHash("1111111111111111111111111111111111111111")
	if err := repo.Storer.SetIndex(idx); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, dir, ".gitmodules",
		"[submodule \"lib\"]\n\tpath = lib\n\turl = https://example.invalid/lib.git\n", "lib")
	cfg, err := repo.Config()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Submodules["lib"] = &config.Submodule{Name: "lib", Path: "lib", URL: "https://example.invalid/lib.git"}
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "notes.txt", "untracked\n")

	for _, call := range []struct {
		name   string
		run    plugin.Handler
		values map[string]any
	}{
		{"git.status", runStatus, nil},
		{"git.diff", runDiff, nil},
		{"git.overview", runOverview, nil},
		{"git.overview --detail", runOverview, map[string]any{"detail": true}},
	} {
		before := snapshot(t, dir)
		if _, err := call.run(context.Background(), req(t, dir, call.values)); err != nil {
			t.Fatalf("%s: %v", call.name, err)
		}
		after := snapshot(t, dir)
		for p, was := range before {
			if now, ok := after[p]; !ok || now != was {
				t.Errorf("%s changed %s", call.name, p)
			}
		}
		for p := range after {
			if _, ok := before[p]; !ok {
				t.Errorf("%s wrote %s", call.name, p)
			}
		}
	}

	tbl := table(t, runStatus, req(t, dir, nil))
	if len(tbl.Rows) != 1 || tbl.Rows[0][0] != "notes.txt" {
		t.Errorf("status rows = %v, want the untracked file alone", tbl.Rows)
	}
}

// The filesystem a repository is read through refuses every write, so a
// write anything in go-git would make fails the call that made it rather than
// changing the repository from a Read capability.
func TestTheRepositoryFilesystemRefusesEveryWrite(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "HEAD", "ref: refs/heads/main\n")
	f := regularFiles{Filesystem: osfs.New(dir), gitDir: true}
	before := snapshot(t, dir)
	for name, write := range map[string]func() error{
		"create": func() error { _, err := f.Create("new"); return err },
		"open for writing": func() error {
			_, err := f.OpenFile("HEAD", os.O_WRONLY|os.O_TRUNC, 0o644)
			return err
		},
		"open to create": func() error {
			_, err := f.OpenFile("new", os.O_RDONLY|os.O_CREATE, 0o644)
			return err
		},
		"temp file": func() error { _, err := f.TempFile("", "x"); return err },
		"rename":    func() error { return f.Rename("HEAD", "moved") },
		"remove":    func() error { return f.Remove("HEAD") },
		"mkdir":     func() error { return f.MkdirAll("modules/lib", 0o755) },
		"symlink":   func() error { return f.Symlink("HEAD", "link") },
	} {
		if err := write(); !errors.Is(err, errReadOnly) {
			t.Errorf("%s: err = %v, want errReadOnly", name, err)
		}
	}
	chrooted, err := f.Chroot("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chrooted.Create("new"); !errors.Is(err, errReadOnly) {
		t.Errorf("create under a chroot: err = %v, want errReadOnly", err)
	}
	if after := snapshot(t, dir); len(after) != len(before) {
		t.Errorf("the directory changed: %v", after)
	}
}
