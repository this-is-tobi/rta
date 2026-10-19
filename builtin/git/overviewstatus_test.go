package git

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func overviewPairsOn(t *testing.T, dir string, sf plugin.Surface) []view.Pair {
	t.Helper()
	v, err := runOverview(context.Background(), req(t, dir, nil).WithSurface(sf))
	if err != nil {
		t.Fatal(err)
	}
	return v.(view.KeyValue).Pairs
}

func keysOfOverview(pairs []view.Pair) []string {
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = p.Key
	}
	return out
}

func valueIn(t *testing.T, pairs []view.Pair, key string) string {
	t.Helper()
	for _, p := range pairs {
		if p.Key == key {
			return p.Value
		}
	}
	t.Fatalf("no %q row in %v", key, keysOfOverview(pairs))
	return ""
}

func fetchedAgo(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	writeFile(t, dir, ".git/FETCH_HEAD", "x\tnot-for-merge\tbranch 'master' of origin\n")
	when := time.Now().Add(-age)
	if err := os.Chtimes(filepath.Join(dir, ".git", "FETCH_HEAD"), when, when); err != nil {
		t.Fatal(err)
	}
}

// stashed leaves what `git stash` leaves: the ref, and a reflog with a line
// for each entry, newest last.
func stashed(t *testing.T, dir string, entries int) {
	t.Helper()
	zero, hash := strings.Repeat("0", 40), strings.Repeat("ab", 20)
	writeFile(t, dir, ".git/refs/stash", hash+"\n")
	var log strings.Builder
	for range entries {
		log.WriteString(zero + " " + hash + " Ada Lovelace <ada@example.com> 1700000000 +0000\tWIP on master: abc1234 work\n")
	}
	writeFile(t, dir, ".git/logs/refs/stash", log.String())
}

// The first row is the verdict, in a word the theme draws, and on a repository
// with nothing the matter it is all the news there is: the branch, what it
// tracks, the tree, the last commit.
func TestACalmRepositoryIsAVerdictAndTheFactsBehindIt(t *testing.T) {
	dir, repo := testRepo(t)
	first := commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	fetched(t, repo, "origin", "master", first)

	for _, sf := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceTUI} {
		pairs := overviewPairsOn(t, dir, sf)
		if want := []string{"status", "branch", "tracking", "working tree", "last commit"}; !slices.Equal(keysOfOverview(pairs), want) {
			t.Errorf("%v: rows = %v, want %v", sf, keysOfOverview(pairs), want)
		}
		if got := pairs[0].Value; got != "ok" || theme.ClassifyStatus(got) != theme.StatusGood {
			t.Errorf("%v: status = %q, want ok, which the theme draws as good", sf, got)
		}
	}
}

// A branch that tracks nothing used to print no row, and a row that is not
// there reads as a repository with nothing to say about it. Saying "none" is
// the answer to "am I about to push to nothing".
func TestABranchWithNoUpstreamSaysItWasNeverPushed(t *testing.T) {
	dir, repo := testRepo(t)
	first := commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")

	pairs := overviewPairsOn(t, dir, plugin.SurfaceCLI)
	if got := valueIn(t, pairs, "tracking"); got != "none — never pushed" || theme.ClassifyStatus(got) != theme.StatusMuted {
		t.Errorf("tracking = %q, want it said, in the muted word", got)
	}
	if got := valueIn(t, pairs, "status"); got != "ok" {
		t.Errorf("status = %q: a branch nobody pushed yet is a fact, not a fault", got)
	}

	// A detached HEAD tracks nothing because it is on no branch, and a repository
	// with no commit has no branch to track: neither is "never pushed".
	if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.HEAD, first)); err != nil {
		t.Fatal(err)
	}
	if pairs = overviewPairsOn(t, dir, plugin.SurfaceCLI); slices.Contains(keysOfOverview(pairs), "tracking") {
		t.Errorf("a detached HEAD has a tracking row: %v", keysOfOverview(pairs))
	}
	empty, _ := testRepo(t)
	if slices.Contains(keysOfOverview(overviewPairsOn(t, empty, plugin.SurfaceCLI)), "tracking") {
		t.Error("a repository with no commit has a tracking row")
	}
}

// "Up to date" is the picture the last fetch left, and the row says how old
// that picture is once it is more than a day old.
func TestTheTrackingRowSaysHowOldTheLastFetchIs(t *testing.T) {
	dir, repo := testRepo(t)
	first := commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	fetched(t, repo, "origin", "master", first)

	// No FETCH_HEAD is no age: a clone that never fetched has none either, and
	// the row does not say it was fetched at a time nothing records.
	if got := overviewValue(t, dir, "tracking"); got != "origin/master (up to date)" {
		t.Errorf("tracking = %q with no FETCH_HEAD", got)
	}
	fetchedAgo(t, dir, 3*time.Hour)
	if got := overviewValue(t, dir, "tracking"); got != "origin/master (up to date)" {
		t.Errorf("tracking = %q for a fetch three hours old, which is not stale", got)
	}
	fetchedAgo(t, dir, 9*24*time.Hour+time.Hour)
	if got := overviewValue(t, dir, "tracking"); got != "origin/master (up to date, fetched 9d ago)" {
		t.Errorf("tracking = %q, want the age of the picture beside the claim", got)
	}

	commitFile(t, repo, dir, "b.txt", "v1\n", "second commit")
	if got := overviewValue(t, dir, "tracking"); got != "origin/master (1 ahead, fetched 9d ago)" {
		t.Errorf("tracking = %q, want the age inside the same brackets as the drift", got)
	}
}

func TestAStashIsCounted(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")

	if slices.Contains(keysOfOverview(overviewPairsOn(t, dir, plugin.SurfaceCLI)), "stash") {
		t.Error("a repository with no stash has a stash row")
	}
	stashed(t, dir, 2)
	if got := overviewValue(t, dir, "stash"); got != "2" {
		t.Errorf("stash = %q, want the two entries counted", got)
	}
	stashed(t, dir, 0)
	if slices.Contains(keysOfOverview(overviewPairsOn(t, dir, plugin.SurfaceCLI)), "stash") {
		t.Error("an emptied stash has a row")
	}
}

// A merge in progress, an unmerged path and a gone upstream are the three
// states the next command depends on, and each leads with the word the theme
// draws as a warning, on its own row and on the verdict.
func TestWhatTheNextCommandDependsOnIsAWarning(t *testing.T) {
	t.Run("merge in progress", func(t *testing.T) {
		dir, repo := testRepo(t)
		commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
		writeFile(t, dir, ".git/MERGE_HEAD", "x\n")

		pairs := overviewPairsOn(t, dir, plugin.SurfaceCLI)
		if got := valueIn(t, pairs, "state"); got != "warn — merge in progress" || theme.ClassifyStatus(got) != theme.StatusWarn {
			t.Errorf("state = %q", got)
		}
		if got := valueIn(t, pairs, "status"); got != "warn — merge in progress" {
			t.Errorf("status = %q", got)
		}
	})
	t.Run("unmerged paths", func(t *testing.T) {
		dir, repo := testRepo(t)
		commitFile(t, repo, dir, "plain.txt", "v1\n", "initial")
		idx, err := repo.Storer.Index()
		if err != nil {
			t.Fatal(err)
		}
		blob := plumbing.NewHash(strings.Repeat("ab", 20))
		writeFile(t, dir, "both.txt", "<<<<<<<\n")
		for _, stage := range []index.Stage{index.AncestorMode, index.OurMode, index.TheirMode} {
			idx.Entries = append(idx.Entries, &index.Entry{Name: "both.txt", Hash: blob, Mode: 0o100644, Stage: stage})
		}
		if err := repo.Storer.SetIndex(idx); err != nil {
			t.Fatal(err)
		}

		pairs := overviewPairsOn(t, dir, plugin.SurfaceCLI)
		if got := valueIn(t, pairs, "working tree"); got != "warn — 1 unmerged" || theme.ClassifyStatus(got) != theme.StatusWarn {
			t.Errorf("working tree = %q", got)
		}
		if got := valueIn(t, pairs, "status"); got != "warn — 1 unmerged" {
			t.Errorf("status = %q", got)
		}
	})
	t.Run("upstream gone", func(t *testing.T) {
		dir, repo := testRepo(t)
		commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
		trackRemote(t, repo, "master", "origin")

		pairs := overviewPairsOn(t, dir, plugin.SurfaceCLI)
		if got := valueIn(t, pairs, "tracking"); got != "warn — origin/master (gone)" || theme.ClassifyStatus(got) != theme.StatusWarn {
			t.Errorf("tracking = %q", got)
		}
		if got := valueIn(t, pairs, "status"); got != "warn — upstream gone" {
			t.Errorf("status = %q", got)
		}
	})
	t.Run("an operation outranks the paths it left", func(t *testing.T) {
		dir, repo := testRepo(t)
		commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
		trackRemote(t, repo, "master", "origin")
		writeFile(t, dir, ".git/MERGE_HEAD", "x\n")
		if got := overviewValue(t, dir, "status"); got != "warn — merge in progress" {
			t.Errorf("status = %q, want the operation, which explains the rest", got)
		}
	})
}

// A status that could not be read is a row in the words every overview uses,
// and the verdict says so too.
func TestAnUnreadableTreeIsSaidByTheRowAndTheVerdict(t *testing.T) {
	dir := controlNamedRepo(t)
	pairs := overviewPairsOn(t, dir, plugin.SurfaceCLI)
	if got := valueIn(t, pairs, "working tree"); !strings.HasPrefix(got, "could not be read — ") || theme.ClassifyStatus(got) != theme.StatusWarn {
		t.Errorf("working tree = %q", got)
	}
	if got := valueIn(t, pairs, "status"); got != "warn — working tree could not be read" {
		t.Errorf("status = %q", got)
	}
}

func TestTheRowsAreInTheOrderOfWhatIsWorthActingOn(t *testing.T) {
	dir, repo := testRepo(t)
	first := commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	fetched(t, repo, "origin", "master", first)
	commitFile(t, repo, dir, "b.txt", "v1\n", "second commit")
	writeFile(t, dir, "a.txt", "v2\n")
	writeFile(t, dir, ".git/MERGE_HEAD", "x\n")
	stashed(t, dir, 1)

	pairs := overviewPairsOn(t, dir, plugin.SurfaceCLI)
	want := []string{"status", "branch", "tracking", "state", "working tree", "stash", "last commit"}
	if !slices.Equal(keysOfOverview(pairs), want) {
		t.Errorf("rows = %v, want %v", keysOfOverview(pairs), want)
	}
	if len(pairs) > 8 {
		t.Errorf("%d rows", len(pairs))
	}
}

// The page enter opens carries the same rows first, then the tables.
func TestTheDetailPageOpensOnTheSameRows(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	v, err := runOverview(context.Background(), req(t, dir, map[string]any{"detail": true}))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range v.(view.Sections).Items {
		if s.ID == "summary" {
			if got := keysOfOverview(s.View.(view.KeyValue).Pairs); got[0] != "status" {
				t.Errorf("summary rows = %v", got)
			}
			return
		}
	}
	t.Fatal("no summary section")
}

// A tile has no use for who wrote the commit; the terminal and the page do.
func TestOnATileTheLastCommitLeavesOutItsAuthor(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")

	tile := valueIn(t, overviewPairsOn(t, dir, plugin.SurfaceTUI), "last commit")
	if n := utf8.RuneCountInString(tile); strings.Contains(tile, "Ada Lovelace") || !strings.Contains(tile, "initial commit") || n > 42 {
		t.Errorf("tile: last commit = %q (%d characters), want the subject and the age, and no author, within 42", tile, n)
	}
	cli := valueIn(t, overviewPairsOn(t, dir, plugin.SurfaceCLI), "last commit")
	if !strings.Contains(cli, "by Ada Lovelace") {
		t.Errorf("CLI: last commit = %q, want the author", cli)
	}
}

// Reading the stash and the fetch time is reading two files of the repository's
// own directory, through the handle that confines every read of it: a repository
// opened in memory has neither, and says neither.
func TestAnInMemoryRepositoryHasNoStashAndNoFetchTime(t *testing.T) {
	repo, err := git.Init(memory.NewStorage(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := stashCount(repo); n != 0 {
		t.Errorf("stashCount = %d", n)
	}
	if _, ok := lastFetch(repo); ok {
		t.Error("an in-memory repository was fetched at a time")
	}
}
