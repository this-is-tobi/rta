package plugin

import "strings"

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
	return "`rta " + strings.ReplaceAll(id, ".", " ") + "`"
}

// InputName names one of a capability's inputs the way a caller on s gives
// it: the flag on the CLI (--key), the argument in the tool's schema over
// MCP (the "key" argument), and the box in a TUI form (the key box).
// SurfaceUnknown and a completion keystroke read the CLI's spelling.
//
// The CLI's spelling is the flag. A Positional input is not given by one,
// and a message about it on the CLI says where it goes in words of its own
// — "give it as an argument" — as MissingInput does.
func (s Surface) InputName(name string) string {
	switch s {
	case SurfaceMCP:
		return `the "` + name + `" argument`
	case SurfaceTUI:
		return "the " + name + " box"
	}
	return "--" + name
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
