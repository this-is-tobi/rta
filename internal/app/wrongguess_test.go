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
		"uninstall pg":   "`rta plugin remove pg`",
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
	if ve := hintFor(t, "plugin", "uninstall", "pg"); !strings.Contains(ve.Hint, "`rta plugin remove pg`") {
		t.Errorf("`rta plugin uninstall pg` was told %q, not that it is plugin remove", ve.Hint)
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
