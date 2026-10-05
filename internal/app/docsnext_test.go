package app

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The docs are read as tracks, not as a shelf (Start here lists them), and a
// track is only a track if every page says which one comes after it. Before
// this, five pages ended with nothing, Grants, the record and Team policy
// pointed at one another in a ring, and the TUI page pointed back at the CLI,
// so a reader who followed the links to the end arrived where they had begun.
//
// Each page therefore ends with one Next: a single link, in the last section
// of the page. Anything else a page wants to point at goes under Related,
// which is where the links that used to share Next with it went. Start here
// is the one page with no Next, being where tracks begin, and the reference
// pages (the glossary, the path gate and the like) are looked things up in,
// not read on from.
func TestEveryDocsPageEndsWithOneNext(t *testing.T) {
	root := repoRoot(t)
	link := regexp.MustCompile(`\[[^\]]+\]\([^)\s]+\)`)
	checked := 0
	for _, page := range markdownPages(t, root) {
		if exemptFromNext(page) {
			continue
		}
		checked++
		_, after, ok := strings.Cut(readDoc(t, root, page), "\n## Next\n")
		if !ok {
			t.Errorf("%s has no Next section; every page ends by naming the one that follows it", page)
			continue
		}
		if strings.Contains(after, "\n## ") {
			t.Errorf("%s has a section after Next; Next is the last thing on a page", page)
		}
		if n := len(link.FindAllString(after, -1)); n != 1 {
			t.Errorf("%s has %d links under Next; a page names one primary Next and keeps the rest under Related", page, n)
		}
	}
	if checked < 25 {
		t.Fatalf("checked %d pages; has the docs tree changed shape?", checked)
	}
}

// Following each page's Next to its end has to reach Start here or a page with
// no Next, never come back to a page already passed: a loop is a reader told
// to go on to somewhere they have been.
func TestTheNextLinksNeverLoop(t *testing.T) {
	root := repoRoot(t)
	link := regexp.MustCompile(`\[[^\]]+\]\(([^)\s]+)\)`)
	next := map[string]string{}
	for _, page := range markdownPages(t, root) {
		if exemptFromNext(page) {
			continue
		}
		_, after, ok := strings.Cut(readDoc(t, root, page), "\n## Next\n")
		if !ok {
			continue
		}
		m := link.FindStringSubmatch(after)
		if m == nil {
			continue
		}
		target, _, _ := strings.Cut(m[1], "#")
		next[page] = filepath.ToSlash(filepath.Join(filepath.Dir(page), target))
	}
	for start := range next {
		seen := map[string]bool{start: true}
		for at := next[start]; at != ""; at = next[at] {
			if seen[at] {
				t.Errorf("following Next from %s comes round to %s again", start, at)
				break
			}
			seen[at] = true
		}
	}
}

// exemptFromNext is the pages that are not read on from: the README, Start
// here, and the reference pages.
func exemptFromNext(page string) bool {
	return page == "README.md" || page == "docs/01-readme.md" || strings.HasPrefix(page, "docs/95-reference/")
}

// The recipes page opens with a table of every recipe, its level, what it
// needs and about how long it takes, because a page of recipes with no index
// was read by scrolling, and the one it calls the one worth learning first
// fails on a fresh machine in its first two commands without saying what it
// presumes. A recipe added without a row in the table is one nobody
// can see the level or the prerequisites of.
func TestEveryRecipeIsInTheRecipesIndex(t *testing.T) {
	const rel = "docs/90-recipes/01-readme.md"
	page := readDoc(t, repoRoot(t), rel)
	table, _, _ := strings.Cut(page, "\nThree have pages of their own.")
	heading := regexp.MustCompile(`(?m)^## (.+)$`)
	recipes := 0
	for _, m := range heading.FindAllStringSubmatch(page, -1) {
		if m[1] == "Related" || m[1] == "Next" {
			continue
		}
		recipes++
		if !strings.Contains(table, "["+m[1]+"](#") {
			t.Errorf("%s has the recipe %q and the index at its head has no row for it", rel, m[1])
		}
	}
	if recipes < 15 {
		t.Fatalf("found %d recipes; has the page changed shape?", recipes)
	}
	for _, row := range strings.Split(table, "\n") {
		if !strings.HasPrefix(row, "| [") {
			continue
		}
		if cells := strings.Split(strings.Trim(row, "| "), " | "); len(cells) != 4 {
			t.Errorf("the recipes index row %q has %d cells; a recipe says its level, what it needs and its minutes", row, len(cells))
		}
	}
}

// The glossary is where a reader is sent for a word the page assumes, and a
// glossary that is linked from two places is a glossary nobody reaches: the
// first use of a term on a page is where somebody who landed there from a
// search needs it. So the first prose use, on each page, of an acronym the
// glossary defines (and of the two terms of art it explains that no other
// page does) is a link to it, or sits inside a link that already names a
// page about it. The basics every reader of a terminal knows, CLI and TUI,
// and the project's own name are left alone, and so are code spans, headings
// and table rows, which are quoted rather than read.
func TestTheFirstUseOfAGlossaryTermOnAPageLinksToTheGlossary(t *testing.T) {
	root := repoRoot(t)
	glossary := readDoc(t, root, "docs/95-reference/10-glossary.md")
	_, acronymBlock, ok := strings.Cut(glossary, "## Acronyms")
	if !ok {
		t.Fatal("the glossary no longer has an Acronyms section; if it moved, move this test with it")
	}
	type term struct {
		word   string
		finder *regexp.Regexp
	}
	var terms []term
	for _, m := range regexp.MustCompile(`(?m)^- \*\*([A-Za-z0-9]+)\*\* —`).FindAllStringSubmatch(acronymBlock, -1) {
		switch m[1] {
		case "RTA", "CLI", "TUI":
			continue
		}
		terms = append(terms, term{m[1], wordFinder(m[1], false)})
	}
	for _, w := range []string{"roster", "safety class"} {
		terms = append(terms, term{w, wordFinder(w, true)})
	}
	if len(terms) < 20 {
		t.Fatalf("read %d glossary terms; has the glossary changed shape?", len(terms))
	}

	hidden := regexp.MustCompile("`[^`\n]*`|\\]\\([^)\n]*\\)|<https?://[^>]*>|https?://\\S+")
	linkText := regexp.MustCompile(`\[[^\]\n]*\]`)
	separator := regexp.MustCompile(`^\|[\s:|-]+\|\s*$`)

	for _, page := range markdownPages(t, root) {
		if page == "docs/95-reference/10-glossary.md" {
			continue
		}
		done := map[string]bool{}
		fenced := false
		lines := strings.Split(readDoc(t, root, page), "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "```") {
				fenced = !fenced
				continue
			}
			if fenced || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "|") {
				continue
			}
			if i+1 < len(lines) && separator.MatchString(lines[i+1]) {
				continue
			}
			masked := hidden.ReplaceAllStringFunc(line, func(s string) string {
				if strings.HasPrefix(s, "]") {
					return "]" + strings.Repeat("\x00", len(s)-1)
				}
				return strings.Repeat("\x00", len(s))
			})
			for _, tm := range terms {
				if done[tm.word] {
					continue
				}
				hit := tm.finder.FindStringSubmatchIndex(masked)
				if hit == nil {
					continue
				}
				done[tm.word] = true
				start := hit[2]
				inLink := false
				for _, span := range linkText.FindAllStringIndex(masked, -1) {
					if span[0] <= start && start < span[1] {
						inLink = true
						break
					}
				}
				if !inLink {
					t.Errorf("%s:%d uses %q for the first time without linking it to the glossary", page, i+1, tm.word)
				}
			}
		}
	}
}

// wordFinder matches word as a whole word, capturing it, not as part of a
// path, a flag or a longer name: `A-Z` acronyms exactly, the terms of art in
// any case.
func wordFinder(word string, anyCase bool) *regexp.Regexp {
	flags := ""
	if anyCase {
		flags = "(?i)"
	}
	return regexp.MustCompile(flags + `(?:^|[^\w/.-])(` + regexp.QuoteMeta(word) + `)(?:$|[^\w/-])`)
}
