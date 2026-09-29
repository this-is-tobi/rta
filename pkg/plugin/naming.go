package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/shellquote"
	"github.com/this-is-tobi/rta/internal/textclean/glyph"
	"github.com/this-is-tobi/rta/pkg/format"
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
// bare flag, and false one turned off, joined to its flag: --tls=false. A
// []string is a StringSlice input's list: the flag once per element on the
// CLI (--grant a --grant b), a word each when Positional, a JSON array over
// MCP, and the box's comma-separated text in the TUI (grant=a,b).
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
		// Sorted by name, so one call is spelled the same way every time,
		// and each value encoded on its own (mcpValue), since one of them
		// may have no JSON to be encoded as.
		names := slices.Sorted(maps.Keys(values))
		fields := make([]string, len(names))
		for i, name := range names {
			fields[i] = mcpValue(name) + ":" + mcpValue(values[name])
		}
		return ToolName(id) + " {" + strings.Join(fields, ",") + "}"
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
		if !a.Positional {
			parts = append(parts, flagTo(a.Name, a.Value))
			continue
		}
		// A list given by its place is the rest of the command line, a word
		// per element, which the CLI hands the input as they come: no
		// splitting at a comma there, unlike a list flag's value.
		if list, ok := a.Value.([]string); ok {
			for _, e := range list {
				parts = append(parts, cliValue(e))
			}
			continue
		}
		parts = append(parts, cliValue(a.Value))
	}
	return strings.Join(parts, " ")
}

// mcpValue is v as a tool's arguments carry it: JSON, without the HTML
// escaping json.Marshal does — nothing here reaches a page, and a
// placeholder's angle brackets, each turned into a six-letter escape, handed
// an agent a value nobody wrote.
//
// **A string that is not UTF-8 has no JSON, and is not spelled as one.** A
// tool's arguments are JSON, whose strings are Unicode, so no argument an
// agent can send holds a byte that is not UTF-8. encoding/json writes U+FFFD
// in such a byte's place, and the call it spelled was a call on another
// record: `kv_get {"key":"db` and the replacement character, a key that is
// not the one stored, and a grant on one is no grant on the other. Such a
// value is spelled in its JSON's place as what it is, not UTF-8, with the
// byte named as the TUI and textclean.Record name it (glyph.Quote) — a form
// that is not JSON, so no agent can send it as it stands and take it for
// the value. A list holding one is spelled element by element, so the
// others still read as themselves.
func mcpValue(v any) string {
	switch t := v.(type) {
	case string:
		if !utf8.ValidString(t) {
			return "<not UTF-8: " + glyph.Quote(t) + ">"
		}
	case []string:
		if slices.ContainsFunc(t, func(s string) bool { return !utf8.ValidString(s) }) {
			items := make([]string, len(t))
			for i, s := range t {
				items[i] = mcpValue(s)
			}
			return "[" + strings.Join(items, ",") + "]"
		}
	}
	var encoded bytes.Buffer
	enc := json.NewEncoder(&encoded)
	enc.SetEscapeHTML(false)
	// A plain value always encodes; one that does not is spelled as Go
	// prints it rather than as nothing.
	if err := enc.Encode(v); err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSuffix(encoded.String(), "\n")
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
	if list, ok := v.([]string); ok {
		return boxList(list)
	}
	text := fmt.Sprint(v)
	if text == "" || !utf8.ValidString(text) || strings.ContainsFunc(text, func(r rune) bool {
		return r == ' ' || r == '"' || r == '\'' || !glyph.Seen(r)
	}) {
		return glyph.Quote(text)
	}
	return text
}

// boxList is a list as a TUI box takes it: the elements joined at commas,
// the text the form splits back into them, each trimmed of the space around
// it — spelled as boxValue spells any text, quoted where a reader would
// misread it. fmt.Sprint spelled it as Go prints a slice, and `grant=[a b]`
// typed into the box was the one element "[a b]".
//
// **A list the box cannot hold is not spelled as one it can.** An element
// holding a comma is two once the box splits it, one with space at an end
// loses that space, and a lone empty element leaves the box empty, which
// answers nothing. No text typed into the box gives such a list, so it is
// spelled with each element quoted (glyph.Quote) inside angle brackets, as
// mcpValue spells a string that is not UTF-8: something the reader sees is
// not text to type, where the joined text would have been a call on other
// values.
func boxList(list []string) string {
	unheld := len(list) == 1 && list[0] == "" || slices.ContainsFunc(list, func(e string) bool {
		return strings.Contains(e, ",") || e != strings.TrimSpace(e)
	})
	if !unheld {
		return boxValue(strings.Join(list, ","))
	}
	quoted := make([]string, len(list))
	for i, e := range list {
		quoted[i] = glyph.Quote(e)
	}
	return "<a list no box text holds: " + strings.Join(quoted, ", ") + ">"
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

// SettingName names connection settings — inputs declared Local, the host
// and the password and the CA file a plugin connects with — the way the
// reader on s changes them: the flags on the CLI (--host and --port), the
// boxes in a TUI form (the host and port boxes), and over MCP the operator's
// settings by the names the declaration gives them (the operator's `host`
// and `port` settings).
//
// Not InputName over MCP, which names an argument: a Local input is in no
// tool's schema and the bridge drops one an agent sends, so an agent told to
// check the "endpoint" argument passed one that was thrown away and read the
// same refusal again. Named as the operator's, it is a setting the agent
// can report and cannot change, and SettingsHint says where the operator
// changes it. Every connection plugin wrote this helper for itself before it
// was here.
func (s Surface) SettingName(names ...string) string {
	switch s {
	case SurfaceMCP:
		quoted := make([]string, len(names))
		for i, n := range names {
			quoted[i] = "`" + n + "`"
		}
		return "the operator's " + listed(quoted) + " " + format.Plural(len(names), "setting", "settings")
	case SurfaceTUI:
		return "the " + listed(names) + " " + format.Plural(len(names), "box", "boxes")
	}
	flags := make([]string, len(names))
	for i, n := range names {
		flags[i] = s.InputName(n)
	}
	return listed(flags)
}

// SettingTo is SettingName for one setting given value, the way the reader
// gives it: `--sslmode disable` as a command line takes it, a switch as Call
// spells one (--tls, --tls=false), and elsewhere the setting with the value
// beside it — the operator's `sslmode` set to disable, the sslmode box set to
// disable. Any Local input's, a path only the operator may name as much as a
// connection's; an input an agent gives as an argument is InputTo's.
//
// A list is the JSON array to an agent, as InputTo spells one there: the
// operator sets it in no box, and a list a box cannot hold was spelled to
// the agent as one "no box text holds", a box it has never seen.
func (s Surface) SettingTo(name string, value any) string {
	switch s {
	case SurfaceMCP:
		if list, ok := value.([]string); ok {
			return "the operator's `" + name + "` set to " + mcpValue(list)
		}
		return "the operator's `" + name + "` set to " + boxValue(value)
	case SurfaceTUI:
		return s.SettingName(name) + " set to " + boxValue(value)
	}
	return flagTo(name, value)
}

// flagTo is flag name given value as a command line takes it, the way Call
// spells an argument that is no Positional one: --sslmode disable, a switch
// bare when on and joined to its flag when off, and a list as the flag once
// per element.
//
// A switch turned off is joined, never a word of its own: pflag gives a
// switch its value only after an equals sign, and reads the word after it as
// the command's own argument, so --tls false turned the switch on and handed
// the command a stray "false" — the opposite call.
//
// A list is the flag repeated, since pflag's StringSlice appends what each
// occurrence gives; fmt.Sprint spelled it as Go prints a slice, and
// `--grant '[a b]'` was a call on the one element "[a b]". Not the elements
// joined at commas either: the same StringSlice splits every occurrence's
// value at its commas, so an element holding one was two (listElement).
func flagTo(name string, value any) string {
	switch value {
	case true:
		return "--" + name
	case false:
		return "--" + name + "=false"
	}
	list, ok := value.([]string)
	if !ok {
		return "--" + name + " " + cliValue(value)
	}
	if len(list) == 0 {
		// An empty value empties the list, where leaving the flag out would
		// have left its default in place.
		return "--" + name + " ''"
	}
	words := make([]string, len(list))
	for i, e := range list {
		words[i] = "--" + name + " " + listElement(e)
	}
	return strings.Join(words, " ")
}

// listElement is one element of a list flag's value as a command line carries
// it: one shell word that pflag's StringSlice reads back as the element.
// StringSlice reads each occurrence's value as a line of CSV, so an element
// holding a comma, a double quote or a line break goes in CSV's double quotes
// — `--grant '"a,b"'` is the one element a,b — and so does an empty one,
// which bare is no element at all.
//
// **An element holding a carriage return before a line feed has no
// spelling.** CSV's reader turns that pair into a line feed alone, quoted or
// not, and the call spelled would be a call on another value, which for a
// grant or a key is another record. Such an element is spelled as what it
// is, named as the TUI names it (glyph.Quote), inside angle brackets as a
// placeholder is: something the reader sees is not a value to paste, as
// mcpValue spells a string that is not UTF-8.
func listElement(e string) string {
	switch {
	case strings.Contains(e, "\r\n"):
		return "<no list flag keeps its CR LF: " + glyph.Quote(e) + ">"
	case e == "" || strings.ContainsAny(e, ",\"\r\n"):
		return shellquote.Arg(`"` + strings.ReplaceAll(e, `"`, `""`) + `"`)
	}
	return cliValue(e)
}

// InputTo is SettingTo for an input the caller gives on every surface — any
// flag input not declared Local, --format or --method — with its value
// spelled as Call spells that argument in a whole call: `--format directory`
// on the CLI, a switch bare or joined (--online, --online=false), the
// "format" argument set to "directory" over MCP, where the value is the JSON
// the agent sends, and the format box set to directory in the TUI. For a
// hint handing its reader one input to add to the call they made —
// "`--jobs 1` runs it serially" — where Call would repeat the whole call.
//
// A Local input is SettingTo's, whether a connection's or a path only the
// operator may name, as --out is: it is in no tool's schema, and an agent
// told to set the argument passes one the bridge drops. The two spell the
// same at a terminal and in a form; they differ over MCP, which is the
// whole of why there are two.
//
// Spelled with Call's values, not pasted beside the name as each of nine
// plugins did for both kinds in a helper of its own: there a value built
// from what a server holds — a backup method a cluster names, a file named
// after a database — went onto the command line bare, and a paste of
// `--out ./$(id).sql` ran what the name held; and an agent was told to set
// "jobs" to 1 with nothing to say whether that is the number or the string.
// A Positional input is given by its place rather than as a flag, and Call
// spells the call it is given in.
func (s Surface) InputTo(name string, value any) string {
	switch s {
	case SurfaceMCP:
		return s.InputName(name) + " set to " + mcpValue(value)
	case SurfaceTUI:
		return s.InputName(name) + " set to " + boxValue(value)
	}
	return flagTo(name, value)
}

// SettingsHint sends the reader on s to where a connection's settings are
// set: the page `rta explain id` prints, which lists every input and each
// place it can come from — the command line, the operator's rta config, a
// profile, the environment. A terminal's command with no capability behind
// it, so over MCP it is the operator who is asked to read it, and the hint
// says the settings are theirs.
//
// Which of those places, not all of them: a password has no config key and
// a host no environment variable, each by its declaration, and a hint saying
// every setting can be written anywhere would send the operator to put a
// password in the config file, where nothing reads it.
func (s Surface) SettingsHint(id string) string {
	if s == SurfaceMCP {
		return AskOperator("explain "+id) + ", which lists every setting and which of the rta config, a " +
			"profile and the environment the operator can set it in"
	}
	return "`rta explain " + id + "` lists every input and which of the command line, the rta config, " +
		"a profile and the environment can set it"
}

// DNSHint is the call that shows what DNS returns for host, spelled for the
// reader on s, for a connection that failed on a name that did not resolve.
// Here rather than in each plugin because it names one of rta's own
// capabilities, net.dns, which rta's tests hold to its registry: written out
// in every connection plugin, a rename of the built-in would have left each of
// them naming a call nothing answers.
func (s Surface) DNSHint(host string) string {
	return "`" + s.Call("net.dns", Arg{Name: "name", Value: host, Positional: true}) + "` shows what DNS returns"
}

// listed joins words as a sentence lists them: a, a and b, a, b and c.
func listed(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
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
