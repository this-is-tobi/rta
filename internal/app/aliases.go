package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/this-is-tobi/rta/builtin/audit"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// verbSynonyms are the verbs that mean the same thing to the person typing them.
//
// Aliases used to be three accidents — `plugin ls`, `profile remove` and
// `dashboard remove` — so `rta kv ls`, `rta note delete` and `rta plugin rm`
// answered "unknown command" while a neighbouring namespace took the same
// word. A person who learned one spelling had learned it for one namespace.
// Now it is a rule: a command named by any verb in a set also answers to the
// others, wherever it is in the tree, and a plugin's commands included, since
// the pass runs over the finished tree and none of them wrote for it.
//
// Aliases, hidden: help lists a command once, under its own name, so this does
// not make any screen longer. And never over a word that is already a different
// command beside it — `kv show` and `kv get` are two things, one with the value
// and one without, and neither is the other's alias.
var verbSynonyms = [][]string{
	{"list", "ls"},
	{"rm", "remove", "delete"},
	{"show", "get"},
}

// extraAliases are the spellings one command answers to beyond the sets above:
// the removal verb of a namespace that does not call it rm. A grant is taken
// back, so `rta grant rm` is what a person who knows `kv rm` types.
var extraAliases = map[string][]string{
	"rta grant revoke": {"rm", "remove", "delete"},
}

// protocolNamespaces use a verb as a name, not as a synonym: `http delete` is the
// request method, and `http show` is no request at all.
var protocolNamespaces = map[string]bool{"http": true}

// duplicates are commands that answer exactly what another does, hidden from
// the lists so there is one place to look. `rta audit doctor` and `rta doctor`
// print the same report; the capability stays registered, because the deny
// list `audit clients` derives and the hints other commands print name it, and
// is not shown.
var duplicates = map[string]bool{"rta audit doctor": true}

// defaultVerbs are the namespaces whose bare form takes the arguments of one of
// their verbs: freezing is what a person who types `rta lock claude` means.
var defaultVerbs = map[string]string{"rta lock": "add"}

// readAsVerbs are words that a person types after a noun to say what to do with
// it — to look, to undo, to do it harder — and never as the name of the thing
// the default verb acts on. `rta lock claude` freezes claude, so `rta lock
// unlock`, with nothing after it, would freeze a principal called unlock: a lock
// that took effect on a mistake and says "locked" the way it says it for the
// right one. They are refused as the unknown commands they are, with the verbs
// the noun has, so the slip costs a second line and not a stray lock somebody has
// to find in `lock list`. A name that really is one of these is still
// `rta lock add <name>`.
var readAsVerbs = map[string]bool{
	"show": true, "get": true, "status": true, "info": true, "view": true, "check": true,
	"inspect": true, "overview": true, "revoke": true, "unlock": true, "unfreeze": true,
	"unblock": true, "lift": true, "release": true, "clear": true, "freeze": true,
	"block": true, "ban": true, "kill": true, "stop": true, "all": true,
}

// shapeVerbs applies the four rules above to the finished tree. catalog is what
// the deny list is derived from.
//
// A verb the deny list names on its own — `audit doctor`, a plugin's human-only
// `show` beside verbs an agent may call — gets no alias. The harness matches
// that list by spelling (`Bash(rta myplugin show:*)`), a string match is blind
// to `rta myplugin get`, and an alias would be a way round a refusal the
// operator wrote down. A namespace denied whole covers its aliases and keeps
// them. Taken from the same derivation `audit clients --fix` prints, so a
// plugin's declaration is held to it as the built-in ones are, which a test
// over the built-in ones alone could not do.
func shapeVerbs(root *cobra.Command, catalog func() []plugin.Capability) {
	deniedAlone := map[string]bool{}
	for _, verb := range audit.HumanOnlyVerbs(catalog) {
		deniedAlone["rta "+verb] = true
	}
	addVerbAliases(root, deniedAlone)
	for path := range duplicates {
		if cmd := commandAt(root, path); cmd != nil {
			cmd.Hidden = true
		}
	}
	for path, verb := range defaultVerbs {
		makeDefaultVerb(commandAt(root, path), verb)
	}
}

func addVerbAliases(cmd *cobra.Command, deniedAlone map[string]bool) {
	for _, sub := range cmd.Commands() {
		addVerbAliases(sub, deniedAlone)
	}
	if !cmd.HasParent() || protocolNamespaces[cmd.Parent().Name()] || deniedAlone[cmd.CommandPath()] {
		return
	}
	for _, set := range verbSynonyms {
		if slices.Contains(set, cmd.Name()) {
			for _, verb := range set {
				claimVerb(cmd, verb)
			}
		}
	}
	for _, verb := range extraAliases[cmd.CommandPath()] {
		claimVerb(cmd, verb)
	}
}

// claimVerb makes verb an alias of cmd unless a sibling already answers to it.
func claimVerb(cmd *cobra.Command, verb string) {
	for _, sibling := range cmd.Parent().Commands() {
		if sibling.Name() == verb || slices.Contains(sibling.Aliases, verb) {
			return
		}
	}
	cmd.Aliases = append(cmd.Aliases, verb)
}

// commandAt is the command at path ("rta audit doctor"), or nil.
func commandAt(root *cobra.Command, path string) *cobra.Command {
	words := strings.Fields(path)
	if len(words) < 2 {
		return nil
	}
	cmd, rest, err := root.Find(words[1:])
	if err != nil || len(rest) > 0 || cmd.CommandPath() != path {
		return nil
	}
	return cmd
}

// makeDefaultVerb lets group take the arguments and flags of its verb, so
// `rta lock claude --ttl 30m` is `rta lock add claude --ttl 30m`.
//
// The verb's flags are declared on the group too, hidden — they have to be
// there for the line to parse, and the group's own help is a list of verbs, not
// of the one verb's flags. A word cobra did not take for a subcommand lands in
// the group's own run, and a word that is a typo of one (`rta lock lsit`) is
// refused as the unknown command it is rather than freezing a principal of that
// name: a lock only subtracts, but it would still be a lock nobody meant.
func makeDefaultVerb(group *cobra.Command, name string) {
	if group == nil {
		return
	}
	verb, _, err := group.Find([]string{name})
	if err != nil || verb == group || verb.RunE == nil {
		return
	}
	var carried []string
	verb.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if group.Flags().Lookup(f.Name) != nil {
			return
		}
		shared := *f
		shared.Hidden = true
		group.Flags().AddFlag(&shared)
		carried = append(carried, f.Name)
	})
	group.SuggestionsMinimumDistance = 2
	if places := useArgument.FindAllString(verb.Use, 1); len(places) == 1 {
		group.Use = group.Name() + " [" + strings.Trim(places[0], "<>[]") + "]"
		documentArgs(group, argDoc{strings.Trim(places[0], "<>[]"),
			fmt.Sprintf("what `%s` takes, so `%s x` is `%s x`", verb.CommandPath(), group.CommandPath(), verb.CommandPath())})
	}
	takes := verb.Args
	group.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 || takes == nil {
			return nil
		}
		return takes(cmd, args)
	}
	group.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			// A flag of the verb with no name beside it is a lock that was meant
			// and has nothing to act on, which the verb says; help for it would be
			// an answer that ignores what was typed.
			if !slices.ContainsFunc(carried, cmd.Flags().Changed) {
				return cmd.Help()
			}
			// Read now, not captured above: by this call the tree's argument
			// checks have been wrapped to answer in CodeUsage (codeUsageErrors).
			if err := verb.Args(verb, args); err != nil {
				return err
			}
		} else if readsAsAVerb(cmd, args[0]) {
			return usageError(cmd, unknownCommand(cmd, args[0], args[1:]...))
		}
		return verb.RunE(cmd, args)
	}
}

// readsAsAVerb says whether word, typed after a noun that takes a name, is a
// verb the noun does not have — a typo of one of its own, or a word from
// readAsVerbs — rather than a name.
func readsAsAVerb(group *cobra.Command, word string) bool {
	if readAsVerbs[strings.ToLower(word)] {
		return true
	}
	return len(plausibleSuggestions(word, group.SuggestionsFor(word))) > 0
}
