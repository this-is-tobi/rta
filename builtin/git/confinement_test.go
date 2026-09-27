package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The repository is a path this plugin *derives*, and the boundary can only
// check the paths a caller sends.
//
// go-git's DetectDotGit walks upward looking for a .git entry, which is
// exactly right for a person standing in a subdirectory and is an escape for
// a caller whose reach was bounded: point an MCP server at a directory that is
// not itself a checkout, and every git capability opens whichever repository
// happens to be *above* it. `~/.git` is the ordinary case — versioning one's
// dotfiles is common, and it puts every committed file's contents inside
// `git.diff`, every message inside `git.log`, and every remembered credential
// inside `git.config`. The argument passed the guard; the thing that got
// opened never went past it.

// guarded builds the request an MCP call arrives as: confined to root, asking
// about path.
func guarded(t *testing.T, root, path string) plugin.Request {
	t.Helper()
	g, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return req(t, path, nil).WithConfinement(g.Check)
}

// nested builds the shape the escape needs: a repository, and inside it a
// plain directory with no repository of its own. A root drawn around the inner
// directory is a root the outer repository is outside of.
func nested(t *testing.T) (outer, inner string) {
	t.Helper()
	outer, repo := testRepo(t)
	commitFile(t, repo, outer, "secret.txt", "the outer repository's contents\n",
		"a commit the root was drawn to exclude")
	inner = filepath.Join(outer, "project", "sub")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	return outer, inner
}

func TestARepositoryAboveTheRootIsNotOpened(t *testing.T) {
	_, inner := nested(t)
	root := filepath.Dir(inner) // …/project, itself no repository

	for name, h := range map[string]plugin.Handler{
		"log":      runLog,
		"diff":     runDiff,
		"status":   runStatus,
		"config":   runConfig,
		"branches": runBranches,
		"overview": runOverview,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := h(context.Background(), guarded(t, root, inner))
			if err == nil {
				t.Fatal("the repository above the root was opened")
			}
			if code := errCode(err); code != "core.mcp.path.outside" {
				t.Fatalf("refused as %q, want core.mcp.path.outside — the walk out of the "+
					"root has to be refused as what it is, not reported as a missing repository", code)
			}
		})
	}
}

// The whole point of walking upward survives inside the root: a caller allowed
// to read a checkout may name any directory in it.
func TestASubdirectoryOfAnAllowedRepositoryStillResolves(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	sub := filepath.Join(dir, "deep", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	tbl := table(t, runLog, guarded(t, dir, sub).With(map[string]any{"limit": defaultLogLimit}))
	if len(tbl.Rows) != 1 {
		t.Fatalf("rows = %d, want the repository's one commit", len(tbl.Rows))
	}
}

// And an unconfined surface is unchanged. There is a person at a terminal who
// can already read their own files, and `rta git log` from a subdirectory is
// the ordinary way to run it.
func TestAnUnconfinedCallStillWalksUpToTheRepository(t *testing.T) {
	_, inner := nested(t)

	tbl := table(t, runLog, req(t, inner, map[string]any{"limit": defaultLogLimit}))
	if len(tbl.Rows) != 1 {
		t.Fatalf("rows = %d, want the outer repository's one commit", len(tbl.Rows))
	}
}

// A checkout's root names its repository without having to hold it: a `.git`
// file says `gitdir: <anywhere>`, a git directory's `commondir` file says
// where its objects, refs and config are, and objects/info/alternates says
// where more objects are. Each is a file a caller can write inside the root,
// and go-git follows the first two to whatever they name — so a root holding
// sub/.git reading `gitdir: /elsewhere/.git` answered git.log, git.diff
// --commit and git.config from a repository the root was drawn to exclude.
func TestAGitDirectoryPointerCannotLeadOutOfTheRoot(t *testing.T) {
	outside, repo := testRepo(t)
	commitFile(t, repo, outside, "creds.txt", "AWS_SECRET=outside-the-root\n", "secret commit")
	gitDir := filepath.Join(outside, ".git")
	root := t.TempDir()

	for name, plant := range map[string]func(t *testing.T, dir string){
		"gitdir file": func(t *testing.T, dir string) {
			writeFile(t, dir, ".git", "gitdir: "+gitDir+"\n")
		},
		"relative gitdir file": func(t *testing.T, dir string) {
			rel, err := filepath.Rel(dir, gitDir)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, ".git", "gitdir: "+rel+"\n")
		},
		"commondir": func(t *testing.T, dir string) {
			head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, ".git/HEAD", string(head))
			writeFile(t, dir, ".git/commondir", gitDir+"\n")
		},
		"alternates": func(t *testing.T, dir string) {
			local, err := git.PlainInit(dir, false)
			if err != nil {
				t.Fatal(err)
			}
			head, err := repo.Head()
			if err != nil {
				t.Fatal(err)
			}
			if err := local.Storer.SetReference(plumbing.NewHashReference("refs/heads/master", head.Hash())); err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, ".git/objects/info/alternates", filepath.Join(gitDir, "objects")+"\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(root, strings.ReplaceAll(name, " ", "-"))
			plant(t, dir)
			for capability, h := range map[string]plugin.Handler{
				"log":    runLog,
				"diff":   runDiff,
				"config": runConfig,
				"hooks":  runHooks,
			} {
				values := map[string]any{"limit": defaultLogLimit}
				if capability == "diff" {
					values = map[string]any{"commit": "HEAD"}
				}
				v, err := h(context.Background(), guarded(t, root, dir).With(values))
				if err == nil {
					t.Fatalf("%s answered from the repository the pointer names: %+v", capability, v)
				}
				if code := errCode(err); code != "core.mcp.path.outside" {
					t.Errorf("%s refused as %q, want core.mcp.path.outside — the pointer has to be "+
						"refused as leading out of the root, not reported as some other fault", capability, code)
				}
			}
		})
	}
}

// And the same pointers stay followed where they lead somewhere the caller may
// read: a linked worktree beside its main checkout, both under the root, is the
// ordinary shape `git worktree add` makes.
func TestALinkedWorktreeInsideTheRootStillOpens(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := git.PlainInit(main, false)
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, main, "a.txt", "a\n", "on the main checkout")

	linked := filepath.Join(root, "linked")
	admin := filepath.Join(main, ".git", "worktrees", "linked")
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, admin, "HEAD", head.Hash().String()+"\n")
	writeFile(t, admin, "commondir", "../..\n")
	writeFile(t, admin, "gitdir", filepath.Join(linked, ".git")+"\n")
	writeFile(t, linked, ".git", "gitdir: "+admin+"\n")

	tbl := table(t, runLog, guarded(t, root, linked).With(map[string]any{"limit": defaultLogLimit}))
	if len(tbl.Rows) != 1 {
		t.Fatalf("rows = %v, want the main checkout's one commit", tbl.Rows)
	}

	// Drawn around the linked worktree alone, the git directory is outside it.
	_, err = runLog(context.Background(), guarded(t, linked, linked).With(map[string]any{"limit": defaultLogLimit}))
	if err == nil || errCode(err) != "core.mcp.path.outside" {
		t.Errorf("a root around the linked worktree alone opened its git directory: %v", err)
	}
}

// A symlink inside the git directory needs no check of its own: go-billy's
// chroot refuses to follow a link out of the directory it was opened on, on
// every surface. Pinned, because gitDirsInBounds judges only the directories
// go-git takes by name and rests on this for everything under them.
func TestALinkInsideTheGitDirectoryDoesNotLeadOut(t *testing.T) {
	outside, repo := testRepo(t)
	commitFile(t, repo, outside, "creds.txt", "AWS_SECRET=outside-the-root\n", "secret commit")
	root := t.TempDir()
	dir := filepath.Join(root, "linked-objects")
	for _, f := range []string{"HEAD", "config", filepath.Join("refs", "heads", "master")} {
		content, err := os.ReadFile(filepath.Join(outside, ".git", f))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, dir, filepath.Join(".git", f), string(content))
	}
	if err := os.Symlink(filepath.Join(outside, ".git", "objects"), filepath.Join(dir, ".git", "objects")); err != nil {
		t.Fatal(err)
	}

	for name, r := range map[string]plugin.Request{
		"confined":   guarded(t, root, dir),
		"unconfined": req(t, dir, nil),
	} {
		v, err := runDiff(context.Background(), r.With(map[string]any{"commit": "HEAD"}))
		if err == nil {
			t.Errorf("%s: the diff was read through a link out of the git directory: %+v", name, v)
		}
	}
}

// rta's own state is refused wherever it sits, a checkout included: a
// dotfiles repository at ~ holds ~/.local/share/rta untracked, and git.diff
// read every changed and untracked file whole — the age identity among them —
// while fs.hash on the same file was refused as rta's own state. A file the
// gate refuses is named in the diff's own shape and not read, from the disk
// or, for a --commit diff, from the object store.
func TestADiffDoesNotShowRtasOwnState(t *testing.T) {
	const secret = "AGE-SECRET-KEY-1NOTFORANAGENT"
	identity := filepath.Join(".local", "share", "rta", "kv.identity")

	setup := func(t *testing.T) (string, *git.Repository) {
		t.Helper()
		dir, repo := testRepo(t)
		t.Setenv("RTA_DATA_DIR", filepath.Join(dir, ".local", "share", "rta"))
		commitFile(t, repo, dir, ".zshrc", "export A=1\n", "dotfiles")
		return dir, repo
	}
	withheld := filepath.ToSlash(identity) + " changed, not diffed: the path gate refuses it (core.mcp.path.protected)"

	t.Run("worktree", func(t *testing.T) {
		dir, _ := setup(t)
		writeFile(t, dir, identity, secret+"\n")
		writeFile(t, dir, ".zshrc", "export A=2\n")

		body := text(t, runDiff, guarded(t, dir, dir))
		if strings.Contains(body, secret) {
			t.Fatalf("the diff showed rta's own state to a confined caller:\n%s", body)
		}
		if !strings.Contains(body, withheld) {
			t.Errorf("the refused file is not named in the diff:\n%s", body)
		}
		if !strings.Contains(body, "+export A=2") {
			t.Errorf("the change the caller may read is missing:\n%s", body)
		}
		// A person at a terminal reads their own files.
		if !strings.Contains(text(t, runDiff, req(t, dir, nil)), secret) {
			t.Error("an unconfined diff withheld a file from the person it belongs to")
		}
	})

	t.Run("commit", func(t *testing.T) {
		dir, repo := setup(t)
		commitFile(t, repo, dir, identity, secret+"\n", "committed by mistake")

		body := text(t, runDiff, guarded(t, dir, dir).With(map[string]any{"commit": "HEAD"}))
		if strings.Contains(body, secret) {
			t.Fatalf("the commit's diff showed rta's own state to a confined caller:\n%s", body)
		}
		if !strings.Contains(body, withheld) {
			t.Errorf("the refused file is not named in the commit's diff:\n%s", body)
		}
	})
}

// A URL reaching a confined handler is refused at the boundary rather than
// mangled into a local path — see pathguard.remote. Pinned from this side too,
// because this is the plugin whose path input accepts one.
func TestAURLIsRefusedRatherThanTurnedIntoALocalPath(t *testing.T) {
	root := t.TempDir()
	g, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, verr := g.Check("path", "https://example.com/repo.git"); verr == nil {
		t.Fatal("a URL was accepted as a path under a root")
	} else if verr.Code != "core.mcp.path.remote" {
		t.Fatalf("code = %q, want core.mcp.path.remote", verr.Code)
	}
}

// The boundary substitutes a Path input rather than merely approving it:
// what a handler receives is the judged form, absolute and symlink-resolved,
// whatever the caller spelled (internal/mcp's checkPaths). So a file input
// never arrives the way its help describes it, relative to the repository
// root — and that relative form is the only one go-git's tree lookup knows.
// Handed the absolute form, blame found no such file, with a hint telling the
// caller to send what it had just sent, and log's --file matched no commit: a
// well-formed empty table an agent reads as "nobody ever touched this file".
func TestAFileInsideTheRootIsHandedToGitRepositoryRelative(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "a\n", "touches a")
	commitFile(t, repo, dir, "sub/b.txt", "b\n", "touches b")

	g, err := pathguard.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	file, verr := g.Check("file", filepath.Join(dir, "sub", "b.txt"))
	if verr != nil {
		t.Fatal(verr)
	}
	r := req(t, dir, map[string]any{"file": file, "limit": defaultLogLimit}).WithConfinement(g.Check)

	t.Run("blame", func(t *testing.T) {
		blame := table(t, runBlame, r)
		if len(blame.Rows) != 1 || blame.Rows[0][4] != "b" {
			t.Fatalf("blame rows = %v, want the file's one line", blame.Rows)
		}
	})
	t.Run("log", func(t *testing.T) {
		history := table(t, runLog, r)
		if len(history.Rows) != 1 || history.Rows[0][3] != "touches b" {
			t.Fatalf("log rows = %v, want the one commit that touched the file", history.Rows)
		}
	})
}

func errCode(err error) string {
	var ve *view.Error
	if errors.As(err, &ve) {
		return ve.Code
	}
	return err.Error()
}
