// Package spelling finds, in text a surface other than the CLI reads, what
// only a terminal could act on: a flag, or an `rta …` command line.
//
// An agent has tools and their arguments, and the TUI a capability's ID and
// a form's boxes, and a hint naming `rta demo key list --limit` sends either
// one looking for something that is not there. Text shown on every surface
// at once — what a capability declares — names an input as `limit` and a
// capability by its ID, and text worded at run time asks the request's
// surface (plugin.Surface.CapabilityName, Call, InputName and the rest).
//
// One speller for everybody who holds text to that: rta's own tests, over
// the host's registry, read every tool list and refusal an agent is handed;
// sdktest.Check reads what a plugin declares; and a plugin's own tests can
// read the sentences its source spells out. Each plugin carried a copy of
// rta's speller before this was exported, because rta's lived in its
// internal tests and read a registry a plugin cannot load — and the copies
// had already fixed a rule rta's still missed, and grown a scan of their own.
//
// What it cannot see is a command line in prose naming a capability the
// speller was not given. Built over one plugin, it cannot tell "run rta net
// dns" from "rta does not run it": only the host's registry says which words
// after "rta" are a namespace. In a code span it can, since a span opening on
// "rta " is a command line whatever follows: rta's own commands are the
// names the host reserves for them, and any other word with more after it is
// a namespace some capability may sit in.
package spelling

import (
	"regexp"
	"slices"
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// Speller finds a terminal's spelling in text, knowing which capabilities
// there are for a command line to name.
type Speller struct {
	capability func(id string) (plugin.Capability, bool)
	namespaces []string
}

// New returns a speller over the capabilities lookup finds by ID.
//
// namespaces are the ones whose command lines are rta's even where the
// words after the namespace name no capability: `rta demo restore` is the
// CLI's spelling of something the reader would go looking for, whether or
// not a demo.restore exists. A plugin gives its own; rta, every plugin's.
func New(lookup func(id string) (plugin.Capability, bool), namespaces ...string) Speller {
	return Speller{capability: lookup, namespaces: slices.Clone(namespaces)}
}

// ForPlugin is New over what p declares and p's own namespace: the speller a
// plugin's tests hold its text to, with no registry to ask about anybody
// else's capabilities.
func ForPlugin(p plugin.Plugin) Speller {
	caps := make(map[string]plugin.Capability, len(p.Capabilities))
	for _, c := range p.Capabilities {
		caps[c.ID] = c
	}
	return New(func(id string) (plugin.Capability, bool) {
		c, ok := caps[id]
		return c, ok
	}, p.Name)
}

// HostSwitches are the flags rta gives a capability's command whatever the
// capability declares: the ones every command inherits, and --detail and
// --profile, which it adds to a capability of the kind that takes them. A
// flag in a code span after a capability's words is rta's command line when
// the capability declares it or when it is one of these — `kv list
// --dry-run` is as much the CLI's spelling as `kv list --match`.
//
// Read off the names the host reserves (plugin.ReservedInputs) rather than
// listed again here, with --profile beside them, reserved only where the
// host adds it; rta's own tests hold the result to the flags its command
// tree really adds, so the list cannot drift from the CLI without one of
// them failing.
func HostSwitches() []string { return slices.Clone(hostSwitches) }

var hostSwitches = func() []string {
	out := plugin.ReservedInputs()
	out = append(out, "profile")
	slices.Sort(out)
	return out
}()

// Operand stands in, in a sentence read out of source, for whatever a
// literal is joined to — a name, a value, what a naming helper returned. A
// flag joined to one is a flag all the same, and Find reads "--" followed by
// it as one: "raise --" + name + " to see more" reads `--limit` to whoever
// gets the message.
const Operand = "…"

// commandLine is "rta" followed by words, wherever it stands — in a code
// span, inside a shell example, or in running prose after "add one with:".
// A word may hold a digit after its first letter, as codec.b64's and a
// namespace such as s3 do: read as letters alone, `rta codec b64` was `rta
// codec b` and named nothing.
var commandLine = regexp.MustCompile(`(?:^|[^a-z-])rta((?: [a-z][a-z0-9-]*)+)`)

// Find returns each place text spells something for a terminal, each quoted
// with the text around it, for a message.
//
// The one command line an agent may read is the one it hands on:
// plugin.AskOperator's phrase, a command for the person at the terminal,
// taken out before anything is looked at.
//
// A flag in prose is held whichever program it belongs to: an agent reading
// "run with --jobs 4" cannot tell pg_dump's option from rta's, and has
// neither. A short one is held when it is one of the host's, -o, -y or -h,
// and read as the switch it is short for (hostShorthands says why only
// those). A code span holding some other program's command line — `git
// status --porcelain`, `pg_restore --jobs` — is that program's spelling, and
// says where an answer came from. A flag in one is held only when the span
// opens on it, or when the words before it name a capability that takes it,
// declared or one of the host's switches: `kv list --match aws` is rta's
// command line without its first word.
//
// terminalOnly is for text only a person at a terminal reads — a HumanOnly
// capability's, or a sentence a handler words in the branch that asked its
// surface for the CLI: there a command with no capability behind it — `rta
// mcp serve --as`, `rta doctor` — has no other spelling to use, and is let
// through (ownCommand). One naming a capability still is not, since the TUI
// reads the same text and names the capability its own way.
func (sp Speller) Find(text string, terminalOnly bool) []string {
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
		if sp.names(text[m[2]:m[3]]) {
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
				if !sp.names(rest) && (!terminalOnly || !ownCommand(rest)) {
					found = append(found, quote(i, i+2+end))
				}
			} else {
				c, named := sp.capabilityOf(span)
				_, lead := flagAt(span, 0)
				if slices.ContainsFunc(flagsIn(span), func(flag string) bool {
					return strings.HasPrefix(span, "--") || lead > 0 || named && declares(c, flag)
				}) {
					found = append(found, quote(i, i+2+end))
				}
			}
			i += end + 1
			continue
		}
		if flag, width := flagAt(text, i); flag != "" {
			found = append(found, quote(i, i+width))
			i += width - 1
		}
	}
	for _, m := range sp.bareCommands(maskSpans(text)) {
		found = append(found, quote(m[0], m[1]))
	}
	return found
}

// names reports whether the words after "rta" are a command line the
// speller knows: a capability's, or one of its namespaces and a word after
// it.
func (sp Speller) names(words string) bool {
	if _, ok := sp.capabilityOf(words); ok {
		return true
	}
	fields := strings.Fields(words)
	return len(fields) > 1 && slices.Contains(sp.namespaces, fields[0])
}

// ownCommand reports whether the words after "rta" in a code span are a
// command no capability can be behind, which text only a terminal reads may
// spell: one of rta's own commands, a name the host reserves for them
// (plugin.ReservedNamespaces), or a word on its own, the namespace a harness
// deny list names as `rta lock`.
//
// Any other word with more after it is a namespace and a command in it, a
// capability's command line to the TUI as much as to an agent, whether or
// not this speller was given the capability. Let through as "no capability
// here", a plugin's HumanOnly text spelling `rta net dns` or `rta grant
// allow` passed its own tests, since a speller over one plugin knows none of
// the built-ins, where rta's tests over the registry held the same text.
func ownCommand(words string) bool {
	fields := strings.Fields(words)
	return len(fields) < 2 || slices.Contains(ownCommands, fields[0])
}

var ownCommands = plugin.ReservedNamespaces()

// proseWord is one word a capability's ID could be made of.
var proseWord = regexp.MustCompile(`[a-z][a-z0-9-]*`)

// bareCommands returns where prose spells a capability in the CLI's words
// without the "rta" before them: "use fs hash to inspect one file", a hint an
// agent read as a command it had no terminal for, with the fs_hash tool in
// its list. commandLine catches the words after "rta" and a code span's flag
// is caught in Find, but two plain words in a sentence were neither, and
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
func (sp Speller) bareCommands(prose string) [][2]int {
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
			if _, ok := sp.capability(id); ok && standsAlone(prose, words[j][1]) {
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
// and Find reads it on its own terms.
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
func (sp Speller) capabilityOf(words string) (plugin.Capability, bool) {
	var ids []string
	for _, w := range strings.Fields(words) {
		if strings.Trim(w, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
			break
		}
		ids = append(ids, w)
		if c, ok := sp.capability(strings.Join(ids, ".")); ok {
			return c, true
		}
	}
	return plugin.Capability{}, false
}

// declares reports whether flag is one of c's inputs, or one of the host's
// switches.
func declares(c plugin.Capability, flag string) bool {
	return slices.Contains(hostSwitches, flag) ||
		slices.ContainsFunc(c.Inputs, func(f plugin.Field) bool { return f.Name == flag })
}

// hostShorthands are the letters the host's switches are also spelled by,
// each with the switch it is short for: `-o csv` is --output csv, to the CLI
// and to nobody else. No capability declares a short flag — an input is
// spelled long or not at all — so these are every short flag a capability's
// command takes, and rta's own tests hold them to its command tree.
//
// **The host's letters only, where a long flag in prose is held whichever
// program it belongs to.** A long flag in a sentence is an instruction far
// more often than not, and an agent reading "run with --jobs 4" cannot tell
// pg_dump's option from rta's. A dash and a letter is as often another
// program's option named as a thing — docker's `-e`, in a sentence about
// where a container's credentials come from — and held by any letter, such
// text would fail for naming what it describes. The host's letters are the
// ones a sentence can mean for rta's command line: read over every
// built-in's declared text and the official plugins', they stood only in
// other programs' command lines in code spans, which a span already reads
// as theirs, and in the one sentence that meant rta's -o.
var hostShorthands = map[byte]string{'o': "output", 'y': "yes", 'h': "help"}

// flagAt returns the flag starting at text[i], by its name, and how many
// bytes of text spell it; or "" and 0.
//
// A flag source splices together is one all the same, and is named by what
// stands in for its name: "--" joined to Operand, and "--%s" handed to
// Sprintf. Either reads `--limit` to whoever gets the message, and read as a
// letter after "--" or nothing, both went through.
//
// One of the host's short switches is named by the switch it is short for
// (hostShorthands), so that a span reads `kv list -o json` as it reads `kv
// list --output json`.
func flagAt(text string, i int) (string, int) {
	if i+1 >= len(text) || text[i] != '-' || i > 0 && isFlagByte(text[i-1]) {
		return "", 0
	}
	if text[i+1] != '-' {
		long, ok := hostShorthands[text[i+1]]
		if !ok || i+2 < len(text) && isFlagByte(text[i+2]) {
			return "", 0
		}
		return long, 2
	}
	if i+2 >= len(text) {
		return "", 0
	}
	switch rest := text[i+2:]; {
	case strings.HasPrefix(rest, Operand):
		return Operand, 2 + len(Operand)
	case rest[0] == '%' && len(rest) > 1:
		return rest[:2], 4
	case rest[0] < 'a' || rest[0] > 'z':
		return "", 0
	}
	j := i + 2
	for j < len(text) && isFlagByte(text[j]) {
		j++
	}
	return text[i+2 : j], j - i
}

func flagsIn(span string) []string {
	var out []string
	for i := 0; i < len(span); i++ {
		if flag, width := flagAt(span, i); flag != "" {
			out = append(out, flag)
			i += width - 1
		}
	}
	return out
}

func isFlagByte(b byte) bool {
	return b == '-' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b >= 'A' && b <= 'Z'
}

// Text is one piece of what a plugin declares about itself, with whose it is
// and where it is said.
type Text struct {
	// ID is the capability that declares it, or the plugin's name for the
	// plugin's own summary: the name sdktest.Skip waives a text by.
	ID string
	// Where is which of its texts this one is: "summary", "description",
	// "help of limit", "action x", "toggle A".
	Where string
	Text  string
	// TerminalOnly is set for a HumanOnly capability's text, which only a
	// person at a terminal reads (Find).
	TerminalOnly bool
}

// Declared is everything p says about itself that is shown on every surface
// at once — `rta explain` and --help, the TUI's form and its footer, an
// agent's tool list — in the order p declares it: the plugin's summary, and
// each capability's summary, description, inputs' help, and the labels of
// its actions and toggles. None of it has a surface to ask which one is
// reading, so it names an input as `key` and a capability by its ID.
func Declared(p plugin.Plugin) []Text {
	out := []Text{{ID: p.Name, Where: "summary", Text: p.Summary}}
	for _, c := range p.Capabilities {
		add := func(where, text string) {
			out = append(out, Text{ID: c.ID, Where: where, Text: text, TerminalOnly: c.HumanOnly})
		}
		add("summary", c.Summary)
		add("description", c.Description)
		for _, f := range c.Inputs {
			add("help of "+f.Name, f.Help)
		}
		for _, a := range c.Actions {
			add("action "+a.Key, a.Label)
		}
		for _, tg := range c.Toggles {
			add("toggle "+tg.Key, tg.Label)
		}
	}
	return out
}
