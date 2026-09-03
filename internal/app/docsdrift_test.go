package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/mcp"
)

// The docs state two numbers that nothing generates: how many built-in plugins
// there are, and how many capabilities they carry between them. Both were
// wrong, and had been for two releases — README.md said 18 and 115 while
// docs/01-readme.md, which is the same paragraph, said 16 and 106, having
// missed `lock` and `operator` entirely. A reader comparing either against
// `rta plugin list` would have found a third answer.
//
// Nobody is going to notice this by reading. The number is in prose, it is
// plausible at any value, and the commit that makes it wrong is a commit about
// something else — which is the definition of what a drift test is for, and
// why this package already has several. The fix is not to recount by hand once
// more; it is to make the recount CI's job, so the next plugin either updates
// the sentence or fails here with the number to put in it.
func TestTheDocsCountTheBuiltInPluginsCorrectly(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("building the built-in registry: %v", err)
	}
	wantPlugins := len(reg.Plugins())
	wantCaps := len(reg.Capabilities())

	// One sentence, two numbers, repeated verbatim in the root README and the
	// docs copy of it. Matched rather than templated because these are prose
	// files a person edits, not generated ones.
	sentence := regexp.MustCompile(`\*\*(\d+) built-in plugins, (\d+) capabilities\*\*`)

	root := repoRoot(t)
	for _, rel := range []string{"README.md", "docs/01-readme.md"} {
		body := readDoc(t, root, rel)
		m := sentence.FindStringSubmatch(body)
		if m == nil {
			t.Errorf("%s no longer states the built-in plugin and capability counts; "+
				"if the sentence moved, move this test with it", rel)
			continue
		}
		if got, _ := strconv.Atoi(m[1]); got != wantPlugins {
			t.Errorf("%s says %d built-in plugins, the registry has %d", rel, got, wantPlugins)
		}
		if got, _ := strconv.Atoi(m[2]); got != wantCaps {
			t.Errorf("%s says %d capabilities, the registry has %d", rel, got, wantCaps)
		}
	}
}

// The same sentence also names every built-in plugin, and that list drifted in
// its own way: docs/01-readme.md carried fourteen names for sixteen plugins,
// so `lock` and `operator` — the two that take a principal's access away —
// were absent from the only place a reader is told they exist. A count alone
// would not have caught it, because a wrong count and a short list are the
// same edit.
func TestTheDocsNameEveryBuiltInPlugin(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("building the built-in registry: %v", err)
	}
	root := repoRoot(t)

	for _, rel := range []string{"README.md", "docs/01-readme.md"} {
		body := readDoc(t, root, rel)
		var missing []string
		for _, p := range reg.Plugins() {
			// The list renders as `name` · `name` · …, so the backticks are
			// what make this a list entry rather than a prose mention.
			if !strings.Contains(body, "`"+p.Name+"`") {
				missing = append(missing, p.Name)
			}
		}
		if len(missing) > 0 {
			t.Errorf("%s never names built-in plugin(s) %s", rel, strings.Join(missing, ", "))
		}
	}
}

// docs/40-plugins/20-writing-a-plugin.md tells a plugin author which of the eleven shipped
// plugins to read, in order, and states how many capabilities each carries so
// the reader knows what they are opening. Seven of the eleven were stale at
// once — kube said 7 and had 19 — because every feature PR since has grown a
// plugin without touching a table in a different chapter.
//
// Counted from the source rather than from a built binary on purpose: the
// plugins are separate modules, and `rta plugin list` reports whatever the
// operator happens to have installed, which on a development machine is
// routinely an older build than the tree. The declaration is the truth here.
func TestTheWorkedExamplesTableCountsEveryPluginsCapabilities(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, root, "docs/40-plugins/20-writing-a-plugin.md")

	// | [`plugins/kube`](../plugins/kube/) | … | 19 |
	row := regexp.MustCompile(`\[` + "`" + `plugins/([a-z0-9]+)` + "`" + `\]\([^)]*\)[^|]*\|[^|]*\|\s*([0-9 ·]+?)\s*\|`)
	stated := map[string]int{}
	for _, m := range row.FindAllStringSubmatch(body, -1) {
		// A row may pair two plugins and state "6 · 7"; the counts are in the
		// same order the links are, so pair them up by position.
		names := regexp.MustCompile(`\[`+"`"+`plugins/([a-z0-9]+)`+"`"+`\]`).
			FindAllStringSubmatch(m[0], -1)
		counts := strings.Split(m[2], "·")
		if len(names) != len(counts) {
			t.Errorf("row %q pairs %d plugins with %d counts", m[0], len(names), len(counts))
			continue
		}
		for i, n := range names {
			c, err := strconv.Atoi(strings.TrimSpace(counts[i]))
			if err != nil {
				t.Errorf("row for %s states a non-numeric count %q", n[1], counts[i])
				continue
			}
			stated[n[1]] = c
		}
	}

	for name, want := range declaredCapabilityCounts(t, root) {
		got, listed := stated[name]
		if !listed {
			t.Errorf("docs/40-plugins/20-writing-a-plugin.md never lists plugins/%s, which ships in this "+
				"repository — a plugin author reading that table would not know it exists", name)
			continue
		}
		if got != want {
			t.Errorf("docs/40-plugins/20-writing-a-plugin.md says plugins/%s has %d capabilities; it declares %d",
				name, got, want)
		}
	}
}

// declaredCapabilityCounts reads each plugin module's own source for the
// capability IDs it declares. A plugin is a separate module and cannot be
// imported from here, and building eleven binaries to count them would make
// this the slowest test in the package for an answer the text already holds.
func declaredCapabilityCounts(t *testing.T, root string) map[string]int {
	t.Helper()
	// ID: "mysql.dump" — the dotted form, which is what a capability ID is and
	// what nothing else in these files looks like.
	id := regexp.MustCompile(`\bID:\s*"([a-z0-9]+(?:\.[a-z0-9]+)+)"`)

	dirs, err := os.ReadDir(filepath.Join(root, "plugins"))
	if err != nil {
		t.Fatalf("reading plugins/: %v", err)
	}

	counts := map[string]int{}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		seen := map[string]bool{}
		modDir := filepath.Join(root, "plugins", d.Name())
		files, err := os.ReadDir(modDir)
		if err != nil {
			t.Fatalf("reading %s: %v", modDir, err)
		}
		for _, f := range files {
			n := f.Name()
			if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
				continue
			}
			src, err := os.ReadFile(filepath.Join(modDir, n))
			if err != nil {
				t.Fatalf("reading %s: %v", n, err)
			}
			for _, m := range id.FindAllStringSubmatch(string(src), -1) {
				seen[m[1]] = true
			}
		}
		if len(seen) > 0 {
			counts[d.Name()] = len(seen)
		}
	}
	if len(counts) == 0 {
		t.Fatal("found no plugin modules under plugins/, which cannot be right")
	}
	return counts
}

// Every external tool a capability shells out to has to be in the installation
// chapter's table, because that table is the only place a user is told to
// install it — and it is also the list Dockerfile.full's own comment says it
// tracks. mysql and mariadb grew dump/restore pairs that shell out, and both
// the table and that Dockerfile still said those plugins shelled out to
// nothing, so the full image shipped four capabilities that could not run.
func TestTheInstallationTableNamesEveryToolACapabilityShellsOutTo(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, root, "docs/10-getting-started/10-installation.md")

	// Each entry is a tool the plugin looks up at run time and refuses by name
	// when it is absent. Adding a shell-out without adding a row here is the
	// mistake this pins.
	for _, tool := range []string{
		"pg_dump", "pg_restore", "psql",
		"mysqldump", "mysql",
		"mariadb-dump", "mariadb",
		"kubectl", "docker", "git", "ssh", "cosign",
	} {
		if !strings.Contains(body, "`"+tool+"`") {
			t.Errorf("docs/10-getting-started/10-installation.md's external-tools table never names %q, which a "+
				"capability shells out to; a user has no way to learn they need it", tool)
		}
	}
}

// docs/30-boundary/20-mcp.md reproduces what `rta mcp serve --http` prints at startup,
// including the count of capabilities the locality gate hides from a remote
// caller. It said 28 and the answer is 27, which is the same rot as the rest
// of this file with a sharper edge: the whole paragraph is teaching an
// operator what a remote agent can and cannot see, so a reader checking their
// own startup line against the doc finds a number that does not match and has
// no way to tell which one is wrong.
func TestTheMCPChapterCountsTheRemoteHiddenCapabilities(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("building the built-in registry: %v", err)
	}
	want := len(mcp.Options{Remote: true}.RemoteBlocked(reg))

	body := readDoc(t, repoRoot(t), "docs/30-boundary/20-mcp.md")
	line := regexp.MustCompile(`remote transport hides (\d+) capabilities`)
	m := line.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("docs/30-boundary/20-mcp.md no longer shows the remote-transport startup line; " +
			"if it moved, move this test with it")
	}
	if got, _ := strconv.Atoi(m[1]); got != want {
		t.Errorf("docs/30-boundary/20-mcp.md says remote transport hides %d capabilities; it hides %d", got, want)
	}
	// The line prints the count twice — "hides N … (N total)" — and a
	// half-updated sample is worse than a stale one, because the two halves
	// disagreeing reads as a bug in rta rather than in the doc.
	if !strings.Contains(body, "("+m[1]+" total)") {
		t.Errorf("docs/30-boundary/20-mcp.md's sample says %s in one half of the line and something else in "+
			"the other; both halves print the same number", m[1])
	}
}

func readDoc(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(b)
}
