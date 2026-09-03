package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A relative link between two docs files is the one kind of rot nothing here
// catches: `make ci` never opens a markdown file, the site generator resolves
// paths at publish time on someone else's machine, and a reader who follows a
// dead one gets a 404 rather than a message anybody sees. There are around a
// hundred and fifty such links across docs/ and the README, and the first time
// that matters is the first time the tree is reorganised.
//
// So this walks every one of them and opens what it points at. File existence
// only, deliberately — not anchors: heading-to-slug rules differ between the
// site generator and GitHub, and a check that fires on punctuation nobody got
// wrong would be turned off within a week, taking the useful half with it.
func TestEveryLinkBetweenDocsPointsAtAFileThatExists(t *testing.T) {
	root := repoRoot(t)

	// [text](./20-mcp.md) and [text](../plugins/pg/) alike — any relative
	// target, with an optional #anchor that is stripped before the lookup.
	link := regexp.MustCompile(`\[[^\]]*\]\((\.[^)\s]+)\)`)

	for _, page := range markdownPages(t, root) {
		body, err := os.ReadFile(filepath.Join(root, page))
		if err != nil {
			t.Fatalf("reading %s: %v", page, err)
		}
		from := filepath.Dir(page)
		for _, m := range link.FindAllStringSubmatch(string(body), -1) {
			target, _, _ := strings.Cut(m[1], "#")
			if target == "" {
				// A bare "#anchor" is a link within the same page.
				continue
			}
			resolved := filepath.Join(from, target)
			if _, err := os.Stat(filepath.Join(root, resolved)); err != nil {
				t.Errorf("%s links to %s, which does not exist (resolves to %s)",
					page, m[1], resolved)
			}
		}
	}
}

// The docs are also cited from places that are not documentation — a Go doc
// comment pointing at the chapter that explains a decision, the Dockerfile
// naming the table it tracks, a workflow explaining itself. Those citations
// are the ones most likely to be forgotten when a file moves, because nobody
// reorganising docs/ is looking in internal/ or at a Dockerfile.
//
// AGENTS.md already draws this line for .local/: never cite from shipped code
// something a future reader cannot open. This is the same rule, enforced for
// the paths that are allowed to be cited.
func TestEveryDocsPathCitedFromCodeExists(t *testing.T) {
	root := repoRoot(t)
	// The repo-relative form, which is how these are written in prose: the
	// docs directory, any subfolder depth, then a number-prefixed page. Note
	// what this comment does not contain — a literal example path. It would
	// match itself, and the first run of this test failed on exactly that.
	cite := regexp.MustCompile(`\bdocs/(?:[a-z0-9-]+/)*[0-9]{2}-[a-z-]+\.md\b`)

	var checked int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", ".local", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case strings.HasSuffix(path, ".go"),
			strings.HasSuffix(path, ".yml"),
			strings.HasSuffix(path, ".yaml"),
			d.Name() == "Makefile",
			strings.HasPrefix(d.Name(), "Dockerfile"):
		default:
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, cited := range cite.FindAllString(string(body), -1) {
			// The workflows cite the *other* repository's docs by the same
			// shape; only paths inside this checkout are this test's business.
			if strings.Contains(string(body), "github-workflows/"+cited) {
				continue
			}
			checked++
			if _, err := os.Stat(filepath.Join(root, cited)); err != nil {
				t.Errorf("%s cites %s, which does not exist — a reader following that "+
					"comment finds nothing", rel, cited)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the checkout: %v", err)
	}
	if checked == 0 {
		t.Fatal("found no docs citations outside docs/, which cannot be right — " +
			"if the citation style changed, this test needs to change with it")
	}
}

// A path assembled from segments is invisible to the check above, because no
// single string in the file spells it. `filepath.Join(root, "docs", "51-...")`
// is the shape, and it is the one that actually broke: the docs reorganisation
// moved the file, every prose citation was updated, and a test went on opening
// a page that no longer existed — found by running the suite rather than by the
// check written to find exactly this.
//
// So the page name is matched on its own, wherever it appears as a Go string,
// and looked up across the whole docs tree. That is looser than resolving a real
// path and it is the right trade: it cannot say where the file should be, only
// that a page by that name still exists somewhere, which is the fact a
// segment-assembled citation depends on.
func TestEveryDocsPageNamedInGoSourceStillExists(t *testing.T) {
	root := repoRoot(t)

	pages := map[string]bool{}
	for _, page := range markdownPages(t, root) {
		pages[filepath.Base(page)] = true
	}

	name := regexp.MustCompile(`"([0-9]{2}-[a-z-]+\.md)"`)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", ".local", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, m := range name.FindAllStringSubmatch(string(body), -1) {
			if !pages[m[1]] {
				t.Errorf("%s names the docs page %q, and no page by that name exists any more — "+
					"a path assembled from segments does not show up in a search for the whole one",
					rel, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the checkout: %v", err)
	}
}

// markdownPages is every documentation page a link can start from: the docs
// tree at whatever depth it has, plus the README that links into it.
func markdownPages(t *testing.T, root string) []string {
	t.Helper()
	pages := []string{"README.md"}
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			pages = append(pages, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking docs/: %v", err)
	}
	if len(pages) < 2 {
		t.Fatal("found no markdown under docs/, which cannot be right")
	}
	return pages
}
