package git

import (
	"context"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// upstreamRepo is a repository whose master is one commit ahead of the one
// origin's master was fetched at, with config appended to its own.
func upstreamRepo(t *testing.T, config string) string {
	t.Helper()
	dir, repo := testRepo(t)
	first := commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	commitFile(t, repo, dir, "b.txt", "v1\n", "second commit")
	for _, name := range []plumbing.ReferenceName{
		plumbing.NewRemoteReferenceName("origin", "master"), "refs/remotes/mirror/master",
	} {
		if err := repo.Storer.SetReference(plumbing.NewHashReference(name, first)); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n"+config)
	return dir
}

// tracks is what git.branches and the overview say master tracks, and git's
// own word on it where there is a git to ask: "" for no upstream.
func tracks(t *testing.T, dir string) (branches, overview, byGit string, asked bool) {
	t.Helper()
	branches = rowFor(t, table(t, runBranches, req(t, dir, nil)), "Name", "master")[2]
	v, err := runOverview(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range v.(view.KeyValue).Pairs {
		if p.Key == "tracking" {
			overview, _, _ = strings.Cut(p.Value, " (")
		}
	}
	byGit, asked = gitSays(t, dir, "rev-parse", "--abbrev-ref", "master@{upstream}")
	return branches, overview, byGit, asked
}

// A ref of a remote is mapped through its fetch refspecs as git 2.50 maps it,
// each case one git was asked: a negative refspec among them, which go-git
// has no grammar for, and a merge by short name, which matches no source.
func TestARefspecMapsARemotesRefAsGitMapsIt(t *testing.T) {
	usual := "+refs/heads/*:refs/remotes/origin/*"
	for _, c := range []struct {
		refspecs  []string
		src, want string
	}{
		{[]string{usual}, "refs/heads/main", "refs/remotes/origin/main"},
		{[]string{usual}, "main", ""},
		{[]string{usual}, "refs/heads/*", "refs/remotes/origin/*"},
		{[]string{usual, "^refs/heads/main"}, "refs/heads/main", "refs/remotes/origin/main"},
		{[]string{"refs/heads/main:refs/remotes/x/y", "^refs/heads/main"}, "refs/heads/main", ""},
		{[]string{"+refs/heads/*:refs/heads/*", "^refs/heads/main"}, "refs/heads/main", ""},
		{[]string{"refs/heads/main:refs/remotes/x/y", usual}, "refs/heads/main", "refs/remotes/x/y"},
		{[]string{"refs/heads/main"}, "refs/heads/main", ""},
		{[]string{"+refs/heads/ma*:refs/remotes/o/*-x"}, "refs/heads/main", "refs/remotes/o/in-x"},
		{[]string{"refs/heads/main:", usual}, "refs/heads/main", ""},
		{[]string{"@:refs/remotes/o/head"}, "HEAD", "refs/remotes/o/head"},
	} {
		if got, _ := trackingRef(c.refspecs, c.src); got != c.want {
			t.Errorf("%s through %q = %q, want %q", c.src, c.refspecs, got, c.want)
		}
	}
}

// A fetch refspec is read as git's parse_refspec reads one, each case one git
// was asked: a * on one side alone or two on one, a side that is not a ref
// name as git reads one, and a negative one with a destination are refused
// by git, and passed over here. The first two were taken for patterns, and
// refs/heads/*:refs/remotes/o/** tracked main at o/main*.
func TestARefspecIsReadAsGitReadsIt(t *testing.T) {
	_, hasGit := gitSays(t, t.TempDir(), "--version")
	for _, c := range []struct {
		spec  string
		valid bool
	}{
		{"+refs/heads/*:refs/remotes/origin/*", true},
		{"refs/heads/main", true},
		{"refs/heads/main:", true},
		{"^refs/heads/ma*", true},
		{"*:*", true},
		{"@:refs/remotes/o/head", true},
		{"+refs/heads/*", false},
		{"refs/heads/*:", false},
		{"refs/heads/**:refs/remotes/o/*", false},
		{"refs/heads/*:refs/remotes/o/**", false},
		{"refs/heads/main:refs/remotes/o/*", false},
		{"^refs/heads/main:refs/x", false},
		{"+^refs/heads/x", false},
		{"^", false},
		{"refs/heads/ma in:refs/remotes/o/main", false},
		{"refs/heads/main:refs/remotes/o/ma..in", false},
		{"refs/heads/main:refs/remotes/o/x.lock", false},
		{"refs/heads/main:refs/remotes/o/x.", false},
		{"refs/heads/main:refs/remotes//x", false},
		{"refs/heads/main:refs/remotes/o/a@{b", false},
	} {
		if _, valid := parseRefspec(c.spec); valid != c.valid {
			t.Errorf("%q read as valid %v, want %v", c.spec, valid, c.valid)
		}
		if !hasGit {
			continue
		}
		dir := upstreamRepo(t, "[remote \"origin\"]\n\turl = https://example.com/r.git\n\tfetch = "+c.spec+"\n")
		if _, runs := gitSays(t, dir, "remote", "-v"); runs != c.valid {
			t.Errorf("git runs with %q: %v, want %v", c.spec, runs, c.valid)
		}
	}
}

// **What a branch tracks at a remote is found where the remote's fetch keeps
// it**, as git's remote_find_tracking finds it: branch.<name>.merge put
// through remote.<remote>.fetch, the first refspec whose source it matches
// deciding. This took refs/remotes/<remote>/<branch> whatever the refspec
// said, so a remote fetched into refs/remotes/mirror/ was tracked at a ref no
// fetch writes, gone; and a remote that fetches nothing, or one that is not
// configured at all, of which git finds no upstream ("not stored as a
// remote-tracking branch"), was said to be tracked, gone too.
func TestWhatABranchTracksIsFoundThroughTheRemotesRefspec(t *testing.T) {
	url := "[remote \"origin\"]\n\turl = https://example.com/r.git\n"
	branch := "[branch \"master\"]\n\tremote = origin\n\tmerge = refs/heads/master\n"
	for _, c := range []struct {
		name, config, want string
	}{
		{"the usual refspec", url + "\tfetch = +refs/heads/*:refs/remotes/origin/*\n" + branch, "origin/master"},
		{"a refspec of the remote's own", url + "\tfetch = +refs/heads/*:refs/remotes/mirror/*\n" + branch,
			"mirror/master"},
		{"the first refspec that matches", url + "\tfetch = refs/heads/master:refs/remotes/mirror/master\n" +
			"\tfetch = +refs/heads/*:refs/remotes/origin/*\n" + branch, "mirror/master"},
		{"a refspec for other branches", url + "\tfetch = +refs/heads/release/*:refs/remotes/origin/release/*\n" +
			branch, ""},
		{"a remote that fetches nothing", url + branch, ""},
		{"a remote that is not configured", branch, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := upstreamRepo(t, c.config)
			branches, overview, byGit, asked := tracks(t, dir)
			if branches != c.want || overview != c.want {
				t.Errorf("master tracks %q in git.branches and %q in the overview, want %q", branches, overview, c.want)
			}
			if asked && byGit != c.want {
				t.Errorf("git reads master's upstream as %q, want %q", byGit, c.want)
			}
		})
	}
}

// **go-git refuses a branch section git reads.** It opens a repository only
// where each branch.<name>.merge names a branch in full, refs/heads/<name>,
// and a merge of `main`, as a hand or a script writes it, left every
// capability refusing the repository as "not a git repository" ("branch
// config: invalid merge"), where git runs. So does a fetch refspec go-git has
// no grammar for, a negative one or one with no destination, and a
// branch.<name>.rebase of merges, which `git pull --rebase=merges` sets. Each
// is read as git reads it: the repository opens, and the upstream is the
// one git finds, or none where git finds none.
func TestABranchSectionGoGitRefusesOpensAsGitReadsIt(t *testing.T) {
	remote := "[remote \"origin\"]\n\turl = https://example.com/r.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	for _, c := range []struct {
		name, config, want string
	}{
		{"a merge by short name, from a remote", remote + "[branch \"master\"]\n\tremote = origin\n\tmerge = master\n", ""},
		{"a merge by short name, from the repository itself", "[branch \"base\"]\n[branch \"master\"]\n\tremote = .\n" +
			"\tmerge = master\n", "master"},
		{"a rebase of merges", remote + "[branch \"master\"]\n\tremote = origin\n\tmerge = refs/heads/master\n" +
			"\trebase = merges\n", "origin/master"},
		{"a negative refspec", remote + "\tfetch = ^refs/heads/wip/*\n[branch \"master\"]\n\tremote = origin\n" +
			"\tmerge = refs/heads/master\n", "origin/master"},
		{"a refspec with no destination", remote + "\tfetch = refs/tags/v1\n[branch \"master\"]\n\tremote = origin\n" +
			"\tmerge = refs/heads/master\n", "origin/master"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := upstreamRepo(t, c.config)
			for name, h := range map[string]plugin.Handler{
				"log": runLog, "status": runStatus, "config": runConfig, "remotes": runRemotes, "hooks": runHooks,
			} {
				if _, err := h(context.Background(), req(t, dir, map[string]any{"limit": defaultLogLimit})); err != nil {
					t.Errorf("%s: %v", name, err)
				}
			}
			branches, overview, byGit, asked := tracks(t, dir)
			if branches != c.want || overview != c.want {
				t.Errorf("master tracks %q in git.branches and %q in the overview, want %q", branches, overview, c.want)
			}
			if asked && byGit != c.want {
				t.Errorf("git reads master's upstream as %q, want %q", byGit, c.want)
			}
		})
	}
}
