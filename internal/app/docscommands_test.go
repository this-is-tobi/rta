package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The quickstart told a new reader to run `rta cert check example.com`, and
// no such verb exists — cert has chain, expiry, inspect, pem and tls. It sat
// on the second page anyone reads, in a bash block, for long enough to ship,
// because a command spelled in prose is checked by nobody and looks right at
// any value. The count test beside this one exists for the same reason; this
// one covers the spelling of every command the docs ask a reader to type.
//
// Checked against the real command tree rather than a list kept here, so a
// renamed verb fails the page that still uses the old name, and a management
// command (`rta plugin index add`) is covered exactly as a capability is. A
// hidden alias (`rta kv ls`) is a command a page may spell; so is the name a
// group with a default verb takes (`rta lock claude`).
// Only what reads as a command is checked: fenced shell blocks and inline
// code spans. Output blocks — the ones with no language — quote rta's own
// sentences ("rta mcp server listening on stdio"), which are not commands
// and must not be read as one.
func TestEveryCommandTheDocsSpellExists(t *testing.T) {
	// The first-party plugins' commands are in this tree too (docscatalogue_test.go),
	// so `rta pg qeury` is a verb nobody can run and not the name of a plugin this
	// build happens not to carry.
	tree := commandTree(documentedRegistry(t))

	repo := repoRoot(t)
	pages, err := filepath.Glob(filepath.Join(repo, "docs", "*", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	top, _ := filepath.Glob(filepath.Join(repo, "docs", "*.md"))
	pages = append(append(pages, top...), filepath.Join(repo, "README.md"))
	if len(pages) < 10 {
		t.Fatalf("found only %d doc pages under %s", len(pages), repo)
	}
	for _, page := range pages {
		rel, _ := filepath.Rel(repo, page)
		for _, snippet := range commandSnippets(readDoc(t, repo, rel)) {
			for _, m := range spelledCommand.FindAllStringSubmatch(snippet.text, -1) {
				if problem := unknownVerb(tree, strings.Fields(m[1])); problem != "" {
					t.Errorf("%s:%d: %s", rel, snippet.line, problem)
				}
			}
		}
	}
}

// rtaCommandTree is every command of the real tree by its full path, `rta
// kv get` and the like. NewRoot attaches cobra's own completion and help, so
// `rta completion zsh` and `rta help` are in it like every other command.
func rtaCommandTree(t *testing.T) map[string]*cobra.Command {
	t.Helper()
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	return commandTree(reg)
}

// commandTree is every command NewRoot builds over reg, by its full path.
func commandTree(reg *registry.Registry) map[string]*cobra.Command {
	tree := map[string]*cobra.Command{}
	var walk func(c *cobra.Command, prefix string)
	walk = func(c *cobra.Command, prefix string) {
		for _, sub := range c.Commands() {
			p := prefix + " " + sub.Name()
			tree[p] = sub
			for _, alias := range sub.Aliases {
				tree[prefix+" "+alias] = sub
			}
			walk(sub, p)
		}
	}
	walk(NewRoot(reg, "test"), "rta")
	return tree
}

// unknownVerb is what is wrong with `rta` followed by words, or "" when
// nothing is: the first word is not one of rta's own nouns (a plugin from
// another repository, or prose that happened to start with it), or the words
// reach a command, or a leaf that takes whatever follows as arguments. What it
// refuses is a word after a group that is not one of its verbs.
func unknownVerb(tree map[string]*cobra.Command, words []string) string {
	if _, known := tree["rta "+words[0]]; !known {
		return ""
	}
	longest := 0
	for k := len(words); k >= 1; k-- {
		if _, ok := tree["rta "+strings.Join(words[:k], " ")]; ok {
			longest = k
			break
		}
	}
	cmd := tree["rta "+strings.Join(words[:longest], " ")]
	// A leaf takes arguments, so whatever follows it is fine. A group takes
	// verbs, so a word after it that is not one is a command nobody can run.
	if longest == len(words) || !cmd.HasSubCommands() {
		return ""
	}
	if _, takesName := defaultVerbs[cmd.CommandPath()]; takesName {
		return ""
	}
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	hint := ""
	if s := cmd.SuggestionsFor(words[longest]); len(s) > 0 {
		hint = " — did you mean " + strings.Join(s, " or ") + "?"
	}
	return fmt.Sprintf("`rta %s` names no command: %q is not a verb of `%s`%s",
		strings.Join(words, " "), words[longest], cmd.CommandPath(), hint)
}

// spelledCommand is `rta` followed by one to three words, bounded on the
// left so that rta-plugin-pg, ghcr.io/…/rta:latest and rta_0.1.0_linux are
// not read as commands. Three words is the deepest the tree goes.
var spelledCommand = regexp.MustCompile("(?:^|[\\s$(|;&`'\"])rta((?: [a-z][a-z0-9-]*){1,3})")

type snippet struct {
	line int
	text string
}

// commandFences are the fence languages whose contents are commands a reader
// types. A fence with no language is output rta printed, and yaml, json and
// go carry rta only as a string.
var commandFences = map[string]bool{
	"bash": true, "sh": true, "shell": true, "zsh": true, "console": true, "dockerfile": true,
}

// commandSnippets yields every line of a shell fence and every inline code
// span outside one, with the line it came from.
func commandSnippets(body string) []snippet {
	var out []snippet
	inFence, fenceIsCommand := false, false
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inFence {
				inFence, fenceIsCommand = false, false
			} else {
				inFence = true
				fenceIsCommand = commandFences[strings.TrimPrefix(trimmed, "```")]
			}
			continue
		}
		if inFence {
			if fenceIsCommand {
				out = append(out, snippet{line: i + 1, text: line})
			}
			continue
		}
		parts := strings.Split(line, "`")
		// Odd indices are inside backticks.
		for j := 1; j < len(parts); j += 2 {
			out = append(out, snippet{line: i + 1, text: parts[j]})
		}
	}
	return out
}

// The spelling test above checks that a command the docs ask for exists. That
// is not that it can be typed: the installation page ended on `rta net probe
// db.internal:5432`, a verb that exists, taking a host and a port as two
// arguments, given one. A reader copying it got "missing <port>" from the one
// page that sends them into a cluster to debug a database.
//
// So every command line in a shell fence is parsed as the command it names —
// its flags, then its arguments — by the real tree, and refused where the tree
// would refuse it. Only what can be judged from the text alone: a line with a
// placeholder, an ellipsis or a substitution is skipped, and so is anything
// after a pipe or a redirect.
//
// The tree has the first-party plugins' commands in it (docscatalogue_test.go).
// Without them, a line like `rta pg query --profile staging`, which asks for no
// SQL, and `rta vault kv tree --path apps`, which gives an argument as a flag,
// were commands the default build knows nothing about, and the two pages that
// carried them were the ones a reader copies from to reach a database.
func TestEveryCommandTheDocsSpellParsesAsWritten(t *testing.T) {
	forEachDocumentedCommand(t, documentedRegistry(t), func(where string, text string, words []string, root *cobra.Command) {
		cmd, args, err := root.Find(words)
		if err != nil || cmd == root || cmd.HasSubCommands() {
			return
		}
		if slices.Contains(args, "--help") || slices.Contains(args, "-h") {
			return
		}
		if err := cmd.ParseFlags(args); err != nil {
			// What a person typing it would be told, which says the fix where
			// pflag's "unknown flag" only says the symptom.
			t.Errorf("%s: `%s`: %v", where, text, cmd.FlagErrorFunc()(cmd, err))
			return
		}
		if err := cmd.ValidateArgs(cmd.Flags().Args()); err != nil {
			t.Errorf("%s: `%s`: %v", where, text, err)
		}
	})
}

// forEachDocumentedCommand visits every command line of every page that the text
// alone settles (plainCommand), with the root the page's lines are found in.
func forEachDocumentedCommand(t *testing.T, reg *registry.Registry, visit func(where, text string, words []string, root *cobra.Command)) {
	t.Helper()
	repo := repoRoot(t)
	pages, err := filepath.Glob(filepath.Join(repo, "docs", "*", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	top, _ := filepath.Glob(filepath.Join(repo, "docs", "*.md"))
	pages = append(append(pages, top...), filepath.Join(repo, "README.md"))
	for _, page := range pages {
		rel, _ := filepath.Rel(repo, page)
		root := NewRoot(reg, "test")
		for _, line := range shellLines(readDoc(t, repo, rel)) {
			words, ok := plainCommand(line.text)
			if !ok {
				continue
			}
			visit(fmt.Sprintf("%s:%d", rel, line.line), line.text, words, root)
		}
	}
}

// What a command is given with --set is a name the command resolves against a
// declaration, and nothing about the line shows whether it does: `rta dashboard
// add cert.expiry --set host=example.com` parses as well as any line, and is
// refused when run because cert.expiry's input is `targets`, which is the line
// the dashboard page gave a reader for adding a tile. The same is true of what
// `rta profile set` is given — `--set` a key the plugin reads, `--secret` an
// input it takes as a credential, `--only` an input it can fence — and of a
// value outside the options or bounds the input declares.
//
// So the keys a line states are resolved the way the command resolves them, by
// the functions it calls and not by a second reading of the declaration: a tile's
// inputs by parseTileInputs and the checks `dashboard add` makes after it, a
// profile's by parseSetFlags, parseSecretFlags, parseOnlyFlags and
// profile.CheckConnection, which is also what `rta profile list` and `rta
// doctor` report with. What stays unjudged is what depends on the reader's
// state — that a profile named by --profile exists, that a kv entry a --secret
// names has been stored — which is the one thing the text cannot settle.
func TestEveryInputTheDocsStateWithSetIsOneItsPluginTakes(t *testing.T) {
	isolate(t)
	reg := documentedRegistry(t)
	checked := 0
	forEachDocumentedCommand(t, reg, func(where, text string, words []string, _ *cobra.Command) {
		// A root of its own for each line: a flag set on a command stays set
		// on it, and `--set` is an array that would carry the last line's keys
		// into this one.
		cmd, args, err := NewRoot(reg, "test").Find(words)
		if err != nil || slices.Contains(args, "--help") {
			return
		}
		if cmd.ParseFlags(args) != nil {
			return // the parse test reports it
		}
		var verr *view.Error
		switch cmd.CommandPath() {
		case "rta dashboard add":
			if len(cmd.Flags().Args()) != 1 {
				return // the parse test reports it
			}
			verr = tileProblem(reg, cmd)
		case "rta profile set":
			verr = connectionProblem(reg, cmd)
		default:
			return
		}
		checked++
		if verr != nil {
			t.Errorf("%s: `%s` — %s: %s", where, text, verr.Code, verr.Message)
		}
	})
	if checked < 4 {
		t.Fatalf("resolved the inputs of %d documented lines, want the handful there are; has the wording moved?", checked)
	}
}

// tileProblem is what `rta dashboard add <capability> --set k=v ...` refuses
// about the capability and its inputs, as far as the line alone shows it. The
// calls are runDashboardAdd's own, in its order, and the parts of it that read
// the reader's config (a profile that does not exist, a view nobody wrote) are
// the ones left out. cmd has the line's flags parsed, and its one argument.
func tileProblem(reg *registry.Registry, cmd *cobra.Command) *view.Error {
	id := cmd.Flags().Args()[0]
	c, ok := reg.Capability(id)
	if !ok {
		return view.Errorf("core.dashboard.unknown", "%s is no capability", id)
	}
	if c.Safety != plugin.Read {
		return view.Errorf("core.dashboard.notread", "%s is not a read, and a tile re-runs on a timer", id)
	}
	pairs, _ := cmd.Flags().GetStringArray("set")
	with, verr := parseTileInputs(pairs, c)
	if verr != nil {
		return verr
	}
	resolved := plugin.Resolve(c, plugin.Inputs{Caller: with})
	if verr := plugin.CheckInputs(c, plugin.NewRequest(resolved, false, false)); verr != nil {
		return verr
	}
	for k := range with {
		with[k] = resolved[k]
	}
	return tileCanRunUnasked(c, with, cmd.Flags().Changed("profile"))
}

// connectionProblem is what `rta profile set ... --plugin <name> --set k=v ...`
// refuses about the connection it states, as far as the line alone shows it:
// the block it would write is checked by the rules every report on a profile
// uses, in the namespace the line names. A line that states no plugin states no
// connection, and a profile that is only recoloured has nothing to resolve. cmd
// has the line's flags parsed.
func connectionProblem(reg *registry.Registry, cmd *cobra.Command) *view.Error {
	key, _ := cmd.Flags().GetString("plugin")
	if key == "" {
		return nil
	}
	ns, _, _ := config.SplitKey(key)
	var conn config.Connection
	var verr *view.Error
	if pairs, _ := cmd.Flags().GetStringArray("set"); len(pairs) > 0 {
		if conn.Set, verr = parseSetFlags(pairs, ns, reg); verr != nil {
			return verr
		}
	}
	if pairs, _ := cmd.Flags().GetStringArray("only"); len(pairs) > 0 {
		if conn.Only, verr = parseOnlyFlags(pairs, ns, reg); verr != nil {
			return verr
		}
	}
	if pairs, _ := cmd.Flags().GetStringArray("secret"); len(pairs) > 0 {
		if conn.Secrets, verr = parseSecretFlags(pairs); verr != nil {
			return verr
		}
	}
	name := "docs"
	if given := cmd.Flags().Args(); len(given) > 0 {
		name = given[0]
	}
	if problems := profile.CheckConnection(name, key, conn, reg); len(problems) > 0 {
		return view.Errorf("core.profile.unusable", "%s", problems[0].Reason).WithHint(problems[0].Hint)
	}
	return nil
}

// The commands that set rta up and run its boundary have no `rta explain`
// card to say what their flags are for — the docs are where a person learns
// that `rta operator init` takes a `--label`, and that the roster line in the
// MCP chapter carries it. A capability's inputs are the card's business; these
// are the flags of the other commands, and each is named somewhere in the
// docs, so a flag added to one of them has to be written about by the change
// that adds it.
func TestEveryFlagOfTheSetUpAndBoundaryCommandsIsNamedInTheDocs(t *testing.T) {
	root := repoRoot(t)
	var corpus strings.Builder
	for _, page := range markdownPages(t, root) {
		corpus.WriteString(readDoc(t, root, page))
	}
	roots := map[string]bool{
		"agent": true, "grant": true, "lock": true, "mcp": true, "operator": true, "policy": true,
		"config": true, "dashboard": true, "doctor": true, "init": true, "plugin": true, "profile": true, "use": true,
	}
	checked := 0
	for path, cmd := range rtaCommandTree(t) {
		if !roots[strings.Fields(path)[1]] {
			continue
		}
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden || f.Name == "help" || f.Name == "dry-run" {
				return
			}
			checked++
			if !strings.Contains(corpus.String(), "--"+f.Name) {
				t.Errorf("`%s` takes --%s, and no page of the docs names it", path, f.Name)
			}
		})
	}
	if checked < 50 {
		t.Fatalf("checked %d flags, want the seventy or so there are; has the command tree changed shape?", checked)
	}
}

// A grant is for one agent, and `rta grant allow` fills the name in only when
// this machine knows exactly one: on a fresh install it knows none and refuses
// with grant.noagent, and for a remote server (`--server`) it never fills one
// in at all. So an example that leaves `--agent` off is one that fails the
// first time it is copied, which is how the remote-server example and the
// README every new plugin is scaffolded with came to say it for years after
// the other pages learned to. Prose naming the command (`rta grant allow
// kv.get` as the whole-store form) is not an example and is not read.
func TestEveryGrantAnExampleIssuesNamesItsAgent(t *testing.T) {
	root := repoRoot(t)
	issue := regexp.MustCompile("rta grant allow[^`\n]*")
	for _, page := range markdownPages(t, root) {
		for _, line := range shellLines(readDoc(t, root, page)) {
			if strings.HasPrefix(line.text, "rta grant allow") && !strings.Contains(line.text, "--agent") {
				t.Errorf("%s:%d: `%s` names no --agent", page, line.line, line.text)
			}
		}
	}
	for name, body := range scaffoldFiles() {
		for _, span := range issue.FindAllString(body, -1) {
			if !strings.Contains(span, "--agent") {
				t.Errorf("the scaffolded %s tells a plugin's author to run `%s`, which names no --agent", name, span)
			}
		}
	}
}

// A profile is how an agent uses a service without holding its credential: the
// host reads the entry the profile's `secrets:` names when a call runs, and an
// agent granted the profile never receives it — `rta doctor` says as much of a
// store that unlocks where the server starts. A `kv.get` grant on that same entry
// is the one thing that does hand it over, so the developer recipe that issued
// both in one block — the staging database, then `kv.get` on `staging-db-password`
// for five minutes, once — taught the reverse of what the product is for, and a
// reader copied it as the ordinary day's setup. The bound on it (five minutes, one
// use) is what kept it from being worse, and is not what made it right.
//
// A grant a page issues on `kv.get` may name a secret an agent has to hold itself,
// a deploy token, and no entry that a page maps to a credential.
func TestNoGrantTheDocsIssueHandsAnAgentACredentialAProfileMaps(t *testing.T) {
	root := repoRoot(t)
	mapped := regexp.MustCompile(`kv:([A-Za-z0-9](?:[A-Za-z0-9/_.-]*[A-Za-z0-9])?)`)
	issue := regexp.MustCompile(`rta grant allow kv\.get ([A-Za-z0-9](?:[A-Za-z0-9/_.-]*[A-Za-z0-9])?)`)

	entries := map[string]string{}
	for _, page := range markdownPages(t, root) {
		for _, m := range mapped.FindAllStringSubmatch(readDoc(t, root, page), -1) {
			if _, seen := entries[m[1]]; !seen {
				entries[m[1]] = page
			}
		}
	}
	if len(entries) < 4 {
		t.Fatalf("found %d entries a profile maps to a credential, want the handful the docs hold; has the reference moved?", len(entries))
	}

	issued := 0
	for _, page := range markdownPages(t, root) {
		for _, line := range shellLines(readDoc(t, root, page)) {
			m := issue.FindStringSubmatch(line.text)
			if m == nil {
				continue
			}
			issued++
			if mappedIn, ok := entries[m[1]]; ok {
				t.Errorf("%s:%d: `%s` hands an agent %s, which %s maps to a credential: a profile grant already lets the agent use it without holding it",
					page, line.line, line.text, m[1], mappedIn)
			}
		}
	}
	if issued < 3 {
		t.Fatalf("found %d kv.get grants in the docs, want the handful there are; has the wording moved?", issued)
	}
}

// shellLines is every logical line of every shell fence, a backslash
// continuation joined onto the line it continues, and the inline code spans
// outside a fence that run rta in its image, with the line it began on. Not
// the other spans: a span names a command in a sentence — `rta grant allow` —
// and is no more complete than the sentence around it.
func shellLines(body string) []snippet {
	var out []snippet
	inFence, fenceIsCommand := false, false
	var pending *snippet
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			fenceIsCommand = inFence && commandFences[strings.TrimPrefix(trimmed, "```")]
			pending = nil
			continue
		}
		if !inFence {
			for j, span := range strings.Split(line, "`") {
				if j%2 == 1 && ranInImage(span) != span {
					out = append(out, snippet{line: i + 1, text: span})
				}
			}
			continue
		}
		if !fenceIsCommand {
			continue
		}
		if pending != nil {
			pending.text += " " + trimmed
		} else {
			out = append(out, snippet{line: i + 1, text: trimmed})
			pending = &out[len(out)-1]
		}
		if strings.HasSuffix(pending.text, "\\") {
			pending.text = strings.TrimSpace(strings.TrimSuffix(pending.text, "\\"))
		} else {
			pending = nil
		}
	}
	return out
}

// ranInImage turns `docker run … ghcr.io/this-is-tobi/rta:latest <args>` and
// `kubectl run … --image=ghcr.io/this-is-tobi/rta:latest -- <args>` into the
// `rta <args>` they run, and leaves any other line as it was.
func ranInImage(line string) string {
	if !strings.Contains(line, "this-is-tobi/rta") {
		return line
	}
	words := strings.Fields(line)
	if len(words) < 2 || (words[0] != "docker" && words[0] != "kubectl") || words[1] != "run" {
		return line
	}
	for i, w := range words {
		switch {
		case words[0] == "kubectl" && w == "--":
			return "rta " + strings.Join(words[i+1:], " ")
		case words[0] == "docker" && strings.Contains(w, "this-is-tobi/rta"):
			return "rta " + strings.Join(words[i+1:], " ")
		}
	}
	return line
}

// plainCommand splits a line that is one rta command and nothing the text
// cannot settle: no placeholder, ellipsis, substitution or variable, nothing
// after a pipe, a redirect, a list or a comment, and an ordinary word-by-word
// quoting. The words are rta's arguments, `rta` itself dropped.
func plainCommand(line string) ([]string, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "$ "))
	// The image's entrypoint is rta, so what follows the image — or the `--`
	// of a kubectl run — is rta's own arguments.
	line = ranInImage(line)
	if !strings.HasPrefix(line, "rta ") || strings.ContainsAny(line, "<$`…") || strings.Contains(line, "...") {
		return nil, false
	}
	var words []string
	var cur strings.Builder
	var quote rune
	started := false
	flush := func() {
		if started {
			words = append(words, cur.String())
		}
		cur.Reset()
		started = false
	}
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, started = r, true
		case r == ' ' || r == '\t':
			flush()
		case r == '|' || r == ';' || r == '&' || r == '#' || r == '(' || r == ')' || r == '>':
			flush()
			return words[1:], len(words) > 1
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	flush()
	return words[1:], len(words) > 1
}

// pluginNouns are the commands a plugin from another repository brings, which
// the default build does not have: the first-party twelve, and the example
// plugin's.
var pluginNouns = map[string]bool{
	"pg": true, "mysql": true, "mariadb": true, "etcd": true, "qdrant": true, "redis": true,
	"s3": true, "vault": true, "kube": true, "cnpg": true, "docker": true, "keycloak": true,
	"hello": true,
}

// The commands rta tells a person to run are written in Go strings as well as
// in the docs — an error's hint, a card's description, the schema an editor
// shows on hover — and nothing checked those: the config schema described the
// theme block as "the names `rta theme` lists", and there is no such command,
// so the one place an editor's reader looked for the answer named a command
// that answers "unknown". The docs have had this test for a long time; the
// strings the binary prints are the same promise.
//
// A code span that begins with `rta` in a string literal (not a comment, which
// nobody reads at a terminal) is held to the same rule as a docs page: where
// it names a command rta or a plugin has, and the words after it are verbs that exist.
func TestEveryCommandAGoStringTellsAPersonToRunExists(t *testing.T) {
	tree := rtaCommandTree(t)
	root := repoRoot(t)
	span := regexp.MustCompile("`rta((?: [a-z][a-z0-9-]*){1,3})")

	checked := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", ".local", "docs", "proto":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, m := range span.FindAllStringSubmatch(line, -1) {
				checked++
				words := strings.Fields(m[1])
				if _, builtIn := tree["rta "+words[0]]; !builtIn && !pluginNouns[words[0]] {
					t.Errorf("%s:%d: `rta %s` names no command: %q is neither one of rta's own nor a plugin's", rel, i+1, m[1][1:], words[0])
					continue
				}
				if problem := unknownVerb(tree, words); problem != "" {
					t.Errorf("%s:%d: %s", rel, i+1, problem)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the checkout: %v", err)
	}
	if checked < 30 {
		t.Fatalf("checked %d commands in Go strings, want the seventy or so there are; has the quoting moved?", checked)
	}
}

// The same promise for a flag: a hint that says to re-run with `--force` is a
// string nobody type-checks, and a flag that was renamed or never written is
// the sentence a person copies to the terminal and meets "unknown flag" from.
// A flag a code span in a Go string cites has to be a flag of some command in
// the tree. Which command is not judged — a hint names the flags of commands
// that are not even in the default build — only that the word exists as a flag
// somewhere, which is what catches a spelling that exists nowhere.
//
// Flags of other programs sit in the same strings (`git fetch --prune`,
// `kubectl --context`), so a span is read only when it begins with `rta`, or
// is a bare flag.
func TestEveryFlagAGoStringTellsAPersonToTypeExists(t *testing.T) {
	flags := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.Flags().VisitAll(func(f *pflag.Flag) { flags[f.Name] = true })
		c.InheritedFlags().VisitAll(func(f *pflag.Flag) { flags[f.Name] = true })
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	walk(NewRoot(reg, "test"))
	// Cobra adds --help when a command runs, not when the tree is built, and
	// the plugins from other repositories bring flags of their own which a
	// string here may name when it explains one. --secrets-from is the host's,
	// but it is added per capability that has an endpoint a forward can fill
	// (addAdHocFlags), and no capability in the default build has one.
	for _, f := range []string{"help", "bucket", "cluster", "namespace", "database", "collection", "secrets-from"} {
		flags[f] = true
	}

	root := repoRoot(t)
	span := regexp.MustCompile("`(rta [^`]*|--[a-z][a-z0-9-]*[^`]*)`")
	flag := regexp.MustCompile(`(?:^|[\s=(])--([a-z][a-z0-9-]*)`)
	checked := 0
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", ".local", "docs", "proto":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, s := range span.FindAllStringSubmatch(line, -1) {
				if strings.Contains(s[1], "{{") {
					// A template for a plugin the reader has not written yet.
					continue
				}
				for _, m := range flag.FindAllStringSubmatch(s[1], -1) {
					checked++
					if !flags[m[1]] {
						t.Errorf("%s:%d: %q cites --%s, which is no flag of any command", rel, i+1, s[1], m[1])
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the checkout: %v", err)
	}
	if checked < 30 {
		t.Fatalf("checked %d flags in Go strings, want the hundred or so there are; has the quoting moved?", checked)
	}
}
