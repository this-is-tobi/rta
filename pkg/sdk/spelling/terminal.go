package spelling

import "regexp"

// terminalWording is how text speaks to a person at a command line: where a
// sentence is read, what a pipe does to it, what happens from a terminal, the
// dashboard's own parts. Said in a description an agent reads, it is spent
// context and, worse, a promise about a surface the reader does not have.
//
// Phrases, not words. "dashboard" and "tile" alone are ordinary words in
// somebody else's domain — a Grafana dashboard, a map tile — and a rule that
// refused them would be waived so often it would stop being read. What
// remains is each way text addresses rta's own screens and a shell.
var terminalWording = regexp.MustCompile("(?i)\\bfrom a terminal\\b|\\b(?:at|on) (?:a|the) terminal\\b|" +
	"\\bon the CLI\\b|\\bread from a pipe\\b|\\bpiping\\b|\\bshell history\\b|" +
	"\\bfull-page surface\\b|\\bdashboard tiles?\\b|\\b(?:on|from|in) the dashboard\\b|\\bthe TUI\\b")

// TerminalWording returns the first phrase in text that addresses a person at
// a terminal rather than whoever is reading — an agent with a tool list and an
// arguments schema, or somebody at the TUI's form — and "" when there is none.
//
// It is for text an agent reads, which is every capability's agent text and
// the help of every input an agent can give. Where a capability behaves
// differently from a terminal the text says that, once, and the handler words
// the difference at run time through the request's surface; text that has to
// describe a terminal and is read by an agent is text that belongs in
// Description beside an Agent of its own.
func TerminalWording(text string) string { return terminalWording.FindString(text) }
