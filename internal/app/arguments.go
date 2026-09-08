package app

import (
	"cmp"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// argDoc is one positional argument as help should describe it.
type argDoc struct {
	Name string
	Help string
}

// argWrapWidth is where a row breaks, chosen to be narrower than the terminal
// widths lipgloss will re-wrap this text at rather than to match any of them.
// fang hands Long to lipgloss with the terminal's width; a row that already
// fits is left alone and keeps its hang indent, while one that does not is
// re-wrapped flush and loses the column. Wrapping short is what makes the
// first case the usual one. A terminal narrower than this still degrades to
// flush prose, which is legible and not worth forking fang's renderer to
// avoid.
const argWrapWidth = 88

// argumentsBlock renders the section that documents a command's positional
// arguments, for appending to its Long text.
//
// Long, rather than a section of its own, because there is nowhere else to put
// it. Help is rendered by fang, whose layout is fixed — long text, usage,
// examples, then the command and flag groups — and which exposes no hook to
// add a group. Its Example field renders after usage, in the right place, but
// truncates each line to the code block's width with an ellipsis, which is
// fine for an example and destroys a description. Reimplementing fang's
// renderer to gain one section would fork a moving target for every command in
// the tree. So the block goes at the end of Long and renders immediately above
// the usage line instead of below it.
func argumentsBlock(args []argDoc) string {
	described := make([]argDoc, 0, len(args))
	width := 0
	for _, a := range args {
		if a.Help == "" {
			continue
		}
		described = append(described, a)
		width = max(width, len(a.Name))
	}
	if len(described) == 0 {
		return ""
	}
	indent := 2 + width + 2
	var b strings.Builder
	b.WriteString("Arguments:")
	for _, a := range described {
		head := "  " + a.Name + strings.Repeat(" ", width-len(a.Name)+2)
		for i, line := range wrapWords(a.Help, argWrapWidth-indent) {
			if i == 0 {
				b.WriteString("\n" + head + line)
				continue
			}
			b.WriteString("\n" + strings.Repeat(" ", indent) + line)
		}
	}
	return b.String()
}

// withArguments appends the block to a command's long text. The one joiner
// both sources go through: capability commands, whose descriptions come from
// the inputs they already declare, and the hand-written trees, whose
// descriptions are spelled out in documentArguments' table because they have
// no Inputs to read.
func withArguments(long string, args []argDoc) string {
	block := argumentsBlock(args)
	switch {
	case block == "":
		return long
	case long == "":
		return block
	default:
		return long + "\n\n" + block
	}
}

// documentArgs gives one hand-written command the arguments section a
// capability command gets from its Inputs.
//
// Seeded from Short when there is no Long, because fang renders
// cmp.Or(Long, Short): a command that had only a summary would otherwise trade
// it for the block, documenting the argument by deleting the sentence saying
// what the command is.
func documentArgs(cmd *cobra.Command, args ...argDoc) {
	cmd.Long = withArguments(cmp.Or(cmd.Long, cmd.Short), args)
}

// documentArguments describes the positional arguments of the commands that
// are written by hand rather than materialized from a capability. They declare
// no Inputs, so unlike the 107 capability commands there is nothing to derive
// and the sentences are written here.
//
// A pass over the finished tree, keyed by command path, for the same reason
// describeGroups is one: several of these commands are returned as literals
// with no variable to hang a call off, they are assembled across five files,
// and keeping the descriptions in one table is what lets a reader check them
// against each other. TestEveryCommandWithAnArgumentDocumentsIt fails when a
// new command with an argument is not in it.
func documentArguments(root *cobra.Command) {
	clients := make([]string, 0)
	for _, c := range mcpClients() {
		clients = append(clients, c.name)
	}
	sort.Strings(clients)

	table := map[string][]argDoc{
		"rta explain": {{"capability", "capability ID to print in full, e.g. sys.cpu — omit to list every one"}},
		"rta mcp install": {{"client",
			"MCP client to register rta in: " + strings.Join(clients, ", ")}},
		"rta plugin allow": {
			{"name", "plugin to allow — omit to list what every loaded plugin asks for"},
			{"location...", "credential locations to allow, from the ones that plugin declares — " +
				"omit to allow everything it declares"},
		},
		"rta plugin dev":      {{"dir", "directory holding the plugin's source (default: the current directory)"}},
		"rta plugin disallow": {{"name", "plugin to withdraw every allowed location from"}},
		"rta plugin doc":      {{"binary", "path to the plugin binary, run sandboxed to read its declaration"}},
		"rta plugin index add": {
			{"name", "name to attach the index under — `official` is reserved for the first-party one"},
			{"repository", "git repository to clone — omit only for `official`, which rta knows by name"},
		},
		"rta plugin index remove": {{"name", "index to detach"}},
		"rta plugin index update": {{"name", "index to bring up to date — omit for every attached index"}},
		"rta plugin install": {{"name | index/name",
			"plugin to install, qualified with an index when more than one claims the name"}},
		"rta plugin manifest": {{"binary", "path to the plugin binary, run sandboxed to read its declaration"}},
		"rta plugin new": {{"name",
			"namespace for the new plugin: it becomes `rta <name> ...` and prefixes every capability ID"}},
		"rta plugin remove": {{"name", "managed plugin to uninstall"}},
		"rta plugin search": {{"term", "text to match against what the indexes claim — omit to list everything"}},
		"rta plugin trust":  {{"name", "discovered plugin to approve — omit to list what was found and not run"}},
		"rta plugin untrust": {{"name|digest",
			"plugin name to withdraw every approval under, or a digest prefix for one artifact"}},
		"rta plugin upgrade": {{"name", "managed plugin to move to what its index now claims"}},
		"rta profile repin": {{"profile",
			"profile to repin — omit with --all for every profile holding an entry for the plugin"}},
		"rta profile rm":   {{"profile", "environment to remove, or to remove one plugin from with --plugin"}},
		"rta profile set":  {{"profile", "environment to create or update"}},
		"rta profile show": {{"profile", "environment to describe"}},
		"rta use":          {{"profile", "environment to switch to — omit to print what is on"}},
	}

	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if args, ok := table[c.CommandPath()]; ok {
			documentArgs(c, args...)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// capabilityArgs describes a capability's positionals from what it already
// declared. flagUsage, rather than Field.Help directly, so a positional with a
// closed set reads "one of: a|b|c" exactly as the same input would if it were
// a flag.
func capabilityArgs(positionals []plugin.Field) []argDoc {
	out := make([]argDoc, 0, len(positionals))
	for _, f := range positionals {
		out = append(out, argDoc{Name: f.Name, Help: flagUsage(f)})
	}
	return out
}

// wrapWords breaks s on spaces so no line exceeds width display columns. A
// single word longer than width keeps its own line rather than being cut:
// these are hostnames, URL schemes and capability IDs, and a reader can scroll
// but cannot restore a character that was dropped mid-token.
func wrapWords(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	lines := []string{words[0]}
	for _, w := range words[1:] {
		last := len(lines) - 1
		if ansi.StringWidth(lines[last])+1+ansi.StringWidth(w) <= width {
			lines[last] += " " + w
			continue
		}
		lines = append(lines, w)
	}
	return lines
}
