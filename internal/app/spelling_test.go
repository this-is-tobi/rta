package app

import (
	"regexp"
	"slices"
	"strings"
	"testing"

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
	const ask = "ask the operator to run `rta "
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
	return found
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
