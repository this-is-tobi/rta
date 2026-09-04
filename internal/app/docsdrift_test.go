package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
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

// The whole-store backups the first-party plugins declare, listed by hand now
// that their source lives in rta-plugins and cannot be read from this tree.
// The other half of the old check — that each receipt carries a `does not
// carry` row — is enforced there, by that repository's `make docs-check`. A
// plugin that ships a new <plugin>.dump or <plugin>.snapshot is added here,
// and this test then fails until the recipes table plans for it.
var wholeStoreBackups = []string{
	"etcd.snapshot",
	"mariadb.dump",
	"mysql.dump",
	"pg.dump",
	"qdrant.dump",
	"vault.snapshot",
}

func TestEveryWholeStoreBackupNamesWhatItLeavesBehind(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, root, "docs/90-recipes/01-readme.md")
	const heading = "Know what your dump does not carry"
	_, table, ok := strings.Cut(body, heading)
	if !ok {
		t.Fatal("docs/90-recipes/01-readme.md no longer has the " + heading +
			" table; if it moved, move this test with it")
	}

	for _, capID := range wholeStoreBackups {
		if !strings.Contains(table, "`"+capID+"`") {
			t.Errorf("%s is absent from the %q table in docs/90-recipes/01-readme.md, which is "+
				"where a backup strategy gets planned", capID, heading)
		}
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
