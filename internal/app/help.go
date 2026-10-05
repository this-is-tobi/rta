package app

import (
	"cmp"
	"io"
	"os"
	"slices"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// Help is drawn here, not by the library that used to draw it.
//
// That library's layout is fixed, and it measured the terminal for the prose
// and for nothing else: a command's summary or a flag's description longer than
// the room left on its line ran off the edge and the terminal broke it
// mid-word with no indent, on the first screen anybody sees at macOS's default
// width. It put a title-casing transform over the first word of every
// description, which is how "One-screen host health" became "One-Screen" and
// "TCP connect-scan" became "Tcp". And it listed on every command the flags
// that mean something on a few — `--dry-run` and `--yes` on a command that only
// reads, `--profile` on one with no connection to point it at. None of that had
// a hook, so each was a reason to fork the renderer or to live with it. The
// renderer is a few hundred lines, it takes its colours from the palette every
// other surface reads, and being ours it is tested without a terminal.

// docsURL is where the documentation lives. The binary named it nowhere, so a
// person who ran `rta --help` had no way to learn that a site existed.
const docsURL = "https://this-is-tobi.com/rta/introduction"

// startHere is the four lines that answer "what do I type first", in the order
// a person meets the product: try it, see it, connect an agent, see what that
// agent could reach.
var startHere = []row{
	{key: "rta sys overview", desc: "try it: this machine's health, nothing to set up"},
	{key: "rta", desc: "the dashboard: no arguments opens it"},
	{key: "rta mcp install claude", desc: "connect an agent: it starts read-only until you grant more"},
	{key: "rta doctor", desc: "what an agent could reach from here"},
}

const (
	// helpIndent is where a section's entries start under its heading.
	helpIndent = 4
	// helpKeyMax bounds the first column. A key wider than this has the line
	// to itself and its description starts under the column, since a column
	// that wide leaves a narrow terminal no room for the description.
	helpKeyMax = 30
	// helpGap is the space between a key and its description.
	helpGap = 2
)

// helpWidth is how wide help is laid out: the terminal's, or COLUMNS, up to a
// line length that is still readable — and, for a pipe or a file, the width
// every terminal has had. Fixed, since the output must not depend on who asked,
// and eighty because a pipe is most often a pager (`rta kv set --help | less`)
// on a terminal that is: a wider line is broken by the pager at the edge,
// mid-word and with no hanging indent, which is the failure this renderer
// exists to avoid.
func helpWidth() int {
	const readable, floor, piped = 100, 40, 80
	w := termWidth()
	if w == 0 {
		return piped
	}
	return min(max(w, floor), readable)
}

// helpFunc is the help function of the whole tree. It closes over the global
// options because --no-color is a flag of the help being asked for.
func helpFunc(opts *globalOpts) func(*cobra.Command, []string) {
	return func(cmd *cobra.Command, _ []string) {
		profile := colorprofile.Unknown
		if opts.noColor {
			profile = colorprofile.NoTTY
		}
		writeHelp(cmd.OutOrStdout(), cmd, helpWidth(), profile)
	}
}

// writeHelp draws cmd's help to w at width columns. profile is the colour depth
// to draw it in, or Unknown for whatever w is: styled for a terminal at its own
// depth, and plain for anything that is not one.
func writeHelp(w io.Writer, cmd *cobra.Command, width int, profile colorprofile.Profile) {
	pw := &colorprofile.Writer{Forward: w, Profile: profile}
	if profile == colorprofile.Unknown {
		pw = colorprofile.NewWriter(w, os.Environ())
	}
	_, _ = io.WriteString(pw, newHelper(width).render(cmd))
}

type helper struct {
	width int
	// Built when the help is drawn, not when the package loads: theme.Apply
	// moves the palette after that.
	heading, name, muted lipgloss.Style
}

func newHelper(width int) *helper {
	return &helper{
		width:   width,
		heading: lipgloss.NewStyle().Foreground(theme.Primary).Bold(true),
		name:    lipgloss.NewStyle().Bold(true),
		muted:   lipgloss.NewStyle().Foreground(theme.Muted),
	}
}

// row is one entry of a two-column section.
type row struct {
	key, desc string
	// args dims the part of the key after its first word, the way a usage line
	// draws the arguments.
	args bool
	// note trails the description, dimmed: a flag's default.
	note string
}

// render is the whole screen: what the command is, where to start (the root
// only), how to type it, examples, the commands under it, and its flags.
func (h *helper) render(cmd *cobra.Command) string {
	var b strings.Builder
	b.WriteString("\n")
	h.prose(&b, cmp.Or(cmd.Long, cmd.Short))
	root := !cmd.HasParent()
	if root {
		h.section(&b, "start here")
		h.rows(&b, startHere, h.keyWidth(startHere))
	}
	h.section(&b, "usage")
	h.line(&b, helpIndent, h.usage(cmd))
	if examples := exampleLines(cmd); len(examples) > 0 {
		h.section(&b, "examples")
		for _, ex := range examples {
			h.example(&b, ex)
		}
	}
	groups, flags := h.commandRows(cmd), h.flagRows(cmd)
	// One first column across the groups and the flags, so the descriptions of
	// the whole screen start in the same place.
	var all []row
	for _, g := range groups {
		all = append(all, g.rows...)
	}
	keyWidth := h.keyWidth(all, flags)
	for _, g := range groups {
		h.section(&b, g.title)
		h.rows(&b, g.rows, keyWidth)
	}
	if len(flags) > 0 {
		h.section(&b, "flags")
		h.rows(&b, flags, keyWidth)
	}
	if root {
		// The start block is at the top, and the top is what a terminal of
		// twenty-four rows has already scrolled away by the time this much help
		// has been written: the last screen is the one a newcomer reads, so the
		// first thing to type is said again at the foot of it.
		b.WriteString("\n")
		h.prose(&b, "New here? `rta sys overview` shows it working. `rta <command> --help` shows a command's "+
			"examples, and `rta explain` lists every capability with what it needs. Documentation: "+docsURL)
	}
	return b.String()
}

func (h *helper) section(b *strings.Builder, title string) {
	b.WriteString("\n  " + h.heading.Render(strings.ToUpper(title)) + "\n")
}

func (h *helper) line(b *strings.Builder, indent int, s string) {
	b.WriteString(strings.Repeat(" ", indent) + s + "\n")
}

// prose draws Long: paragraphs wrapped to the width, and whatever is laid out
// by its own line breaks (an indented block, a list, a fence) left as written.
// The Arguments block is both: its rows are hung again at this width, because
// they were wrapped for a wide one.
func (h *helper) prose(b *strings.Builder, long string) {
	long = strings.TrimSpace(long)
	if long == "" {
		return
	}
	for i, p := range strings.Split(long, "\n\n") {
		if i > 0 {
			b.WriteString("\n")
		}
		switch {
		case strings.HasPrefix(p, "Arguments:\n"):
			h.arguments(b, p)
		case !strings.HasPrefix(p, " ") && (!strings.Contains(p, "\n") || isProse(p)):
			for _, line := range wrapWords(p, h.width-4) {
				h.line(b, 2, line)
			}
		default:
			for _, line := range strings.Split(p, "\n") {
				h.line(b, 2, line)
			}
		}
	}
}

// arguments hangs the rows argumentsBlock wrote: a name, a gap, and a
// description that continues under itself.
func (h *helper) arguments(b *strings.Builder, block string) {
	lines := strings.Split(block, "\n")
	h.line(b, 2, lines[0])
	var rows []row
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") {
			name, desc, _ := strings.Cut(trimmed, "  ")
			rows = append(rows, row{key: name, desc: strings.TrimSpace(desc)})
		} else if len(rows) > 0 {
			rows[len(rows)-1].desc += " " + trimmed
		}
	}
	h.rows(b, rows, h.keyWidth(rows))
}

// usage is the usage line as it is typed: the command path and its arguments,
// then what else it takes.
func (h *helper) usage(cmd *cobra.Command) string {
	use := strings.ReplaceAll(cmd.UseLine(), "[flags]", "[--flags]")
	if cmd.HasAvailableSubCommands() && !strings.Contains(use, "[command") {
		before, after, found := strings.Cut(use, " [--flags]")
		// A group that takes a verb's arguments itself (`rta lock claude`)
		// has its argument beside the commands, not after them.
		if place := useArgument.FindString(before); place != "" {
			use = strings.Replace(before, place, "[command | "+strings.Trim(place, "<>[]")+"]", 1)
		} else {
			use = before + " [command]"
		}
		if found {
			use += " [--flags]" + after
		}
	}
	return h.styleKey(row{key: use, args: true})
}

type group struct {
	title string
	rows  []row
}

// commandRows are the subcommands in the groups they were filed under, the
// unfiled first.
func (h *helper) commandRows(cmd *cobra.Command) []group {
	ids := make([]string, 1, 1+len(cmd.Groups()))
	titles := map[string]string{"": "commands"}
	for _, g := range cmd.Groups() {
		ids = append(ids, g.ID)
		titles[g.ID] = g.Title
	}
	var out []group
	for _, id := range ids {
		var rows []row
		for _, sub := range cmd.Commands() {
			if sub.GroupID != id || (!sub.IsAvailableCommand() && sub.Name() != "help") {
				continue
			}
			rows = append(rows, row{key: listKey(sub), desc: sub.Short, args: true})
		}
		if len(rows) > 0 {
			out = append(out, group{titles[id], rows})
		}
	}
	return out
}

// listKey is a command as a list names it: its name and its arguments, and not
// the flags every command takes.
func listKey(sub *cobra.Command) string {
	key := sub.Use
	for _, drop := range []string{" [flags]", " [--flags]", " [command]"} {
		key = strings.ReplaceAll(key, drop, "")
	}
	return key
}

// flagRows are the flags worth listing on cmd — its own and the root's, without
// the ones that mean nothing here — and the help flag, which cobra adds only
// once a command is run and so is not in the set when help is asked for by
// name.
func (h *helper) flagRows(cmd *cobra.Command) []row {
	// Merges the root's persistent flags into the set; Flags() alone is without
	// them until a command has been run.
	cmd.InheritedFlags()
	var rows []row
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" || f.Name == "version" || !listsFlag(cmd, f) {
			return
		}
		key := "--" + f.Name
		if f.Shorthand != "" {
			key = "-" + f.Shorthand + ", " + key
		}
		r := row{key: key, desc: sentence(f.Usage)}
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && f.DefValue != "[]" {
			r.note = "(" + f.DefValue + ")"
		}
		rows = append(rows, r)
	})
	rows = append(rows, row{key: "-h, --help", desc: "Show this help"})
	if !cmd.HasParent() && cmd.Version != "" {
		rows = append(rows, row{key: "-v, --version", desc: "Show the version"})
	}
	slices.SortStableFunc(rows, func(a, b row) int { return strings.Compare(longName(a.key), longName(b.key)) })
	return rows
}

// longName is the flag's long spelling, which is how pflag sorts them and how
// a person looks one up: a shorthand does not move a flag.
func longName(key string) string {
	_, long, _ := strings.Cut(key, "--")
	return long
}

// Annotations for the help that lists flags. A flag is accepted wherever it was
// accepted; these decide only where it is listed.
const (
	// annotSafety marks a command materialised from a capability, with its
	// safety class: what separates one that reads from a hand-written command
	// that is not in readOnlyCommands.
	annotSafety = "rta.help.safety"
	// annotUnlisted marks a flag the command accepts and its help does not
	// list: the generic --profile of a capability with no connection to point
	// it at. On the flag, not on the command, because `grant allow` and
	// `dashboard add` have a --profile of their own, which is what they are for.
	annotUnlisted = "rta.help.unlisted"
	// annotCredential marks a flag that carries a credential. An error that
	// names the flags a command takes (valueFlagsHint) leaves these out: a
	// sentence offered to somebody who typed an extra word is no place to
	// suggest putting a secret on the command line, where it is in argv.
	annotCredential = "rta.help.credential"
)

// capabilityAnnotations is what attach records about a capability for
// commandWrites.
func capabilityAnnotations(c plugin.Capability) map[string]string {
	return map[string]string{annotSafety: string(c.Safety)}
}

// hasConnection says whether a capability reads an endpoint or a credential a
// profile can fill: the only reason to point it at a profile.
func hasConnection(c plugin.Capability) bool {
	return slices.ContainsFunc(c.Inputs, func(f plugin.Field) bool {
		return plugin.ProfileFillable(c, f) && (f.Endpoint != plugin.EndpointNone || f.Local)
	})
}

// readOnlyCommands are the hand-written commands that never write and never
// ask. They accept --yes and --dry-run because every command does, and list
// neither. A command left out lists both, which is the safe way to be wrong.
var readOnlyCommands = map[string]bool{
	"rta explain": true, "rta doctor": true, "rta config schema": true, "rta profile list": true,
	"rta profile show": true, "rta dashboard list": true, "rta plugin list": true,
	"rta plugin search": true, "rta plugin outdated": true, "rta plugin doc": true,
	"rta plugin manifest": true, "rta plugin index list": true, "rta policy show": true,
	"rta mcp serve": true,
}

// listsFlag says whether cmd's help lists f.
func listsFlag(cmd *cobra.Command, f *pflag.Flag) bool {
	if _, unlisted := f.Annotations[annotUnlisted]; unlisted {
		return false
	}
	if f.Name == "yes" || f.Name == "dry-run" {
		return commandWrites(cmd)
	}
	return true
}

// commandWrites says whether --yes and --dry-run mean something to cmd. A
// group does not run, the completion scripts print text, a capability that
// reads has nothing to confirm or to preview, and a hand-written command is
// presumed to write unless it is known not to.
func commandWrites(cmd *cobra.Command) bool {
	switch {
	case cmd.HasSubCommands(), cmd.Name() == "help", strings.HasPrefix(cmd.CommandPath(), "rta completion"):
		return false
	case cmd.Annotations[annotSafety] != "":
		return cmd.Annotations[annotSafety] != string(plugin.Read)
	}
	return !readOnlyCommands[cmd.CommandPath()]
}

// sentence capitalises the first letter of a description written as a fragment
// ("skip confirmation prompts") and changes nothing else about it. A first
// word that is not plain lower-case letters — a name with a dot or a colon in
// it, a flag, `rta` — is spelled the way it was meant to be. The transform this
// replaces made every first word a title.
func sentence(s string) string {
	first, _, _ := strings.Cut(s, " ")
	if first == "" || first == "rta" || strings.ToLower(first) != first {
		return s
	}
	for i, r := range first {
		if !unicode.IsLetter(r) && (r != '-' || i == 0) {
			return s
		}
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// keyWidth is the first column's width for the given rows: the widest key
// that fits the column.
func (h *helper) keyWidth(sets ...[]row) int {
	w := 0
	for _, rows := range sets {
		for _, r := range rows {
			if kw := ansi.StringWidth(r.key); kw <= helpKeyMax {
				w = max(w, kw)
			}
		}
	}
	return w
}

// rows draws entries with the description hanging under itself: the key, a
// gap, the description wrapped to what is left of the line, and each
// continuation under the first line's.
func (h *helper) rows(b *strings.Builder, rows []row, keyWidth int) {
	descAt := helpIndent + keyWidth + helpGap
	room := max(h.width-descAt, 20)
	for _, r := range rows {
		key, kw := h.styleKey(r), ansi.StringWidth(r.key)
		lines := wrapWords(strings.TrimSpace(r.desc+" "+r.note), room)
		switch {
		case len(lines) == 0:
			h.line(b, helpIndent, key)
		case kw > keyWidth:
			h.line(b, helpIndent, key)
			for _, line := range lines {
				h.line(b, descAt, h.dimNote(line, r.note))
			}
		default:
			h.line(b, helpIndent, key+strings.Repeat(" ", keyWidth-kw+helpGap)+h.dimNote(lines[0], r.note))
			for _, line := range lines[1:] {
				h.line(b, descAt, h.dimNote(line, r.note))
			}
		}
	}
}

// dimNote paints a trailing note where the wrap left it.
func (h *helper) dimNote(line, note string) string {
	if note == "" || !strings.HasSuffix(line, note) {
		return line
	}
	return strings.TrimSuffix(line, note) + h.muted.Render(note)
}

func (h *helper) styleKey(r row) string {
	head, tail, _ := strings.Cut(r.key, " ")
	if !r.args || tail == "" {
		return h.name.Render(r.key)
	}
	return h.name.Render(head) + " " + h.muted.Render(tail)
}

// example draws one line of an example block: a comment dimmed, a command as
// written. Never cut short; a command truncated on a narrow terminal is one
// that cannot be copied.
func (h *helper) example(b *strings.Builder, line string) {
	if strings.HasPrefix(strings.TrimSpace(line), "#") {
		line = h.muted.Render(line)
	}
	h.line(b, helpIndent, line)
}

// exampleLines are a command's examples without the indent they were written
// with.
func exampleLines(cmd *cobra.Command) []string {
	if strings.TrimSpace(cmd.Example) == "" {
		return nil
	}
	lines := strings.Split(strings.Trim(cmd.Example, "\n"), "\n")
	indent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			n := len(l) - len(strings.TrimLeft(l, " "))
			if indent < 0 || n < indent {
				indent = n
			}
		}
	}
	for i, l := range lines {
		lines[i] = strings.TrimRight(l[min(indent, len(l)):], " ")
	}
	return lines
}
