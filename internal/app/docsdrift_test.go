package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/mcp"
	"github.com/this-is-tobi/rta/pkg/plugin"
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

// The MCP chapter says what a remote server leaves out twice over: a
// paragraph naming the capabilities, and the startup line quoted under it
// with their count. Both had drifted from HostSpecific — net.listen joined
// the hidden set and the paragraph never named it, and the quoted line kept
// a count of 28, and a "(28 total)" suffix, long after the binary printed
// neither. The commit that marks a capability HostSpecific is about that
// capability, and nothing in it sends anyone to reread this page.
//
// A bare namespace names its capabilities only when the gate hides every
// one of them, the way it hides sys, fs and git. The paragraph also says
// "the parts of `net`", and counting that as naming net.listen is exactly
// the reading that let the omission through.
func TestTheMCPChapterNamesWhatARemoteServerHides(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("building the built-in registry: %v", err)
	}
	hidden := mcp.Options{Remote: true}.RemoteBlocked(reg)
	const rel = "docs/30-boundary/20-mcp.md"
	body := readDoc(t, repoRoot(t), rel)

	m := regexp.MustCompile(`remote transport hides (\d+) capabilities`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("%s no longer quotes the startup line naming what a remote server hides; "+
			"if it moved, move this test with it", rel)
	}
	if got, _ := strconv.Atoi(m[1]); got != len(hidden) {
		t.Errorf("%s quotes a remote server hiding %d capabilities, the locality gate hides %d",
			rel, got, len(hidden))
	}

	const marker = "answer for the machine rta happens to run on"
	var paragraph string
	for _, p := range strings.Split(body, "\n\n") {
		if strings.Contains(p, marker) {
			paragraph = p
			break
		}
	}
	if paragraph == "" {
		t.Fatalf("%s no longer has the paragraph saying what %q; if it was reworded, "+
			"move this test with it", rel, marker)
	}
	isHidden := map[string]bool{}
	for _, id := range hidden {
		isHidden[id] = true
	}
	partly := map[string]bool{}
	for _, c := range reg.Capabilities() {
		if !isHidden[c.ID] {
			partly[plugin.Namespace(c.ID)] = true
		}
	}
	var unnamed []string
	for _, id := range hidden {
		if !namesCapability(paragraph, id, !partly[plugin.Namespace(id)]) {
			unnamed = append(unnamed, id)
		}
	}
	if len(unnamed) > 0 {
		t.Errorf("%s hides %s from a remote caller and never says so: name each by ID, by a "+
			"`prefix.*` that covers it, or by its plugin when every capability of that plugin is hidden",
			rel, strings.Join(unnamed, ", "))
	}
}

// namesCapability reports whether prose names id in backticks: the ID itself,
// a `prefix.*` covering it, or — when wholeNamespace says the gate takes all
// of it — the bare plugin name.
func namesCapability(prose, id string, wholeNamespace bool) bool {
	if strings.Contains(prose, "`"+id+"`") {
		return true
	}
	if wholeNamespace && strings.Contains(prose, "`"+plugin.Namespace(id)+"`") {
		return true
	}
	parts := strings.Split(id, ".")
	for i := len(parts) - 1; i >= 1; i-- {
		if strings.Contains(prose, "`"+strings.Join(parts[:i], ".")+".*`") {
			return true
		}
	}
	return false
}

func readDoc(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(b)
}
