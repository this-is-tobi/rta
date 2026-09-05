package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatal(err)
	}
}

// When is set on every commit these fixtures make: go-git leaves a zero
// Signature.When alone, so a fixture without one produces commits dated 1970 —
// which reads as "57 years ago" in any view that reports an age, and as a bug
// in the view rather than in the fixture.
func signature() *object.Signature {
	return &object.Signature{Name: "Ada Lovelace", Email: "ada@example.com", When: time.Now()}
}

var testAuthor = signature()

// testRepo initializes a real, on-disk repository in a temp directory —
// every capability here opens a real *git.Repository, so a fixture built
// from anything less would only prove the fixture, not the plugin.
func testRepo(t *testing.T) (dir string, repo *git.Repository) {
	t.Helper()
	dir = t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return dir, repo
}

// commitFile writes content to name (relative to the repo root), stages it,
// and commits it, returning the new commit's hash.
func commitFile(t *testing.T, repo *git.Repository, dir, name, content, message string) plumbing.Hash {
	t.Helper()
	return commitFileAt(t, repo, dir, name, content, message, time.Now())
}

// commitFileAt is commitFile with the commit's date under the test's control,
// for the views that report an age rather than a timestamp.
func commitFileAt(t *testing.T, repo *git.Repository, dir, name, content, message string, when time.Time) plumbing.Hash {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add(name); err != nil {
		t.Fatal(err)
	}
	author := signature()
	author.When = when
	hash, err := wt.Commit(message, &git.CommitOptions{Author: author})
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

// writeFile writes content to name (relative to dir) without staging or
// committing it — the fixture for an untracked or unstaged-modification test.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func req(t *testing.T, path string, values map[string]any) plugin.Request {
	t.Helper()
	if values == nil {
		values = map[string]any{}
	}
	if _, ok := values["path"]; !ok {
		values["path"] = path
	}
	return plugin.NewRequest(values, false, false)
}

func table(t *testing.T, h plugin.Handler, r plugin.Request) view.Table {
	t.Helper()
	v, err := h(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("want Table, got %s", view.TypeOf(v))
	}
	return tbl
}

func text(t *testing.T, h plugin.Handler, r plugin.Request) string {
	t.Helper()
	v, err := h(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	txt, ok := v.(view.Text)
	if !ok {
		t.Fatalf("want Text, got %s", view.TypeOf(v))
	}
	return txt.Body
}

// rowFor returns the row whose named column matches want, or fails the test.
func rowFor(t *testing.T, tbl view.Table, col, want string) []string {
	t.Helper()
	idx := -1
	for i, c := range tbl.Columns {
		if c.Name == col {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("no %q column in %v", col, tbl.Columns)
	}
	for _, row := range tbl.Rows {
		if row[idx] == want {
			return row
		}
	}
	t.Fatalf("no row with %s = %q in %v", col, want, tbl.Rows)
	return nil
}
