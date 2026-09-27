package app

import (
	"context"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/mcp"
	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// speller finds, in text a surface other than the CLI reads, what only a
// terminal could act on: a flag, or an `rta …` command line. Every other
// surface names a capability and an input its own way
// (plugin.Surface.CapabilityName and InputName), and text shown on every
// surface at once names an input as `key` and a capability by its ID.
type speller struct{ reg *registry.Registry }

// commandLine is "rta" followed by words, wherever it stands — in a code
// span, inside a shell example, or in running prose after "add one with:".
var commandLine = regexp.MustCompile(`(?:^|[^a-z-])rta((?: [a-z][a-z-]*)+)`)

// find returns each place text spells something for a terminal.
//
// The one command line an agent may read is the one it hands on:
// plugin.AskOperator's phrase, a command for the person at the terminal,
// taken out before anything is looked at.
//
// A code span holding some other program's command line — `git status
// --porcelain`, `cargo tree --invert` — is that program's spelling, and says
// where an answer came from. A flag in one is held only when the span opens
// on it, or when the words before it name a capability that declares it:
// `kv list --match aws` is rta's command line without its first word.
//
// terminalOnly is for text only a person at a terminal reads, a HumanOnly
// capability's: there a command with no capability behind it — `rta mcp
// serve --as`, `rta doctor` — has no other spelling to use, and is let
// through. One naming a capability still is not, since the TUI reads the
// same text and names the capability its own way.
func (sp speller) find(text string, terminalOnly bool) []string {
	// The phrase up to the command, read off the helper so the two cannot
	// drift: "ask the operator to run `rta ".
	ask := strings.TrimSuffix(plugin.AskOperator(""), "`")
	for {
		i := strings.Index(text, ask)
		if i < 0 {
			break
		}
		end := strings.IndexByte(text[i+len(ask):], '`')
		if end < 0 {
			break
		}
		text = text[:i] + text[i+len(ask)+end+1:]
	}
	var found []string
	quote := func(i, j int) string {
		from, to := max(0, i-30), min(len(text), j+30)
		return strings.ReplaceAll(text[from:to], "\n", " ")
	}
	for _, m := range commandLine.FindAllStringSubmatchIndex(text, -1) {
		if _, ok := sp.capabilityOf(text[m[2]:m[3]]); ok {
			found = append(found, quote(m[0], m[1]))
		}
	}
	for i := 0; i < len(text); i++ {
		if text[i] == '`' {
			end := strings.IndexByte(text[i+1:], '`')
			if end < 0 {
				break
			}
			span := text[i+1 : i+1+end]
			if rest, ok := strings.CutPrefix(span, "rta "); ok {
				// One naming a capability was found above.
				if _, named := sp.capabilityOf(rest); !named && !terminalOnly {
					found = append(found, quote(i, i+2+end))
				}
			} else {
				c, named := sp.capabilityOf(span)
				for _, flag := range flagsIn(span) {
					if strings.HasPrefix(span, "--") || named && declares(c, flag) {
						found = append(found, quote(i, i+2+end))
					}
				}
			}
			i += end + 1
			continue
		}
		if flag := flagAt(text, i); flag != "" {
			found = append(found, quote(i, i+2+len(flag)))
			i += 1 + len(flag)
		}
	}
	for _, m := range sp.bareCommands(maskSpans(text)) {
		found = append(found, quote(m[0], m[1]))
	}
	return found
}

// proseWord is one word a capability's ID could be made of.
var proseWord = regexp.MustCompile(`[a-z][a-z0-9-]*`)

// bareCommands returns where prose spells a capability in the CLI's words
// without the "rta" before them: "use fs hash to inspect one file", a hint an
// agent read as a command it had no terminal for, with the fs_hash tool in
// its list. commandLine catches the words after "rta" and a code span's flag
// is caught in find, but two plain words in a sentence were neither, and
// that hint went out.
//
// Words a capability's ID is made of, one space apart, standing alone:
// fs.hash and fs_hash are the ID and the tool, each spelled as one word, and
// a path or a file name that happens to hold the words — fs/hash, fs hash.go
// — names something else. And not after a determiner: the words an ID is
// made of are English words too, and after "a" or "the" they are the noun
// they say — "a key set one with a keys list" is a JWK's keys member, "the
// operator's git config" git's own file — where after "use" or "run" they
// are a command.
func (sp speller) bareCommands(prose string) [][2]int {
	words := proseWord.FindAllStringIndex(prose, -1)
	var out [][2]int
	for i := 0; i < len(words); i++ {
		start := words[i][0]
		if start > 0 && joined(prose[start-1]) || strings.HasSuffix(prose[:start], "rta ") ||
			afterDeterminer(prose[:start]) {
			// Inside a longer word, a command line commandLine found, or a
			// noun.
			continue
		}
		id, last := prose[start:words[i][1]], -1
		for j := i + 1; j < len(words) && j < i+3; j++ {
			if prose[words[j-1][1]:words[j][0]] != " " {
				break
			}
			id += "." + prose[words[j][0]:words[j][1]]
			if _, ok := sp.reg.Capability(id); ok && standsAlone(prose, words[j][1]) {
				last = j
			}
		}
		if last >= 0 {
			out = append(out, [2]int{start, words[last][1]})
			i = last
		}
	}
	return out
}

// determiners are the words that make the ones after them a noun.
var determiners = map[string]bool{
	"a": true, "an": true, "the": true, "this": true, "that": true, "these": true, "those": true,
	"its": true, "your": true, "their": true, "our": true, "my": true,
	"each": true, "every": true, "any": true, "no": true,
}

// afterDeterminer reports whether before ends in a determiner and a space, a
// possessive — the operator's — among them.
func afterDeterminer(before string) bool {
	before, spaced := strings.CutSuffix(before, " ")
	if !spaced {
		return false
	}
	word := strings.ToLower(before[strings.LastIndexAny(before, " \t\n(\"")+1:])
	return determiners[word] || strings.HasSuffix(word, "'s")
}

// joined reports whether b, beside a word, makes it part of a longer one: an
// identifier, a path, a dotted ID or a flag.
func joined(b byte) bool {
	return isFlagByte(b) || b == '_' || b == '.' || b == '/'
}

// standsAlone reports whether the word ending at prose[end] ends there, and
// is not the start of a file name or a path: the full stop ending a sentence
// does not join it to anything, the one in hash.go does.
func standsAlone(prose string, end int) bool {
	if end == len(prose) {
		return true
	}
	if b := prose[end]; b == '.' {
		return end+1 == len(prose) || !isFlagByte(prose[end+1])
	}
	return !joined(prose[end])
}

// maskSpans is text with each code span blanked out, the same length so an
// offset into it is one into text. A span is the spelling of whatever it
// quotes, another program's command line among them — `git log` is git's —
// and find reads it on its own terms.
func maskSpans(text string) string {
	b := []byte(text)
	for i := 0; i < len(b); i++ {
		if b[i] != '`' {
			continue
		}
		end := strings.IndexByte(text[i+1:], '`')
		if end < 0 {
			break
		}
		for k := i; k <= i+1+end; k++ {
			b[k] = ' '
		}
		i += end + 1
	}
	return string(b)
}

// capabilityOf returns the capability whose ID words begins with, as the CLI
// spells one: `kv list` for kv.list.
func (sp speller) capabilityOf(words string) (plugin.Capability, bool) {
	var ids []string
	for _, w := range strings.Fields(words) {
		if strings.Trim(w, "abcdefghijklmnopqrstuvwxyz-") != "" {
			break
		}
		ids = append(ids, w)
		if c, ok := sp.reg.Capability(strings.Join(ids, ".")); ok {
			return c, true
		}
	}
	return plugin.Capability{}, false
}

// declares reports whether flag is one of c's inputs, or the host's own
// detail.
func declares(c plugin.Capability, flag string) bool {
	return flag == "detail" || slices.ContainsFunc(c.Inputs, func(f plugin.Field) bool { return f.Name == flag })
}

// flagAt returns the name of the flag starting at text[i], or "".
func flagAt(text string, i int) string {
	if !strings.HasPrefix(text[i:], "--") || i > 0 && isFlagByte(text[i-1]) ||
		i+2 >= len(text) || text[i+2] < 'a' || text[i+2] > 'z' {
		return ""
	}
	j := i + 2
	for j < len(text) && isFlagByte(text[j]) {
		j++
	}
	return text[i+2 : j]
}

func flagsIn(span string) []string {
	var out []string
	for i := 0; i < len(span); i++ {
		if flag := flagAt(span, i); flag != "" {
			out = append(out, flag)
			i += 1 + len(flag)
		}
	}
	return out
}

func isFlagByte(b byte) bool {
	return b == '-' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b >= 'A' && b <= 'Z'
}

// The speller itself, held to the spellings it exists to catch and the ones
// it must let through: a test that passes because its scanner sees nothing
// guards nothing.
func TestTheSpellerTellsATerminalsSpellingFromEveryoneElses(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	sp := speller{reg}
	for text, want := range map[string]bool{
		"run `rta note list --all` to see every note":                     true,
		"use --timeout to extend the deadline":                            true,
		"add one with: rta note add \"...\"":                              true,
		"`rta doctor` reports it":                                         true,
		"`kv list --match aws` finds it":                                  true,
		"With `--all`, the remote branches follow":                        true,
		"ask the operator to run `rta grant allow kv.get --ttl 15m`":      false,
		"the structured equivalent of `git status --porcelain`":           false,
		"the `note_list` tool with the \"all\" argument lists every note": false,
		"the environment rta mcp serve runs in":                           false,
		"`key` takes a private key file":                                  false,
		"-----BEGIN PUBLIC KEY-----":                                      false,
		"use fs hash to inspect one file":                                 true,
		"compare it with fs hash.":                                        true,
		"net hosts add writes the line":                                   true,
		"there is no \"fs hash\" to run":                                  true,
		"the fs_hash tool checks one file":                                false,
		"fs.hash checks one file":                                         false,
		"the structured equivalent of `git log`":                          false,
		"a key set one with a keys list":                                  false,
		"Path from the operator's git config":                             false,
		"the files under fs/hash and reads fs hash.go":                    false,
	} {
		if got := len(sp.find(text, false)) > 0; got != want {
			t.Errorf("find(%q) found a terminal's spelling: %v, want %v", text, got, want)
		}
	}
	// A command line is one finding, not its own and its words' without "rta".
	if hits := sp.find("run rta fs hash ./x", false); len(hits) != 1 {
		t.Errorf("a command line was found %d times: %q", len(hits), hits)
	}
	// Text only a person at a terminal reads may name a command with no
	// capability behind it, and still not one with.
	if hits := sp.find("the name `rta mcp serve --as` uses", true); len(hits) > 0 {
		t.Errorf("a command with no capability behind it was held against HumanOnly text: %v", hits)
	}
	if hits := sp.find("Allow one with `rta grant allow <capability>`", true); len(hits) == 0 {
		t.Error("a capability's command line passed in HumanOnly text")
	}
}

// Everything a client is told about a tool — its description, rta's frame
// around it, every string in its input schema — is read by an agent, which
// has arguments and no flags, and tools and no terminal. Both transports'
// tool lists, since the remote one is a different set.
func TestAToolListSpellsNothingForATerminal(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	sp := speller{reg}
	for _, opts := range []mcp.Options{{}, {Remote: true}} {
		for _, tl := range surface(t, opts) {
			texts := []string{tl.Description}
			collectStrings(tl.Schema, &texts)
			for _, text := range texts {
				for _, hit := range sp.find(text, false) {
					t.Errorf("tool %s (remote %v) spells a terminal's: …%s…", tl.Name, opts.Remote, hit)
				}
			}
		}
	}
}

func collectStrings(v any, out *[]string) {
	switch v := v.(type) {
	case string:
		*out = append(*out, v)
	case []any:
		for _, e := range v {
			collectStrings(e, out)
		}
	case map[string]any:
		for _, e := range v {
			collectStrings(e, out)
		}
	}
}

// The refusals an agent is actually handed, from a representative set of
// tools called over MCP the way a client calls them: the host's own — a
// missing argument, one outside its options or range, a path outside the
// roots, a grant nobody issued — and the ones handlers word. Every one names
// a tool and an argument, never a flag or an `rta` command line, except in
// the one form that hands a command on to the operator.
func TestARefusalOverMCPSpellsNothingForATerminal(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	root := t.TempDir()
	guard, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, ct := sdk.NewInMemoryTransports()
	if _, err := mcp.NewServer(reg, "spelling", mcp.Options{Paths: guard}).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "spelling", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	const pemKey = "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAGb9ECWmEzf6FQbrBZ9w7lshQhqowtrbLDFw4rXAxZuE=\n-----END PUBLIC KEY-----\n"
	hs256 := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhIn0.c2lnbmF0dXJl"
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"codec_jwt", map[string]any{}}, // a required argument left out
		{"codec_jwt", map[string]any{"token": hs256, "key": `{"kty":"oct","k":"c2VjcmV0"}`}}, // a shared secret as a key
		{"codec_jwk", map[string]any{"key": pemKey}},                                         // PEM where a JWK goes
		{"debug_ansi", map[string]any{"input": ""}},                                          // a piped input, empty
		{"gen_token", map[string]any{"encoding": "b64"}},                                     // outside its options
		{"gen_password", map[string]any{"length": 0}},                                        // outside its range
		{"sys_ps", map[string]any{"sort": "name"}},
		{"note_show", map[string]any{"id": 999}},
		{"http_status", map[string]any{"code": "499"}},
		{"eol_check", map[string]any{"product": "demo@2", "cycle": "3"}},
		{"fs_tree", map[string]any{"path": "/"}},                         // outside the roots
		{"audit_deps", map[string]any{"path": root}},                     // nothing declared
		{"audit_why", map[string]any{"package": "lodash", "path": root}}, // nothing declared
		{"kv_get", map[string]any{"key": "db-password"}},                 // a grant nobody issued
		{"net_port", map[string]any{"host": "localhost", "ports": "22"}}, // a grant nobody issued
		{"time_at", map[string]any{"when": "not a time"}},
	}
	sp := speller{reg}
	for _, c := range calls {
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: c.tool, Arguments: c.args})
		if err != nil {
			t.Fatalf("%s %v: %v", c.tool, c.args, err)
		}
		if !res.IsError {
			t.Errorf("%s %v was not refused, so it tests nothing", c.tool, c.args)
			continue
		}
		for _, content := range res.Content {
			text, ok := content.(*sdk.TextContent)
			if !ok {
				continue
			}
			for _, hit := range sp.find(text.Text, false) {
				t.Errorf("%s %v refuses in a terminal's words: …%s…", c.tool, c.args, hit)
			}
		}
	}
}

// What a capability declares about itself is shown on every surface at once
// — `rta explain` and --help, the TUI's form, an agent's tool list — and has
// no surface to ask which one is reading, so it names an input as `key` and a
// capability by its ID, and never as one surface spells it.
func TestDeclaredTextSpellsNothingForOneSurface(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	sp := speller{reg}
	for _, c := range reg.Capabilities() {
		texts := map[string]string{"summary": c.Summary, "description": c.Description}
		for _, f := range c.Inputs {
			texts["help of "+f.Name] = f.Help
		}
		for what, text := range texts {
			for _, hit := range sp.find(text, c.HumanOnly) {
				t.Errorf("%s %s spells a terminal's: …%s…", c.ID, what, hit)
			}
		}
	}
}
