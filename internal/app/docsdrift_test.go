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
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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
	for _, rel := range []string{"README.md"} {
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

	for _, rel := range []string{"README.md"} {
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

// The README names the built-in plugins and `rta plugin list` prints them, but
// no page said what `http`, `time`, `keys` or `debug` are for, or how to start
// with one: a reader of the docs alone met their names and nothing else. The
// plugins page carries a table of them, and it says what each is for in the
// plugin's own words, held here to the declaration rather than to a copy of it
// that a rewording would leave behind.
func TestThePluginsPageSaysWhatEveryBuiltInPluginIsFor(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("building the built-in registry: %v", err)
	}
	body := readDoc(t, repoRoot(t), "docs/40-plugins/10-plugins.md")
	for _, p := range reg.Plugins() {
		if !strings.Contains(body, "| `"+p.Name+"` | "+p.Summary+" | `rta "+p.Name+" ") {
			t.Errorf("docs/40-plugins/10-plugins.md has no row for the built-in plugin %s saying %q and giving a first command", p.Name, p.Summary)
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

// The hosting chapter says what a remote server leaves out twice over: a
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
func TestTheHostingChapterNamesWhatARemoteServerHides(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("building the built-in registry: %v", err)
	}
	hidden := mcp.Options{Remote: true}.RemoteBlocked(reg)
	const rel = "docs/30-boundary/65-hosting-a-server.md"
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

// pluginPages is the text of every page about plugins, the using page and the
// authoring pages together, for a check that a word is said somewhere among
// them.
func pluginPages(t *testing.T, root string) string {
	t.Helper()
	pages, err := filepath.Glob(filepath.Join(root, "docs", "40-plugins", "*.md"))
	if err != nil || len(pages) < 5 {
		t.Fatalf("found %d plugin pages (%v); want the using page and the authoring pages", len(pages), err)
	}
	var all strings.Builder
	for _, page := range pages {
		rel, _ := filepath.Rel(root, page)
		all.WriteString(readDoc(t, root, rel))
	}
	return all.String()
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
// The check is the cheap kind: every exported field of the declaration and view
// types is named, as a word, somewhere on the plugin pages. It cannot say
// the sentence is right, only that nobody added a switch to the SDK and left
// its authors to read the source to learn it.
func TestThePluginChaptersNameEveryFieldAPluginDeclares(t *testing.T) {
	root := repoRoot(t)
	pages := pluginPages(t, root)

	// An error is left out: its code, message and hint are what Errorf and
	// WithHint take, and the chapter shows them as those calls.
	for _, decl := range []any{
		plugin.Plugin{}, plugin.Capability{}, plugin.Field{}, plugin.Action{}, plugin.Toggle{},
		view.Text{}, view.Table{}, view.Column{}, view.KeyValue{}, view.Pair{}, view.Tree{}, view.Node{},
		view.Chart{}, view.Series{}, view.Sections{}, view.Section{},
	} {
		typ := reflect.TypeOf(decl)
		for i := range typ.NumField() {
			name := typ.Field(i).Name
			if !typ.Field(i).IsExported() {
				continue
			}
			if !regexp.MustCompile(`\b` + name + `\b`).MatchString(pages) {
				t.Errorf("no plugin page names %s.%s; say what it does where an author using %s would look",
					typ.Name(), name, typ.Name())
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
	ledger := regexp.MustCompile(`(?i)ledger|audit log`)
	for _, page := range markdownPages(t, root) {
		for i, line := range strings.Split(readDoc(t, root, page), "\n") {
			if ledger.MatchString(line) {
				t.Errorf("%s:%d says ledger or audit log; the docs call it the record, read with `rta agent log`", page, i+1)
			}
		}
	}
}

// The same word, from the other side: a message the binary prints is read by
// somebody who has only the docs and `rta agent log` to go on, and two of the
// operator roster's refusals and the container audit's advice still said "the
// audit trail" for what every page calls the record. Only a string a person
// can be shown is read, not a comment, and not a struct tag.
func TestNoMessageCallsTheRecordALedgerOrAnAuditTrail(t *testing.T) {
	root := repoRoot(t)
	literal := regexp.MustCompile(`"(?:[^"\\\n]|\\.)*"`)
	word := regexp.MustCompile(`(?i)ledger|audit log|audit trail`)
	for _, dir := range []string{"cmd", "internal", "pkg", "builtin"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			for i, line := range strings.Split(string(body), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "//") || strings.Contains(line, "json:\"") {
					continue
				}
				for _, s := range literal.FindAllString(line, -1) {
					if word.MatchString(s) {
						t.Errorf("%s:%d prints %s; the product calls what an agent's calls are written into the record", rel, i+1, s)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("reading the source under %s: %v", dir, err)
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

// The README's table of contents is the one list of the docs a reader on
// GitHub sees, and the site builds its own sidebar from the tree: a page added
// to the tree and not to the list is on the site and invisible from the
// repository, which is how a chapter goes unread for a release.
func TestTheReadmeListsEveryDocsPage(t *testing.T) {
	root := repoRoot(t)
	readme := readDoc(t, root, "README.md")
	for _, page := range markdownPages(t, root) {
		if page == "README.md" || page == "docs/01-readme.md" {
			continue
		}
		if !strings.Contains(readme, "(./"+page+")") {
			t.Errorf("README.md's table of contents does not list %s", page)
		}
	}
}

// Prose that says "RBAC" or "SLSA" to somebody who has not met it is where a
// page stops being readable, and nobody writing the sentence can see it,
// because they know the word. So a capitalised abbreviation in the docs'
// prose is either one every working programmer knows — listed below, and
// short on purpose — or it is in the glossary, which is what the reader is
// sent to. A new one fails here with the name to add.
//
// Code spans and fenced blocks are skipped: what is typed or printed is
// quoted, not explained.
func TestTheGlossaryExplainsEveryAcronymTheDocsUse(t *testing.T) {
	root := repoRoot(t)
	known := map[string]bool{}
	for _, w := range strings.Fields(`AI API ASCII CI CPU CSV DNS ES256 GID HEAD HMAC HTTP IAM ID IP JSON
		JSONL JWT KV MB OS PATH PEM PS256 README RPC RPM RS256 S3 SDK SQL SSH TCP TOML UI UID URL UUID VPN VS YAML`) {
		known[w] = true
	}
	glossary := readDoc(t, root, "docs/95-reference/10-glossary.md")
	span := regexp.MustCompile("`[^`\n]*`")
	link := regexp.MustCompile(`\]\([^)]*\)`)
	word := regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,6}\b`)

	used := map[string]string{}
	for _, page := range markdownPages(t, root) {
		if page == "docs/95-reference/10-glossary.md" {
			continue
		}
		fenced := false
		for i, line := range strings.Split(readDoc(t, root, page), "\n") {
			if strings.HasPrefix(line, "```") {
				fenced = !fenced
				continue
			}
			if fenced {
				continue
			}
			line = link.ReplaceAllString(span.ReplaceAllString(line, ""), "]")
			for _, w := range word.FindAllString(line, -1) {
				if _, seen := used[w]; !seen {
					used[w] = page + ":" + strconv.Itoa(i+1)
				}
			}
		}
	}
	for w, where := range used {
		if known[w] || strings.Contains(glossary, "**"+w+"**") {
			continue
		}
		t.Errorf("%s uses %s, which neither the glossary explains nor the test counts as known to every reader", where, w)
	}
}

// The suite an author is told to pass can be told to look away, one capability
// and one rule at a time, with `sdktest.Skip` — and the rules it takes were
// named nowhere in the chapter, so the message that offers the way out (and
// the redaction warning that every plugin with a secret input meets first)
// sent an author to the source to learn what the word meant. The rules are
// read from the source, not listed here, so a new one fails this test until
// the chapter says what it checks.
func TestThePluginChapterNamesEveryRuleTheSuiteCanBeToldToSkip(t *testing.T) {
	root := repoRoot(t)
	chapter := readDoc(t, root, "docs/40-plugins/24-testing-and-publishing.md")
	rule := regexp.MustCompile(`(?m)^\t(Rule[A-Z]\w*) Rule = `)
	src := readDoc(t, root, "pkg/sdk/sdktest/sdktest.go")
	found := rule.FindAllStringSubmatch(src, -1)
	if len(found) < 6 {
		t.Fatalf("found %d rules in sdktest.go; has the declaration moved?", len(found))
	}
	for _, m := range found {
		if m[1] == "RuleDeclaration" {
			continue
		}
		if !strings.Contains(chapter, m[1]) {
			t.Errorf("the testing page does not name sdktest.%s, which Skip takes", m[1])
		}
	}
}

// `rta doctor`'s confinement row is quoted as a sample where it is explained,
// the plugins page, and it was once quoted on the installation page too — the
// two said different numbers of pinned directories, 15 and 9. The figure is
// per machine, which is why the page tells the reader to read their own row,
// but two samples of one row on one site that disagree read as one of them
// being stale. Every quote is held to the same number, so a sample added to
// another page has to take the first with it.
func TestTheDocsQuoteTheConfinementRowAlike(t *testing.T) {
	root := repoRoot(t)
	pinned := regexp.MustCompile(`(\d+)\s+directories\s+pinned`)
	pagesByCount := map[string][]string{}
	quotes := 0
	for _, page := range markdownPages(t, root) {
		for _, m := range pinned.FindAllStringSubmatch(readDoc(t, root, page), -1) {
			pagesByCount[m[1]] = append(pagesByCount[m[1]], page)
			quotes++
		}
	}
	if quotes < 1 {
		t.Fatal("found no quote of the confinement row's pinned directories, which the plugins page samples; " +
			"if the sample moved, move this test with it")
	}
	if len(pagesByCount) > 1 {
		t.Errorf("the docs quote the confinement row with different numbers of pinned directories: %v", pagesByCount)
	}
}

// A plugin's build is named by the first twelve characters of its digest
// everywhere rta prints one — `rta doctor`, `rta profile show`, the key a
// config block is filed under, the error that names it — and the profiles
// chapter quoted two of them at eight, `pg@685186a7` and `mysql@f5074594`,
// beside recipes that quoted the same kind of name at twelve. Somebody
// copying a key out of the chapter into their config got a block that matched
// no build.
func TestADigestTheDocsQuoteIsSpelledAtTheLengthRtaPrintsIt(t *testing.T) {
	root := repoRoot(t)
	pin := regexp.MustCompile(`\b[a-z][a-z0-9]*(?:/[a-z0-9-]+)?@([0-9a-f]{6,64})\b`)
	quoted := 0
	for _, page := range markdownPages(t, root) {
		for i, line := range strings.Split(readDoc(t, root, page), "\n") {
			for _, m := range pin.FindAllStringSubmatch(line, -1) {
				quoted++
				if len(m[1]) != 12 {
					t.Errorf("%s:%d quotes %q, a digest of %d characters; rta prints twelve", page, i+1, m[0], len(m[1]))
				}
			}
		}
	}
	if quoted < 5 {
		t.Fatalf("found %d quoted plugin digests, want the half dozen the docs hold; has the spelling moved?", quoted)
	}
}

// `theme:` is a config key like any other, and the only place a reader could
// learn its ten names was the theme editor on `t` and an error from `rta
// doctor` after a wrong one: no page said the block exists. The dashboard and
// theme page names each colour now, so a colour added to the palette fails
// here until the page does too.
func TestTheThemePageNamesEveryColourAThemeBlockTakes(t *testing.T) {
	const rel = "docs/20-using/25-dashboard-and-theme.md"
	chapter := readDoc(t, repoRoot(t), rel)
	for _, name := range theme.Fields() {
		if !strings.Contains(chapter, "`"+name+"`") {
			t.Errorf("%s does not name the theme colour `%s`", rel, name)
		}
	}
}

// The table of where rta keeps things is the one page a person opens to find a
// file, and a row written from a branch that had not landed named a directory
// (`plugins/run/`) and a seal key (`profile.key`) that no code on this tree
// ever creates, so a reader looked for them and found the data directory
// without. Each file or directory the section names is held to the source:
// every segment of it has to appear as a quoted string in code that is not a
// test, which is how rta spells a path it builds. Looser than resolving the
// path, as the check on cited pages is, and for the same reason: it cannot say
// where a file is, only that nothing in rta could have made one by that name.
func TestTheFilesThePagesSayRtaKeepsAreNamesRtaUses(t *testing.T) {
	root := repoRoot(t)
	const rel = "docs/95-reference/50-where-rta-keeps-things.md"
	page := readDoc(t, root, rel)
	_, section, ok := strings.Cut(page, "## The files")
	if !ok {
		t.Fatal(rel + " no longer has a `The files` section; if it moved, move this test with it")
	}
	section, _, _ = strings.Cut(section, "\n## ")

	var src strings.Builder
	for _, dir := range []string{"cmd", "internal", "pkg", "builtin"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			body, err := os.ReadFile(path)
			src.Write(body)
			return err
		})
		if err != nil {
			t.Fatalf("reading the source under %s: %v", dir, err)
		}
	}

	checked := 0
	for _, m := range regexp.MustCompile("`([^`\n]+)`").FindAllStringSubmatch(section, -1) {
		name := m[1]
		if strings.ContainsAny(name, "~$< ") || (!strings.Contains(name, ".") && !strings.HasSuffix(name, "/")) {
			continue
		}
		segments := strings.Split(strings.Trim(name, "/"), "/")
		for _, segment := range segments {
			if segment == "" || segment == "." {
				continue
			}
			checked++
			if !strings.Contains(src.String(), `"`+segment+`"`) {
				t.Errorf("%s says rta keeps `%s`, and nothing in rta's source spells %q", rel, name, segment)
			}
		}
		// A directory under another is built as Join(data, "plugins", "store"),
		// and each word alone is in the source for some other reason: "run"
		// is a verb, so `plugins/run/` passed the check above.
		if len(segments) > 1 {
			joined := `"` + strings.Join(segments, `",\s*"`) + `"`
			if !regexp.MustCompile(joined).MatchString(src.String()) && !strings.Contains(src.String(), strings.Trim(name, "/")) {
				t.Errorf("%s says rta keeps `%s`, and nothing in rta's source builds that path", rel, name)
			}
		}
	}
	if checked < 15 {
		t.Fatalf("checked %d names in the section; has its table changed shape?", checked)
	}
}
