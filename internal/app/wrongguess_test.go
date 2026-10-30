package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// hintFor runs args against the real tree and returns the coded refusal it ends
// in.
func hintFor(t *testing.T, args ...string) *view.Error {
	t.Helper()
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = run(t, reg, args...)
	var ve *view.Error
	if !errors.As(err, &ve) {
		t.Fatalf("`rta %s` ended in %v, not a coded refusal", strings.Join(args, " "), err)
	}
	if ve.Code != CodeUsage {
		t.Fatalf("`rta %s` was refused as %s, not as a usage mistake: %v", strings.Join(args, " "), ve.Code, ve)
	}
	return ve
}

// A word typed at the prompt is the task, in the words the person thinks it in.
// Every one of these was answered with "unknown command" and a pointer at
// installing a plugin of that name; each is a command that exists, one noun
// deeper, or a settings word naming the command that holds the setting.
func TestAWordNamedSomewhereElseInTheTreeIsAnsweredWithWhereItIs(t *testing.T) {
	for line, want := range map[string]string{
		"revoke":         "`rta grant revoke`",
		"ps":             "`rta sys ps`",
		"uuid":           "`rta gen uuid`",
		"ping":           "`rta net ping`",
		"dns":            "`rta net dns`",
		"disk":           "`rta sys disk`",
		"install pg":     "`rta plugin install pg`",
		"uninstall pg":   "`rta plugin rm pg`",
		"status":         "`rta agent overview`",
		"log":            "`rta agent log`",
		"history":        "`rta agent log`",
		"freeze claude":  "`rta lock add claude`",
		"unlock claude":  "`rta lock rm claude`",
		"allow pg":       "`rta grant allow pg`",
		"settings":       "`rta config`",
		"theme":          "`rta config`",
		"colors":         "`rta config`",
		"cfg":            "`rta config`",
		"tiles":          "`rta dashboard`",
		"connect claude": "`rta mcp install claude`",
		"setup":          "`rta init`",
		"passwords":      "`rta gen password`",
		"hosts":          "`rta net hosts`",
		"uuidd":          "`rta gen uuid`",
		"certificate":    "`rta cert`",
		"now":            "`rta time at`",
		"date":           "`rta time at`",
		"clock":          "`rta time at`",
	} {
		ve := hintFor(t, strings.Fields(line)...)
		if !strings.Contains(ve.Hint, want) {
			t.Errorf("`rta %s` was told %q, which does not name %s", line, ve.Hint, want)
		}
		if strings.Contains(ve.Message, "closest match") {
			t.Errorf("`rta %s` was also told a neighbour: %q", line, ve.Message)
		}
	}
}

// A verb is never a service, and is not told it might be: the plugin sentence
// belongs to a word that names nothing in the tree.
func TestAVerbIsNotToldItMightBeAService(t *testing.T) {
	for _, word := range []string{"revoke", "ps", "uuid", "status", "freeze"} {
		if ve := hintFor(t, word); strings.Contains(ve.Hint, "plugin") {
			t.Errorf("`rta %s` was sent to a plugin: %q", word, ve.Hint)
		}
	}
	// A group's name may be one: `rta audit kube` is a command, and kube is a
	// service of its own.
	ve := hintFor(t, "kube")
	if !strings.Contains(ve.Hint, "`rta audit kube`") || !strings.Contains(ve.Hint, "`rta plugin install kube`") {
		t.Errorf("`rta kube` was told %q, which has to name both readings", ve.Hint)
	}
}

// A synonym the operator's vocabulary gives a command is not a service either:
// `rta theme` is `rta config`, and the plugin sentence beside it is a second
// reading nobody had. A group's own name keeps both (above).
func TestASynonymOfACommandIsNotToldItMightBeAService(t *testing.T) {
	for _, word := range []string{"theme", "settings", "cfg", "colors", "tiles"} {
		ve := hintFor(t, word)
		if !strings.Contains(ve.Hint, "did you mean") || strings.Contains(ve.Hint, "plugin") {
			t.Errorf("`rta %s` was told %q", word, ve.Hint)
		}
	}
	if ve := hintFor(t, "plugin", "uninstall", "pg"); !strings.Contains(ve.Hint, "`rta plugin rm pg`") {
		t.Errorf("`rta plugin uninstall pg` was told %q, not that it is plugin rm", ve.Hint)
	}
}

// A word that only begins like a command is another word: `postgres` is a
// service to install, not `http post`, and `login` is not `agent log`.
func TestAWordThatOnlyBeginsLikeACommandIsNotOfferedIt(t *testing.T) {
	for word, not := range map[string]string{"postgres": "rta http post", "login": "rta agent log"} {
		ve := hintFor(t, word)
		if strings.Contains(ve.Hint, not) || strings.Contains(ve.Message, "closest") {
			t.Errorf("`rta %s` was offered %q: %s / %s", word, not, ve.Message, ve.Hint)
		}
	}
	if ve := hintFor(t, "postgres"); !strings.Contains(ve.Hint, "`rta plugin install pg`") {
		t.Errorf("`rta postgres` was told %q, which does not say it is the pg plugin", ve.Hint)
	}
}

// A word that reverses a command is not a typo of it: `rta mcp uninstall` was
// told the closest match was install, which does the opposite of what it asks.
func TestAWordThatUndoesACommandIsNotOfferedIt(t *testing.T) {
	for _, line := range []string{"mcp uninstall", "kv unset", "net hosts unadd"} {
		ve := hintFor(t, strings.Fields(line)...)
		if strings.Contains(ve.Message, "closest") {
			t.Errorf("`rta %s` was told %q", line, ve.Message)
		}
	}
	if ve := hintFor(t, "mcp", "instal"); !strings.Contains(ve.Message, `"install"`) {
		t.Errorf("a typo of install was told %q", ve.Message)
	}
}

// What the tables name has to exist, or a hint points at nothing.
func TestTheSuggestionTablesNameRealCommands(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	for path := range commandKeywords {
		if commandAt(root, path) == nil {
			t.Errorf("commandKeywords names %q, which is not a command", path)
		}
	}
	for word, path := range firstChoice {
		if commandAt(root, path) == nil {
			t.Errorf("firstChoice sends %q to %q, which is not a command", word, path)
		}
	}
}

// cobra parses a group's flags before it looks at what follows the group, so a
// flag after the wrong word answered `unknown flag: --ttl` about a command that
// was never found. The word is the mistake and is reported first.
func TestAnUnknownCommandIsReportedBeforeAnUnknownFlagAfterIt(t *testing.T) {
	ve := hintFor(t, "allow", "pg", "--ttl", "1h")
	if !strings.Contains(ve.Message, `unknown command "allow"`) || !strings.Contains(ve.Hint, "`rta grant allow pg`") {
		t.Errorf("`rta allow pg --ttl 1h` was told %q / %q", ve.Message, ve.Hint)
	}
	ve = hintFor(t, "kv", "sett", "--zzz")
	if !strings.Contains(ve.Message, `unknown command "sett"`) {
		t.Errorf("`rta kv sett --zzz` was told %q", ve.Message)
	}
	// A group that takes a name has no unknown word to report.
	ve = hintFor(t, "lock", "claude", "--zzz")
	if !strings.Contains(ve.Message, "unknown flag") {
		t.Errorf("`rta lock claude --zzz` was told %q", ve.Message)
	}
	// And a flag with no word before it is what it says it is.
	ve = hintFor(t, "--zzz")
	if !strings.Contains(ve.Message, "unknown flag") {
		t.Errorf("`rta --zzz` was told %q", ve.Message)
	}
}

func TestAMistypedFlagIsAnsweredWithTheNearestOne(t *testing.T) {
	for line, want := range map[string]string{
		"sys cpu --core":            "--cores",
		"grant allow kv.get --agnt": "--agent",
		"gen password --lengh 20":   "--length",
		"agent log --refuse":        "--refused",
		"agent log --lim 5":         "--limit",
	} {
		ve := hintFor(t, strings.Fields(line)...)
		if !strings.Contains(ve.Hint, "did you mean") || !strings.Contains(ve.Hint, want) {
			t.Errorf("`rta %s` was told %q, which does not offer %s", line, ve.Hint, want)
		}
	}
	// Nothing near it: the old pointer at --help.
	if ve := hintFor(t, "sys", "cpu", "--zzzzzz"); strings.Contains(ve.Hint, "did you mean") {
		t.Errorf("a flag like nothing was offered %q", ve.Hint)
	}
}

// The other side of a switch is told what leaving the switch out does. `rta
// codec b64 --encode` was offered --decode, two letters away and the one flag
// that does the reverse of what was asked.
func TestTheOppositeOfASwitchIsToldWhatLeavingItOutDoes(t *testing.T) {
	for line, want := range map[string]string{
		"codec b64 hello --encode": "`--encode` is what `rta codec b64` does without --decode",
		"codec url hello --encode": "`--encode` is what `rta codec url` does without --decode",
		"sys cpu --color":          "`--color` is what `rta sys cpu` does without --no-color",
	} {
		ve := hintFor(t, strings.Fields(line)...)
		if !strings.Contains(ve.Hint, want) || strings.Contains(ve.Hint, "did you mean") {
			t.Errorf("`rta %s` was told %q", line, ve.Hint)
		}
	}
	for _, pair := range [][2]string{{"encode", "decode"}, {"decrypt", "encrypt"}, {"enable", "disable"},
		{"include", "exclude"}, {"lock", "unlock"}, {"unwrap", "wrap"}, {"color", "no-color"}} {
		if !opposite(pair[0], pair[1]) {
			t.Errorf("%s and %s are not read as opposites", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{{"decode", "decode"}, {"in", "ex"}, {"index", "exdent"}, {"cores", "core"}, {"limit", "unlimit-x"}} {
		if opposite(pair[0], pair[1]) {
			t.Errorf("%s and %s are read as opposites", pair[0], pair[1])
		}
	}
}

// The other side of a verb is not its near miss either: `rta mcp uninstall
// claude` was offered `rta mcp install claude`. A command that names the word
// among its keywords still is the answer, which is how `plugin uninstall`
// finds remove.
func TestTheOppositeOfACommandIsNotOffered(t *testing.T) {
	if ve := hintFor(t, "mcp", "uninstall", "claude"); strings.Contains(ve.Hint, "install claude") {
		t.Errorf("`rta mcp uninstall claude` was offered %q", ve.Hint)
	}
	if ve := hintFor(t, "plugin", "uninstall", "pg"); !strings.Contains(ve.Hint, "`rta plugin rm pg`") {
		t.Errorf("`rta plugin uninstall pg` was told %q, want plugin rm", ve.Hint)
	}
	if !opposite("deactivate", "activate") || !opposite("uninstall", "install") {
		t.Error("de- and un- are not read as reversing the word")
	}
}

// A flag named by a word of another flag's help is offered that flag: `rta gen
// token` reports its size as "bytes" and takes it as --length, "bytes of
// entropy". A word in the help of several flags names none of them.
func TestAFlagNamedByAWordOfAnothersHelpIsOfferedIt(t *testing.T) {
	ve := hintFor(t, "gen", "token", "--bytes", "16")
	if !strings.Contains(ve.Hint, "did you mean --length (bytes of entropy)?") {
		t.Errorf("`rta gen token --bytes 16` was told %q", ve.Hint)
	}
	if ve := hintFor(t, "sys", "cpu", "--zzzzzz"); strings.Contains(ve.Hint, "did you mean") {
		t.Errorf("a flag like nothing was offered %q", ve.Hint)
	}
}

// Only a flag the help lists is offered: `--dry-run` is accepted by a command
// that only reads and listed by none, so `sys cpu --dryrun` is not sent to a
// help screen that does not mention it.
func TestAFlagTheHelpDoesNotListIsNotOffered(t *testing.T) {
	if ve := hintFor(t, "sys", "cpu", "--dryrun"); strings.Contains(ve.Hint, "--dry-run") {
		t.Errorf("a command that only reads was offered %q", ve.Hint)
	}
	if ve := hintFor(t, "note", "rm", "1", "--dryrun"); !strings.Contains(ve.Hint, "--dry-run") {
		t.Errorf("a command that writes was told %q, which does not offer --dry-run", ve.Hint)
	}
}

// `rta fs hash f 5891…` was told there was an unexpected argument and shown a
// usage line with no flag in it. The person typed the value they meant to check
// against; the flag that takes it is the answer.
func TestAnUnexpectedArgumentNamesTheFlagsThatTakeAValue(t *testing.T) {
	for line, want := range map[string]string{
		"fs hash f 5891abcd":           "--expect",
		"net port localhost 22,80,443": "--ports",
		"gen password 32":              "--length",
		"sys ps mem":                   "--sort",
	} {
		ve := hintFor(t, strings.Fields(line)...)
		if !strings.Contains(ve.Message, "unexpected argument") || !strings.Contains(ve.Hint, want) {
			t.Errorf("`rta %s` was told %q / %q, which does not name %s", line, ve.Message, ve.Hint, want)
		}
	}
	// A command whose flags are all switches has nothing to name.
	if ve := hintFor(t, "doctor", "x"); strings.Contains(ve.Hint, "behind a flag") {
		t.Errorf("a command with no value flag was told %q", ve.Hint)
	}
	// A credential is not a value to suggest on the command line: --passphrase
	// is accepted, listed in --help, and kept out of a sentence that is read as
	// advice by somebody who typed one word too many.
	ve := hintFor(t, "kv", "get", "a", "b", "c")
	if !strings.Contains(ve.Hint, "--out") || strings.Contains(ve.Hint, "--passphrase") {
		t.Errorf("`rta kv get a b c` was told %q, which has to name --out and leave the credential flag out", ve.Hint)
	}
}

// `rta codec hex decode 68656c6c6f` took "decode" for the value and refused the
// hex string, so the sentence blamed the word the person had right. The verb
// they wrote is a flag of the command, and the hint says so with the line to
// run.
func TestAnArgumentThatIsAFlagsNameIsToldItTakesTwoDashes(t *testing.T) {
	for line, want := range map[string]string{
		"codec hex decode 68656c6c6f": "`decode` is a flag, so it takes two dashes: `rta codec hex --decode 68656c6c6f`",
		"codec b64 decode aGVsbG8=":   "`rta codec b64 --decode aGVsbG8=`",
		"fs hash f expect 5891abcd":   "`rta fs hash f --expect 5891abcd`",
	} {
		ve := hintFor(t, strings.Fields(line)...)
		if !strings.Contains(ve.Hint, want) {
			t.Errorf("`rta %s` was told %q, which does not say %s", line, ve.Hint, want)
		}
	}
	// A word that is not a flag's name keeps the answer it had.
	if ve := hintFor(t, "fs", "hash", "f", "5891abcd"); !strings.Contains(ve.Hint, "--expect") || strings.Contains(ve.Hint, "two dashes") {
		t.Errorf("`rta fs hash f 5891abcd` was told %q", ve.Hint)
	}
	// A word a shell would change is named, and no line is built from it.
	if ve := hintFor(t, "codec", "hex", "decode", "a b", "c"); !strings.Contains(ve.Hint, "`decode` is a flag") || strings.Contains(ve.Hint, "rta codec hex --decode") {
		t.Errorf("a word with a space was shown in a pasteable line: %q", ve.Hint)
	}
}

// "lists its commands" sent the person to another command to read the names of
// the first: inside a namespace they are the answer.
func TestAWrongVerbInANamespaceListsTheVerbsInline(t *testing.T) {
	ve := hintFor(t, "kv", "frobnicate")
	for _, verb := range []string{"get", "set", "list", "rm"} {
		if !strings.Contains(ve.Hint, verb) {
			t.Errorf("`rta kv frobnicate` was told %q, which does not list %s", ve.Hint, verb)
		}
	}
	if ve := hintFor(t, "Frob_nicate"); !strings.Contains(ve.Hint, "`rta --help` lists its commands") {
		t.Errorf("the root is forty commands and keeps its pointer at --help: %q", ve.Hint)
	}
}

// `rta help security` asks about a subject, not for a service: it is told what
// names the subject and where the documentation is, and never that it might be
// a plugin to install. Nor is a subject a typo: `getting-started` has no
// business being offered `kv get`.
func TestAHelpSubjectPointsAtTheDocumentation(t *testing.T) {
	for _, topic := range []string{"security", "getting-started", "concepts"} {
		ve := hintFor(t, "help", topic)
		if !strings.Contains(ve.Hint, docsURL) {
			t.Errorf("`rta help %s` was told %q, which does not say where the documentation is", topic, ve.Hint)
		}
		if strings.Contains(ve.Hint, "plugin") || strings.Contains(ve.Hint, "kv get") {
			t.Errorf("`rta help %s` was told %q", topic, ve.Hint)
		}
	}
	if ve := hintFor(t, "help", "revoke"); !strings.Contains(ve.Hint, "`rta grant revoke`") {
		t.Errorf("`rta help revoke` was told %q", ve.Hint)
	}
}

func TestAnUnknownProfileSaysHowToCreateOne(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = run(t, reg, "use", "staging", "--for", "2h")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "core.profile.unknown" {
		t.Fatalf("`rta use staging` ended in %v", err)
	}
	if !strings.Contains(ve.Hint, "`rta profile set staging --plugin") {
		t.Errorf("hint = %q, which does not say how to create the profile", ve.Hint)
	}
}

// An empty word is an unset variable in a script, not a misspelling of every
// command there is: the refusal names the verbs in its hint and lists none of
// them as the closest matches.
func TestAnEmptyWordIsNotNearEveryCommand(t *testing.T) {
	for _, args := range [][]string{{""}, {"sys", ""}, {"kv", " "}} {
		ve := hintFor(t, args...)
		if strings.Contains(ve.Message, "closest") {
			t.Errorf("`rta %q` says %q, which offers every command as a near miss", args, ve.Message)
		}
		if ve.Hint == "" {
			t.Errorf("`rta %q` has no hint", args)
		}
	}
}

// `rta mysql status --tls` was told "flag needs an argument" and to read --help,
// for a flag whose five values the capability declares. They are the answer.
func TestAFlagGivenNoValueNamesTheOptionsItTakes(t *testing.T) {
	ve := hintFor(t, "net", "dns", "example.org", "--type")
	if !strings.Contains(ve.Message, "flag needs an argument: --type") || !strings.Contains(ve.Hint, "--type takes one of") ||
		!strings.Contains(ve.Hint, "AAAA") {
		t.Errorf("`rta net dns example.org --type` was told %q / %q, which does not list the record types", ve.Message, ve.Hint)
	}
	// A flag with no declared options keeps the answer it had.
	if ve := hintFor(t, "net", "dns", "example.org", "--server"); strings.Contains(ve.Hint, "takes one of") {
		t.Errorf("a free-text flag was told it takes one of a list: %q", ve.Hint)
	}
}

// `ls` is the spelling of `list` that every shell taught, and a command answers
// to it wherever there is one (verbSynonyms). At the root, where `list` is told
// it belongs to many commands, `ls` was told its nearest neighbour was `fs`.
func TestLsAtTheRootIsAnsweredLikeList(t *testing.T) {
	list, ls := hintFor(t, "list"), hintFor(t, "ls")
	if !strings.Contains(ls.Hint, "`ls` is under many commands") {
		t.Errorf("`rta ls` was told %q, not that it is under many commands", ls.Hint)
	}
	if ls.Message != `unknown command "ls" for "rta"` || strings.Contains(ls.Message, "closest") {
		t.Errorf("`rta ls` was also told a neighbour: %q", ls.Message)
	}
	if got, want := strings.ReplaceAll(ls.Hint, "`ls`", "`list`"), list.Hint; got != want {
		t.Errorf("`rta ls` was told %q, `rta list` %q", ls.Hint, list.Hint)
	}
}

// A person who wants the time types the word for it, and `rta explain` finds
// the capability by the same word.
func TestTheWordsForTheTimeFindTimeAt(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"now", "date", "clock"} {
		_, _, err = run(t, reg, "explain", word)
		var ve *view.Error
		if !errors.As(err, &ve) || !strings.Contains(ve.Hint, "time.at") {
			t.Errorf("`rta explain %s` ended in %v, which does not offer time.at", word, err)
		}
	}
}
