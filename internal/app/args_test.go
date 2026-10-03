package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `--for` was read in the one branch that switches a profile on, and as unset
// whenever it was not positive: `rta use --for 2h` printed what was on and said
// nothing of the two hours, and `rta use staging --for -5m`, or `--for 0`,
// switched staging on with no deadline at all.
func TestAUseDeadlineThatWouldBeDroppedIsRefused(t *testing.T) {
	reg := testRegistry(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"use", "--for", "2h"}, "no profile was named"},
		{[]string{"use", "staging", "--for", "-5m"}, "end the switch before it began"},
		{[]string{"use", "staging", "--for", "0"}, "end the switch before it began"},
		{[]string{"use", "--off", "--for", "1h"}, "two different things"},
	} {
		_, _, err := run(t, reg, c.args...)
		var ve *view.Error
		if !errors.As(err, &ve) || ve.Code != CodeUsage || !strings.Contains(ve.Message, c.want) {
			t.Errorf("rta %s: %v, want a %s saying %q", strings.Join(c.args, " "), err, CodeUsage, c.want)
		}
	}
}

// The first things a person types at an rta they have just installed are
// `rta version` and the name of a service. Neither is a command: the first is
// `--version`, the second is a plugin. `rta pg query` was told that the
// closest matches were "fs", "kv" and "pkg" — two edits away from a two-letter
// word, which is the whole word — under a hint that said only to read --help.
func TestAnUnknownWordAtTheRootPointsAtWhereItMayLive(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	dataDir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dataDir)
	root := NewRoot(reg, "test")
	hintOf := func(word string) (message, hint string) {
		t.Helper()
		var ve *view.Error
		if !errors.As(unknownCommand(root, word), &ve) {
			return "", ""
		}
		return ve.Message, ve.Hint
	}

	if _, hint := hintOf("version"); !strings.Contains(hint, "`rta --version`") {
		t.Errorf("`rta version` was sent to %q", hint)
	}

	// A service, with no index attached: how to get the one it lives in.
	msg, hint := hintOf("vault")
	if !strings.Contains(hint, "`rta plugin index add official`") || !strings.Contains(hint, "`rta plugin install vault`") {
		t.Errorf("`rta vault` was sent to %q", hint)
	}
	if strings.Contains(msg, "closest") {
		t.Errorf("`rta vault` was offered neighbours: %q", msg)
	}

	// The word is no neighbour of fs, kv or pkg, and is not told it is.
	if err := unknownCommand(root, "pg"); strings.Contains(err.Error(), `"fs"`) || strings.Contains(err.Error(), `"kv"`) {
		t.Errorf("`rta pg` was offered unrelated names: %v", err)
	}
	// A four-letter service is not "use" with two letters changed.
	if msg, hint := hintOf("kube"); strings.Contains(msg, "closest") || !strings.Contains(hint, "`rta plugin install kube`") {
		t.Errorf("`rta kube` was offered %q and sent to %q", msg, hint)
	}

	// With an index attached that carries it, the exact command.
	manifest := "name: pg\nversion: 0.1.0\nsummary: PostgreSQL\nplatforms:\n  - os: linux\n    arch: amd64\n" +
		"    url: https://example.com/pg\n    sha256: " + strings.Repeat("a", 64) + "\ncapabilities:\n  - id: pg.status\n    safety: read\n"
	indexDir := filepath.Join(dataDir, "indexes", "lab", "index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(indexDir, "pg.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, hint := hintOf("pg"); !strings.Contains(hint, "plugin in the lab index") || !strings.Contains(hint, "`rta plugin install pg`") {
		t.Errorf("`rta pg` with an index that carries it was sent to %q", hint)
	}
	// An index that does not carry it has nothing better to say than the help.
	if _, hint := hintOf("vault"); hint != "" {
		t.Errorf("a word no attached index carries was sent to %q", hint)
	}

	// And a typo keeps its neighbour, a swapped pair of letters included.
	for word, want := range map[string]string{"sy": `"sys"`, "sysy": `"sys"`, "nte": `"note"`, "lcok": `"lock"`, "kvv": `"kv"`} {
		if err := unknownCommand(root, word); !strings.Contains(err.Error(), want) {
			t.Errorf("`rta %s` lost its suggestion %s: %v", word, want, err)
		}
	}
}

// A value a flag cannot hold was refused with the name of a standard-library
// function: `invalid argument "abc" for "--count" flag: strconv.ParseInt:
// parsing "abc": invalid syntax`. It is now the sentence the same mistake gets
// from a capability's input check, with the declared bounds when there are
// any, and a switch says that it takes no value.
func TestAFlagValueTheParserRefusesIsRefusedInTheWordsOfAnInputCheck(t *testing.T) {
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "knob", Summary: "has flags",
		Capabilities: []plugin.Capability{{
			ID: "knob.turn", Summary: "turn it", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "count", Type: plugin.Int, Min: 1, Max: 100, Default: 4, Help: "how many"},
				{Name: "ratio", Type: plugin.Float, Help: "how much"},
				{Name: "loud", Type: plugin.Bool, Help: "shout"},
			},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args        []string
		want, hint  string
		notContains string
	}{
		{[]string{"knob", "turn", "--count", "abc"},
			"`rta knob turn` takes a whole number from 1 to 100 for --count, not \"abc\"", "", "strconv"},
		{[]string{"knob", "turn", "--count=1.5"},
			"takes a whole number from 1 to 100 for --count, not \"1.5\"", "", "strconv"},
		{[]string{"knob", "turn", "--ratio", "x"},
			"takes a number for --ratio, not \"x\"", "", "strconv"},
		{[]string{"knob", "turn", "--loud=yes"},
			"takes true or false for --loud, not \"yes\"", "`--loud` alone turns it on", "strconv"},
		{[]string{"dashboard", "add", "sys.cpu", "--span", "abc"},
			"`rta dashboard add` takes a whole number for --span, not \"abc\"", "", "strconv"},
	} {
		_, _, err := run(t, reg, c.args...)
		if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), c.notContains) {
			t.Errorf("rta %s: err = %v, want it to say %q", strings.Join(c.args, " "), err, c.want)
			continue
		}
		var ve *view.Error
		if !errors.As(err, &ve) || ve.Code != CodeUsage || !strings.Contains(ve.Hint, c.hint) {
			t.Errorf("rta %s: %+v, want a %s with a hint holding %q", strings.Join(c.args, " "), err, CodeUsage, c.hint)
		}
	}
}

// cobra's "requires at least 1 arg(s), only received 0" named neither the
// thing missing nor the command to type.
func TestAMissingArgumentIsNamedWithTheLineToType(t *testing.T) {
	_, _, err := run(t, testRegistry(t), "demo", "item", "pick")
	if err == nil || !strings.Contains(err.Error(), "missing <name> — usage: rta demo item pick <name>") {
		t.Fatalf("err = %v", err)
	}
}

// The same refusal from a command written by hand. positionalArgsValidator
// fixed it for the capability commands and the rest kept cobra's count:
// `rta plugin install` with nothing after it said "accepts 1 arg(s), received
// 0" — a number, with the thing missing and the line to type left to --help.
func TestAHandWrittenCommandNamesTheArgumentItIsMissing(t *testing.T) {
	reg := testRegistry(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"mcp", "install"}, "missing <client> — usage: rta mcp install <client>"},
		{[]string{"plugin", "install"}, "missing <name | index/name> — usage: rta plugin install <name | index/name>"},
		{[]string{"profile", "set"}, "missing <profile> — usage: rta profile set <profile>"},
		{[]string{"dashboard", "add"}, "missing <capability> — usage: rta dashboard add <capability>"},
		{[]string{"explain", "sys.cpu", "extra"}, `unexpected argument "extra" — usage: rta explain [capability]`},
		{[]string{"profile", "rm", "a", "b", "c"}, `unexpected arguments "b", "c" — usage: rta profile rm <profile>`},
		// A command that takes nothing called its argument a command:
		// `unknown command "x" for "rta doctor"`.
		{[]string{"doctor", "x"}, `unexpected argument "x" — usage: rta doctor`},
		{[]string{"profile", "list", "x"}, `unexpected argument "x" — usage: rta profile list`},
		{[]string{"dashboard", "list", "x", "y"}, `unexpected arguments "x", "y" — usage: rta dashboard list`},
	} {
		_, _, err := run(t, reg, c.args...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("rta %s: err = %v, want it to say %q", strings.Join(c.args, " "), err, c.want)
		}
	}
}

// The other half of the same question, and it was answered by a sentence
// written for one case only: "one argument too many, %q" counted in a word
// and named args[len(fields)], so three extra arguments were reported as one,
// and the two the reader still had to find were not mentioned at all.
//
// A count that disagrees with the list beside it is the defect the counting
// vocabulary in pkg/format exists to stop, so the count comes from there —
// and what the reader actually needs is not the arithmetic but which words to
// delete, so every extra argument is named.
func TestEveryUnexpectedArgumentIsNamed(t *testing.T) {
	reg := testRegistry(t)
	_, _, err := run(t, reg, "demo", "item", "pick", "alpha", "extra")
	if err == nil || !strings.Contains(err.Error(), `unexpected argument "extra" — usage: rta demo item pick <name>`) {
		t.Fatalf("one extra: err = %v", err)
	}

	_, _, err = run(t, reg, "demo", "item", "pick", "alpha", "one", "two", "three")
	if err == nil {
		t.Fatal("three extra arguments were accepted")
	}
	if !strings.Contains(err.Error(), `unexpected arguments "one", "two", "three"`) {
		t.Fatalf("three extra: err = %v", err)
	}
}
