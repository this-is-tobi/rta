package app

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/this-is-tobi/rta/internal/match"
)

// What a wrong guess is answered with.
//
// A word typed at the prompt is almost never a typo of a command; it is the
// task, in the words the person thinks it in. `rta revoke`, `rta status`,
// `rta log`, `rta ps`, `rta uuid`, `rta theme`: every one of them was answered
// with "unknown command", and the ones that could pass for a service — nearly
// every short word does — with a pointer at installing a plugin of that name,
// which is false for a verb and sent `rta install pg` to `rta plugin install
// install`. The commands that mean them exist, one noun deeper, and
// `rta explain` already knew how to find them. This asks the same matcher
// (internal/match) over the command tree, so a word finds the same thing at
// the prompt, in the search box and in `rta explain`.
//
// A word that is a command's whole name — its last word, a word in its path, or
// one of its keywords — is the answer to "which command is that". A word that
// only resembles one (a typo, a word in the summary) is a guess, offered when
// nothing better is. A word that is the start of a root command stays with the
// edit-distance suggestion cobra already makes: `rta sy` is `sys`, not every
// command with sys in its path.

// commandKeywords are the words a person types for a command that are in neither
// its name nor its summary: the operator's own vocabulary, and the settings
// words that name no command at all. Keyed by the command's path. A word that
// is already the command's name (`revoke` for grant revoke) needs no entry; the
// matcher finds it. Every path here has to be a command, and a test holds them
// to it.
var commandKeywords = map[string][]string{
	"rta agent overview": {"status", "activity"},
	"rta agent log":      {"history", "record", "calls"},
	"rta agent allow":    {"approve"},
	"rta agent deny":     {"reject"},
	"rta grant allow":    {"permit", "authorize"},
	"rta lock add":       {"freeze", "block"},
	"rta lock rm":        {"unlock", "unfreeze", "unblock"},
	"rta plugin remove":  {"uninstall"},
	"rta mcp install":    {"connect", "register"},
	"rta cert":           {"certificate", "certificates"},
	"rta config":         {"settings", "theme", "colors", "colours", "cfg", "preferences"},
	"rta dashboard":      {"tiles", "widgets", "layout", "home"},
	"rta init":           {"setup", "wizard"},
	"rta doctor":         {"check", "health", "diagnose"},
	"rta use":            {"switch", "environment"},
	"rta explain":        {"capabilities", "catalogue", "catalog", "describe"},
}

// firstChoice names, for a word that is the verb of several commands, the one
// people mean: `rta install pg` is `rta plugin install pg` and not `mcp
// install`, and `rta log` is the record before it is a repository's history.
// It is the whole answer, and carries the arguments typed after the word. A test
// holds every path to a command.
var firstChoice = map[string]string{
	"install": "rta plugin install",
	"log":     "rta agent log",
	"status":  "rta agent overview",
	"allow":   "rta grant allow",
	"revoke":  "rta grant revoke",
}

// manyHits is how many whole-name matches a word may have before it is no
// longer a guess at one command but a verb that belongs to many (`list`).
const manyHits = 3

// suggestion is what a wrong word was matched to.
type suggestion struct {
	commands []*cobra.Command
	// whole is a command's name, as opposed to a likeness to one.
	whole bool
	// more is that the word names more commands than are shown.
	more bool
}

// namesAGroup is whether any of the commands is a group of commands and not one
// that runs.
func (s suggestion) namesAGroup() bool {
	return slices.ContainsFunc(s.commands, func(c *cobra.Command) bool { return c.HasSubCommands() })
}

// byKeyword is whether word is one of the keywords of every command it was
// matched to: a synonym of a command that exists, spelled the way operators
// say it (`theme` for `config`), and so no name a plugin could be hiding under.
func (s suggestion) byKeyword(word string) bool {
	word = strings.ToLower(word)
	return len(s.commands) > 0 && !slices.ContainsFunc(s.commands, func(c *cobra.Command) bool {
		return !slices.Contains(commandKeywords[c.CommandPath()], word)
	})
}

// suggestCommands finds the commands of group that word names, or — with
// likenesses — the ones it most likely misspells or describes; the zero
// suggestion when it is neither. A word has to be longer than a few letters to
// be offered a likeness, because the short ones are the names of services that
// are not installed — pg, s3 — and a neighbour of those is chance. A word that
// asks for help on a subject (`rta help security`) is no typo of anything, and
// asks for names alone.
func suggestCommands(group *cobra.Command, word string, likenesses bool) suggestion {
	word = headVerb(word)
	items, cmds := commandItems(group, true)
	if lead, ok := firstChoice[strings.ToLower(word)]; ok && !group.HasParent() {
		if first := commandAt(group, lead); first != nil {
			return suggestion{commands: []*cobra.Command{first}, whole: true}
		}
	}
	var named []*cobra.Command
	for _, r := range match.Find(word, items) {
		if r.Score >= match.Whole {
			named = append(named, cmds[r.Index])
		}
	}
	if named = outermost(named); len(named) > 0 {
		return suggestion{commands: named[:min(len(named), manyHits)], whole: true, more: len(named) > manyHits}
	}
	if !likenesses || len([]rune(word)) < 4 {
		return suggestion{}
	}
	// Without the summaries: a word that is in the sentence about a command
	// and not in its name (`memory` is in the summary of sys overview) is no
	// reason to offer that command for `sys memory`, where `sys mem` is what
	// was meant.
	items, cmds = commandItems(group, false)
	var likely []*cobra.Command
	for _, r := range match.Nearest(word, items) {
		// A likeness is never the other side of the word: `rta mcp uninstall`
		// was offered `rta mcp install`, two letters away and the reverse of
		// what was asked. A whole-word match above is left alone, since a
		// command that names the word among its keywords (plugin remove, for
		// uninstall) is the answer.
		if r.Score >= match.Likely && !opposite(strings.ToLower(word), cmds[r.Index].Name()) {
			likely = append(likely, cmds[r.Index])
		}
	}
	if likely = outermost(likely); len(likely) > 0 {
		return suggestion{commands: likely[:min(len(likely), manyHits)]}
	}
	return suggestion{}
}

// headVerb is the verb a synonym stands for (verbSynonyms): `ls` is `list`, so
// at the root, where no command is named either, `rta ls` is told what `rta list`
// is — the verb belongs to many commands — and not that `fs` is its neighbour.
func headVerb(word string) string {
	for _, set := range verbSynonyms {
		if slices.Contains(set, strings.ToLower(word)) {
			return set[0]
		}
	}
	return word
}

// outermost drops what repeats: a command listed twice, and one under another
// that is listed — `net hosts` stands for every verb of `net hosts`.
func outermost(cmds []*cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, c := range cmds {
		if slices.Contains(out, c) {
			continue
		}
		covered := false
		for _, o := range out {
			if strings.HasPrefix(c.CommandPath(), o.CommandPath()+" ") {
				covered = true
			}
		}
		if !covered {
			out = append(out, c)
		}
	}
	return out
}

// commandItems is the commands under group as the matcher reads them, in the
// tree's order, with each one's path inside group as its ID and, when asked
// for, its summary. A group is an item beside its verbs, so that a word that
// names every verb of one (`certificate` for cert) is answered with the group.
func commandItems(group *cobra.Command, summaries bool) ([]match.Item, []*cobra.Command) {
	var items []match.Item
	var cmds []*cobra.Command
	prefix := group.CommandPath() + " "
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if !sub.IsAvailableCommand() || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			item := match.Item{
				ID:       strings.ReplaceAll(strings.TrimPrefix(sub.CommandPath(), prefix), " ", "."),
				Keywords: append(slices.Clone(commandKeywords[sub.CommandPath()]), strings.Fields(sub.Annotations[annotKeywords])...),
			}
			if summaries {
				item.Summary = sub.Short
			}
			items = append(items, item)
			cmds = append(cmds, sub)
			walk(sub)
		}
	}
	walk(group)
	return items, cmds
}

// suggestionHint is the sentence that names what a word probably meant, with
// the arguments typed after it when one command is meant — `rta install pg` is
// `rta plugin install pg`. "" for no suggestion.
func suggestionHint(s suggestion, word string, rest []string) string {
	if len(s.commands) == 0 {
		return ""
	}
	lines := make([]string, len(s.commands))
	for i, c := range s.commands {
		lines[i] = "`" + c.CommandPath() + "`"
	}
	if len(lines) == 1 {
		if args := strings.Join(rest, " "); args != "" && !s.commands[0].HasSubCommands() {
			lines[0] = "`" + s.commands[0].CommandPath() + " " + args + "`"
		}
		return "did you mean " + lines[0] + "?"
	}
	if s.more {
		return "`" + word + "` is under many commands — " + strings.Join(lines[:2], ", ") +
			" among them; `rta explain` lists every capability"
	}
	return "did you mean " + strings.Join(lines[:len(lines)-1], ", ") + " or " + lines[len(lines)-1] + "?"
}

// nearestFlags are the flags of cmd that a mistyped one most likely meant: one
// that starts with what was typed (`--core` for `--cores`), one it starts with,
// or one a letter or two away. Hidden flags are not offered.
func nearestFlags(cmd *cobra.Command, typed string) []string {
	typed = strings.ToLower(typed)
	type scored struct {
		name string
		dist int
	}
	var found []scored
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		// The flags help lists and no others: one it leaves out (`--dry-run` on
		// a command that only reads) is accepted, not offered, or the sentence
		// would send the person to a help screen that does not mention it.
		if f.Hidden || f.Name == "help" || f.Name == "version" || !listsFlag(cmd, f) {
			return
		}
		// Never the other side of what was typed: `--encode` is two letters
		// from --decode, and offering it sent the person to the one flag that
		// does the reverse of what they asked for (oppositeFlagHint answers it).
		if opposite(typed, f.Name) {
			return
		}
		switch d := match.Distance(typed, f.Name); {
		case len(typed) >= 3 && strings.HasPrefix(f.Name, typed):
			found = append(found, scored{f.Name, 0})
		case len(f.Name) >= 3 && strings.HasPrefix(typed, f.Name):
			found = append(found, scored{f.Name, 0})
		case d <= max(1, len([]rune(typed))/3) && len([]rune(typed)) >= 3:
			found = append(found, scored{f.Name, d})
		}
	})
	slices.SortStableFunc(found, func(a, b scored) int { return cmp.Or(a.dist-b.dist, strings.Compare(a.name, b.name)) })
	out := make([]string, 0, 3)
	for _, f := range found[:min(len(found), 3)] {
		out = append(out, "--"+f.name)
	}
	return out
}

// helpWordFlag is the one flag of cmd whose help names the word that was typed
// as a flag, when no spelling is near it: `rta gen token` answers with
// "bytes: 32" and takes the number as --length, whose help is "bytes of
// entropy", so `--bytes 16` was told only to read --help. A word in the help of
// two flags or more says nothing about which was meant, and a short one is too
// common to go by. "" when there is no such flag.
func helpWordFlag(cmd *cobra.Command, typed string) string {
	typed = strings.ToLower(typed)
	if len([]rune(typed)) < 4 {
		return ""
	}
	var found []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" || f.Name == "version" || !listsFlag(cmd, f) || opposite(typed, f.Name) {
			return
		}
		if slices.Contains(strings.FieldsFunc(strings.ToLower(f.Usage), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-'
		}), typed) {
			found = append(found, f.Name)
		}
	})
	if len(found) != 1 {
		return ""
	}
	return "--" + found[0]
}

// oppositePrefixes are the starts a word and its opposite differ by, the rest
// being the same: encode and decode, enable and disable, include and exclude,
// lock and unlock, activate and deactivate, color and no-color.
var oppositePrefixes = [][2]string{{"en", "de"}, {"en", "dis"}, {"in", "ex"}, {"", "un"}, {"", "de"}, {"", "no-"}}

// opposite says whether a and b are the two sides of one switch by how they
// start (oppositePrefixes), over a shared rest of three letters or more so that
// a short word is not matched by chance.
func opposite(a, b string) bool {
	for _, p := range oppositePrefixes {
		for _, pair := range [][2]string{p, {p[1], p[0]}} {
			if rest, ok := strings.CutPrefix(a, pair[0]); ok && len(rest) >= 3 && pair[1]+rest == b {
				return true
			}
		}
	}
	return false
}

// oppositeFlagHint answers a flag that is the other side of one of cmd's
// switches. `rta codec b64 --encode` was offered --decode, the nearest
// spelling and the one flag that does the reverse of what was asked: encoding
// is what the command does when --decode is left out, and the sentence says
// that. Only a switch that is off unless given has a side that is the
// command's own default. "" when typed is no such switch's opposite.
func oppositeFlagHint(cmd *cobra.Command, typed string) string {
	typed = strings.ToLower(typed)
	var hint string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if hint != "" || f.Hidden || f.Value.Type() != "bool" || f.DefValue != "false" || !listsFlag(cmd, f) {
			return
		}
		if opposite(typed, f.Name) {
			hint = "`--" + typed + "` is what `" + cmd.CommandPath() + "` does without --" + f.Name
		}
	})
	return hint
}

// unknownBeforeFlag is the refusal of a group's unknown word that a flag after
// it would otherwise have hidden. cobra parses a group's flags before it checks
// what is typed after the group, so `rta allow pg --ttl 1h` answered `unknown
// flag: --ttl` about a command that was never found: the flag was the symptom,
// and the word before it the mistake. nil when there is no such word, and for a
// group that takes a name (`rta lock claude --ttl 30m`), where the word is not a
// mistake.
func unknownBeforeFlag(cmd *cobra.Command) error {
	if !cmd.HasSubCommands() {
		return nil
	}
	if _, takesName := defaultVerbs[cmd.CommandPath()]; takesName {
		return nil
	}
	words := cmd.Flags().Args()
	if len(words) == 0 {
		return nil
	}
	return usageError(cmd, unknownCommand(cmd, words[0], words[1:]...))
}

// valueFlagsHint names the flags of cmd that take a value, for the refusal of
// an argument it has no place for. `rta fs hash f 5891…` is told there is an
// unexpected argument and shown a usage line with no flag in it; the person
// typed the value they meant to check against, and the flag that takes it,
// --expect, is what the sentence has to say. "" for a command with none.
func valueFlagsHint(cmd *cobra.Command) string {
	var names []string
	cmd.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" || f.Name == "profile" || f.Value.Type() == "bool" || f.Value.Type() == "count" {
			return
		}
		if _, credential := f.Annotations[annotCredential]; credential {
			return
		}
		names = append(names, "--"+f.Name)
	})
	if len(names) == 0 {
		return ""
	}
	more := ""
	if len(names) > 4 {
		names, more = names[:4], ", …"
	}
	return "its other values go behind a flag: " + strings.Join(names, ", ") + more +
		" — `" + cmd.CommandPath() + " --help` says which"
}

// plainWord is a word a shell passes through untouched, so a command line
// built from words that are all one can be shown for pasting without quoting.
var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)

// flagWordHint answers an argument that is the name of one of cmd's own flags:
// `rta codec hex decode 68656c6c6f` takes "decode" for the value, accepts it,
// and then refuses the hex string as the unexpected one. The person wrote the
// verb they meant, in the place the flag goes, and the sentence blamed the
// word they had right. The flag, with its dashes, is the answer; a command
// line is shown only when every word is plain enough to paste without quotes.
// "" when no argument is a flag's name.
func flagWordHint(cmd *cobra.Command) string {
	words := cmd.Flags().Args()
	flags := cmd.LocalNonPersistentFlags()
	for i, w := range words {
		f := flags.Lookup(w)
		if f == nil || f.Hidden || f.Name == "help" {
			continue
		}
		hint := "`" + w + "` is a flag, so it takes two dashes"
		fixed := slices.Clone(words)
		fixed[i] = "--" + w
		if !slices.ContainsFunc(fixed, func(s string) bool { return !plainWord.MatchString(s) }) {
			hint += ": `" + cmd.CommandPath() + " " + strings.Join(fixed, " ") + "`"
		}
		return hint
	}
	return ""
}

// verbsHint lists the commands of a group inline, so a person who typed the
// wrong one sees the right ones in the error and not behind another command. A
// group of sixteen verbs is a line, not a screen; past what a line holds the
// rest is left to --help.
func verbsHint(group *cobra.Command) string {
	const room = 100
	var names []string
	width := 0
	for _, sub := range group.Commands() {
		if !sub.IsAvailableCommand() {
			continue
		}
		if width += len(sub.Name()) + 2; width > room {
			names = append(names, "…")
			break
		}
		names = append(names, sub.Name())
	}
	return "`" + group.CommandPath() + "` takes " + strings.Join(names, ", ") + " — `" +
		group.CommandPath() + " --help` says what each does"
}
