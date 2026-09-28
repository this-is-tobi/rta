package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/shellquote"
	"github.com/this-is-tobi/rta/internal/textclean/glyph"
)

// Naming a capability or an input in a message, the way the caller reads it.
//
// A message is read on one surface, and each surface spells the same thing
// differently: `rta codec jwk` at a terminal is the codec_jwk tool to an
// agent and codec.jwk in the TUI's catalogue, and the key a person types as
// --key is an agent's "key" argument and a box in a form. A hint that names
// the wrong one sends its reader looking for something that is not there:
// an agent told "--key takes only public keys" has a schema with a key
// argument and no flags at all, and one told that "`rta codec jwk` says what
// is wrong" can run no command. Tool descriptions and hints said both, to
// agents, dozens of times, each worded where it was written — and the few
// that got it right did so with a switch of their own, three spellings of
// one rule in one plugin.
//
// So the spelling lives here, once, and a handler asks for it with the
// surface its request came through: req.Surface().InputName("key"). What a
// capability declares — Summary, Description, an input's Help — is shown on
// every surface at once and has no surface to ask, so it names an input as
// `key` and a capability by its ID, and only text worded at run time goes
// through these.
//
// Known limit: the surface is the one stamped on the request where the
// handler runs, and a call given a server does not run there. It crosses
// the operator channel, whose envelope carries a verb and its arguments and
// never the caller's surface, and what comes back is worded by code on the
// server that has no request to ask, for a command line — a TUI form given a
// server reads a remote refusal in flags rather than in its own boxes.
// Carrying the surface across would put a claim about the caller into a
// signed envelope that has, so far, needed none.

// ToolName is the name capability id is published under as an MCP tool. Dots
// are not accepted in a tool name by every client, so each becomes an
// underscore. The one rule: the bridge registers tools by it, and a message
// naming a tool to an agent spells it with it, so the two cannot disagree.
func ToolName(id string) string { return strings.ReplaceAll(id, ".", "_") }

// CapabilityName names capability id the way a caller on s would reach it:
// `rta codec jwk` on the CLI, the `codec_jwk` tool over MCP, and `codec.jwk`
// in the TUI, whose catalogue lists capabilities by ID and filters on it.
// SurfaceUnknown, a caller inside the process, and a completion keystroke
// read the CLI's spelling.
//
// It names a capability somebody on s can call. One marked HumanOnly is no
// tool over MCP, and one marked HostSpecific is none over a remote
// transport: a hint sending an agent to either sends it after something its
// tool list does not have, and the person at the terminal is the one to ask
// — AskOperator.
func (s Surface) CapabilityName(id string) string {
	switch s {
	case SurfaceMCP:
		return "the `" + ToolName(id) + "` tool"
	case SurfaceTUI:
		return "`" + id + "`"
	}
	return "`" + commandLine(id) + "`"
}

// commandLine is capability id as the CLI types it: rta and the ID's words.
func commandLine(id string) string { return "rta " + strings.ReplaceAll(id, ".", " ") }

// CapabilityWith names capability id called with inputs, the way a caller on
// s would give them: `rta note list --all` on the CLI, the `note_list` tool
// with the "all" argument over MCP, `note.list` with the all box in the TUI.
// For a hint that sends its reader to a call rather than to a capability —
// "`rta note list --all` lists every note" — so the two halves of it cannot
// be spelled for two different surfaces.
//
// The inputs are named, never valued: a value belongs to the sentence
// around it ("with %s set to 5"), where it reads the same on every surface.
// And they are flags on the CLI: a Positional input has a place on the
// command line rather than a name, and a sentence names it with ArgumentName.
func (s Surface) CapabilityWith(id string, inputs ...string) string {
	if len(inputs) == 0 {
		return s.CapabilityName(id)
	}
	names := make([]string, len(inputs))
	for i, n := range inputs {
		names[i] = s.InputName(n)
	}
	if s.spellsForCLI() {
		return "`" + commandLine(id) + " " + strings.Join(names, " ") + "`"
	}
	return s.CapabilityName(id) + " with " + strings.Join(names, " and ")
}

// Arg is one input a call spelled by Surface.Call gives: its name, its value,
// and whether the CLI takes it by its place on the command line rather than
// as a flag. A Value of true is a switch turned on, spelled on the CLI as the
// bare flag, and false one turned off, joined to its flag: --tls=false.
type Arg struct {
	Name       string
	Value      any
	Positional bool
}

// Call spells capability id called with args, whole and with its values, the
// way a caller on s makes that call: `rta kv get db-password` on the CLI,
// `kv_get {"key":"db-password"}` over MCP — the tool and the arguments to
// give it — and `kv.get key=db-password` in the TUI, whose form is filled in
// rather than typed. For a page that hands its reader the next call to make,
// "reveal: rta kv get db-password", which an agent read as a command line it
// has no terminal for.
//
// Unquoted, unlike CapabilityName: a cell somebody copies wants the call
// alone, and a sentence puts it in backticks itself.
func (s Surface) Call(id string, args ...Arg) string {
	switch s {
	case SurfaceMCP:
		values := make(map[string]any, len(args))
		for _, a := range args {
			values[a.Name] = a.Value
		}
		// A map of plain values always encodes, and its keys come out
		// sorted, so one call is spelled the same way every time. Without
		// the HTML escaping json.Marshal does: nothing here reaches a page,
		// and a placeholder's angle brackets, each turned into a six-letter
		// escape, handed an agent a value nobody wrote.
		var encoded bytes.Buffer
		enc := json.NewEncoder(&encoded)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(values)
		return ToolName(id) + " " + strings.TrimSuffix(encoded.String(), "\n")
	case SurfaceTUI:
		parts := []string{id}
		for _, a := range args {
			if a.Value == true {
				parts = append(parts, a.Name)
				continue
			}
			parts = append(parts, a.Name+"="+boxValue(a.Value))
		}
		return strings.Join(parts, " ")
	}
	parts := []string{commandLine(id)}
	for _, a := range args {
		switch {
		case a.Positional:
			parts = append(parts, cliValue(a.Value))
		case a.Value == true:
			parts = append(parts, s.InputName(a.Name))
		case a.Value == false:
			// Joined, never a word of its own: pflag gives a switch its value
			// only after an equals sign, and reads the word after it as the
			// command's own argument, so --tls false turned the switch on and
			// handed the command a stray "false" — the opposite call.
			parts = append(parts, s.InputName(a.Name)+"=false")
		default:
			parts = append(parts, s.InputName(a.Name), cliValue(a.Value))
		}
	}
	return strings.Join(parts, " ")
}

// cliValue is v as a command line carries it: one shell word that reads back
// as v, byte for byte (shellquote.Arg) — except a placeholder, <file> or
// <key>, which stands for what the reader types in its place, and which a
// usage line spells bare.
//
// A shell's quoting and not Go's: a value is whatever somebody stored, an
// agent's kv key among them, and inside the double quotes strconv.Quote adds
// a $(…) or a backquoted command still runs on paste. An empty value is a
// pair of quotes rather than nothing, since it is a word the call gives.
func cliValue(v any) string {
	text := fmt.Sprint(v)
	switch {
	case placeholder(text):
		return text
	case text == "":
		return "''"
	}
	return shellquote.Arg(text)
}

// boxValue is v as a TUI form's box takes it: typed as it is, since a box is
// not a shell and a quote in it is part of the value — quoted only where the
// call's own spelling would misread it, a value with a space running into
// the next box's or one holding a quote, or where its reader would: one
// holding a character a reader does not see as itself, or a byte that is not
// UTF-8. Quoted, each such character is named by its code point
// (glyph.Quote), as textclean.Record shows the same record beside it.
//
// By the rule the shell's spelling reads (shellquote.Arg), not
// unicode.IsPrint: that counts a Hangul filler, a Braille blank and a
// variation selector printable, so a value ending in one was left bare, and
// strconv.Quote would have left it raw inside the quotes all the same — the
// TUI's call on a padded record read as the call on the bare one.
func boxValue(v any) string {
	text := fmt.Sprint(v)
	if text == "" || !utf8.ValidString(text) || strings.ContainsFunc(text, func(r rune) bool {
		return r == ' ' || r == '"' || r == '\'' || !glyph.Seen(r)
	}) {
		return glyph.Quote(text)
	}
	return text
}

// placeholder reports whether text is one word in angle brackets, <file>.
func placeholder(text string) bool {
	if len(text) < 3 || text[0] != '<' || text[len(text)-1] != '>' {
		return false
	}
	return strings.Trim(text[1:len(text)-1], "abcdefghijklmnopqrstuvwxyz-") == ""
}

// InputName names one of a capability's inputs the way a caller on s gives
// it: the flag on the CLI (--key), the argument in the tool's schema over
// MCP (the "key" argument), and the box in a TUI form (the key box).
// SurfaceUnknown and a completion keystroke read the CLI's spelling.
//
// The CLI's spelling is the flag. A Positional input is not given by one,
// and is named with ArgumentName instead.
func (s Surface) InputName(name string) string {
	switch s {
	case SurfaceMCP:
		return `the "` + name + `" argument`
	case SurfaceTUI:
		return "the " + name + " box"
	}
	return "--" + name
}

// ArgumentName is InputName for an input declared Positional, which the CLI
// takes by its place on the command line and never as a flag: there it is
// the slot the usage line names, <hostname>, and a hint naming --hostname
// sends somebody to a flag the command refuses. Every other surface names it
// as it names any input.
func (s Surface) ArgumentName(name string) string {
	if s.spellsForCLI() {
		return "<" + name + ">"
	}
	return s.InputName(name)
}

// WithoutInputs is how a caller on s leaves inputs out of a call, for a hint
// that sends them back to run it with less: "without --key" on the CLI, and
// without the argument over MCP. A TUI form's boxes are there whether or not
// they are filled, so there it is the boxes left empty — "without the key
// box" would send somebody looking for a form that lacks one.
func (s Surface) WithoutInputs(names ...string) string {
	spelled := make([]string, len(names))
	for i, n := range names {
		spelled[i] = s.InputName(n)
	}
	if s == SurfaceTUI {
		return "with " + strings.Join(spelled, " and ") + " left empty"
	}
	return "without " + strings.Join(spelled, " and ")
}

// spellsForCLI reports whether s reads the CLI's spelling: the CLI itself,
// and the two callers with no spelling of their own.
func (s Surface) spellsForCLI() bool { return s != SurfaceMCP && s != SurfaceTUI }

// AskOperator is the hint that hands an agent a command only the person at
// the terminal can run — cmd is the command line after "rta", as in
// AskOperator("grant allow kv.get") — spelled for that person's terminal,
// because they are the one who types it.
//
// The one form in which text an agent reads may spell a command line, and
// fixed so it can be told apart from every other: an agent is never meant to
// run an `rta` command, and the test holding MCP-facing text to that reads
// this phrase as the exception it is, and nothing else. A capability the
// agent can call itself is named with CapabilityName, never with this.
func AskOperator(cmd string) string { return "ask the operator to run `rta " + cmd + "`" }
