package app

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/mcp"
	"github.com/this-is-tobi/rta/pkg/format"
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
			t.Errorf("%s never names %s %s", rel,
				format.Plural(len(missing), "the built-in plugin", "the built-in plugins"),
				strings.Join(missing, ", "))
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

// The installation page quotes `rta doctor`, whose first row states the same
// two numbers as the sentence the test above holds. It was left out of that
// test and drifted on its own, reading 18 and 115 after the sentence it
// sits under had moved on — the first command the page tells a new reader to
// run, disagreeing with the page.
func TestTheInstallationPageQuotesDoctorsCounts(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("building the built-in registry: %v", err)
	}
	const rel = "docs/10-getting-started/10-installation.md"
	body := readDoc(t, repoRoot(t), rel)
	m := regexp.MustCompile(`capabilities\s+ok\s+(\d+) plugins, (\d+) capabilities`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("%s no longer quotes doctor's capabilities row; if the sample moved, "+
			"move this test with it", rel)
	}
	if got, _ := strconv.Atoi(m[1]); got != len(reg.Plugins()) {
		t.Errorf("%s quotes doctor counting %d plugins, the registry has %d", rel, got, len(reg.Plugins()))
	}
	if got, _ := strconv.Atoi(m[2]); got != len(reg.Capabilities()) {
		t.Errorf("%s quotes doctor counting %d capabilities, the registry has %d",
			rel, got, len(reg.Capabilities()))
	}
}

// The first-party plugins are counted in words, in four places, and the writing
// chapter's "Ten first-party plugins live in rta-plugins" outlived the twelve
// the other three pages named: the page a plugin author reads to see what has
// been built before them undercounted it by two, with a table beside the
// sentence listing all twelve. Their source lives in another repository, so
// the registry cannot be asked; the README's own list is the one every other
// statement is held to — each "<number> first-party plugins" in the docs, and
// the table of them on the plugins page, must agree with how many it names.
func TestTheDocsCountTheFirstPartyPluginsAlike(t *testing.T) {
	root := repoRoot(t)
	list := regexp.MustCompile("(?s)first-party plugins live in .*? as proof the contract works — (.*?) — each ")
	m := list.FindStringSubmatch(readDoc(t, root, "README.md"))
	if m == nil {
		t.Fatal("README.md no longer lists the first-party plugins after \"as proof the contract works\"; " +
			"if the sentence moved, move this test with it")
	}
	want := len(regexp.MustCompile("`[a-z0-9]+`").FindAllString(m[1], -1))
	if want < 2 {
		t.Fatalf("read %d first-party plugins out of %q", want, m[1])
	}

	words := map[string]int{
		"two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9,
		"ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15,
		"sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19, "twenty": 20,
	}
	stated := regexp.MustCompile(`(?i)\b(\w+) first-party plugins\b`)
	found := 0
	for _, rel := range markdownPages(t, root) {
		for _, s := range stated.FindAllStringSubmatch(readDoc(t, root, rel), -1) {
			n, ok := words[strings.ToLower(s[1])]
			if !ok {
				continue
			}
			found++
			if n != want {
				t.Errorf("%s says %s first-party plugins, the README names %d", rel, s[1], want)
			}
		}
	}
	if found < 3 {
		t.Errorf("found %d statements of how many first-party plugins there are; has the wording changed?", found)
	}

	page := readDoc(t, root, "docs/40-plugins/10-plugins.md")
	_, table, ok := strings.Cut(page, "| Plugin | Service |")
	if !ok {
		t.Fatal("docs/40-plugins/10-plugins.md no longer has the Plugin | Service table; if it moved, move this test with it")
	}
	rows := 0
	for _, line := range strings.Split(table, "\n")[2:] {
		if !strings.HasPrefix(line, "| [`") {
			break
		}
		rows++
	}
	if rows != want {
		t.Errorf("the table on the plugins page has %d rows, the README names %d first-party plugins", rows, want)
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

// A plugin author learns what a declaration can say from the writing chapter,
// and the chapter is prose that a person keeps current by hand. Writing a
// plugin from it alone turned up four Capability fields the SDK offers and the
// chapter never names — Detailed, Prefill, Idempotent and MinWidth — so an
// author who wanted an overview page, an edit form that opens on today's
// values, or a tile that keeps its width could not find that they exist.
//
// The check is the cheap kind: every exported field of the five declaration
// types is named, as a word, somewhere on the two plugin pages. It cannot say
// the sentence is right, only that nobody added a switch to the SDK and left
// its authors to read the source to learn it.
func TestThePluginChaptersNameEveryFieldAPluginDeclares(t *testing.T) {
	root := repoRoot(t)
	pages := readDoc(t, root, "docs/40-plugins/10-plugins.md") + readDoc(t, root, "docs/40-plugins/20-writing-a-plugin.md")

	for _, decl := range []any{plugin.Plugin{}, plugin.Capability{}, plugin.Field{}, plugin.Action{}, plugin.Toggle{}} {
		typ := reflect.TypeOf(decl)
		for i := range typ.NumField() {
			name := typ.Field(i).Name
			if !typ.Field(i).IsExported() {
				continue
			}
			if !regexp.MustCompile(`\b` + name + `\b`).MatchString(pages) {
				t.Errorf("neither plugin page names %s.%s; say what it does where an author declaring a %s would look",
					typ.Name(), name, strings.ToLower(typ.Name()))
			}
		}
	}
}

// What an agent's calls are written into is the record, and `rta agent log`
// is how it is read; the boundary chapters were rewritten to say so after
// "ledger" had been the word in the source for as long as the feature
// existed. A word only the source uses is a word a reader cannot search for
// in the product, and two of them survived that rewrite — one in the grants
// chapter and one in the name of the alert the recipes tell an operator to
// create, which is the one string somebody copies out whole.
func TestTheDocsNameWhatAgentsCallsAreWrittenIntoTheRecord(t *testing.T) {
	root := repoRoot(t)
	ledger := regexp.MustCompile(`(?i)ledger`)
	for _, page := range markdownPages(t, root) {
		for i, line := range strings.Split(readDoc(t, root, page), "\n") {
			if ledger.MatchString(line) {
				t.Errorf("%s:%d says ledger; the docs call it the record, read with `rta agent log`", page, i+1)
			}
		}
	}
}

// rta says "1 entry", and a count of one is singular everywhere the product
// counts — a rule that took a pass over every surface to apply. The sample
// output in the docs is typed by hand, so it is where the old spelling lives
// on: the trees chapter still showed a directory holding "1 entries" after
// the command had stopped printing it, which is the first thing a reader
// comparing the page with their terminal would notice.
//
// A word ending in -ss, -us or -is is left out of the rule because it is a
// singular that happens to end in s ("1 status", "1 process").
func TestTheDocsShowACountOfOneAsSingular(t *testing.T) {
	root := repoRoot(t)
	one := regexp.MustCompile(`(^|[^0-9.,A-Za-z/_-])1 [a-z]{3,}(ies|[^sui]s)\b`)
	for _, page := range markdownPages(t, root) {
		for i, line := range strings.Split(readDoc(t, root, page), "\n") {
			if m := one.FindString(line); m != "" {
				t.Errorf("%s:%d shows %q; a count of one is singular", page, i+1, strings.TrimSpace(m))
			}
		}
	}
}
