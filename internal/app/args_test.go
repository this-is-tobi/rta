package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `rta help nope` printed the root's help and exited 0, so a script asking for
// the help of a command that does not exist was told it had one. It is a usage
// error like `rta nope`, with the same neighbours, and a topic that exists is
// still its help.
func TestHelpForACommandThatDoesNotExistIsAUsageError(t *testing.T) {
	reg := testRegistry(t)
	_, _, err := run(t, reg, "help", "dem")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != CodeUsage || !strings.Contains(ve.Message, `unknown command "dem"`) ||
		!strings.Contains(ve.Message, `"demo"`) {
		t.Errorf("rta help dem: %v, want a usage error offering demo", err)
	}
	_, _, err = run(t, reg, "help", "demo", "nope")
	if !errors.As(err, &ve) || ve.Code != CodeUsage || !strings.Contains(ve.Message, `for "rta demo"`) {
		t.Errorf("rta help demo nope: %v, want the unknown command named under demo", err)
	}
	out, _, err := run(t, reg, "help", "demo", "item", "list")
	if err != nil || !strings.Contains(out, "rta demo item list") {
		t.Errorf("rta help demo item list = %q (%v), want that command's help", out, err)
	}
}

// A negative number or a signed duration given as a value is read as flags:
// `rta time at -90m` answered `unknown shorthand flag: '9' in -90m` under a hint
// to read --help, and nothing in it says that a `--` before the value is what
// makes it one. The capability's own description knew; the error did not.
func TestAValueThatStartsWithADashAndADigitSaysToPutDashDashBeforeIt(t *testing.T) {
	reg := testRegistry(t)
	for _, arg := range []string{"-5", "-90m", "-1.5"} {
		_, _, err := run(t, reg, "demo", "item", "list", arg)
		var ve *view.Error
		if !errors.As(err, &ve) || ve.Code != CodeUsage || !strings.Contains(ve.Hint, "`rta demo item list -- "+arg+"`") {
			t.Errorf("rta demo item list %s: %v, want a hint putting -- before it", arg, err)
		}
	}
	// A flag that does not exist keeps the ordinary hint.
	_, _, err := run(t, reg, "demo", "item", "list", "-x")
	var ve *view.Error
	if !errors.As(err, &ve) || strings.Contains(ve.Hint, " -- ") {
		t.Errorf("an unknown flag was told to use --: %v", err)
	}
	// And after the --, the value is taken as one.
	if _, _, err := run(t, reg, "demo", "item", "list", "--", "-90m"); err != nil {
		t.Errorf("a value after -- was refused: %v", err)
	}
}

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

// The step a plugin author skips after building is `rta plugin trust`, and the
// startup line that names it is printed by a pre-run an unknown word never
// reaches. `rta weather greet world` was then answered with the index the
// plugin was never in, while `rta doctor` — which does run it — said the
// artifact was installed and waiting.
func TestAWordNamingAPluginWaitingForApprovalSaysSo(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	prev := untrustedPluginsFound
	t.Cleanup(func() { untrustedPluginsFound = prev })
	SetUntrustedPlugins([]pluginhost.Untrusted{
		{Name: "weather", Path: "/home/you/.local/bin/rta-plugin-weather", Digest: strings.Repeat("a", 64)},
		{Name: "hello", Path: "/usr/local/bin/rta-plugin-hello", Digest: strings.Repeat("b", 64), Taken: true},
	})
	root := NewRoot(reg, "test")
	hintOf := func(word string) string {
		t.Helper()
		var ve *view.Error
		if !errors.As(unknownCommand(root, word), &ve) {
			return ""
		}
		return ve.Hint
	}

	hint := hintOf("weather")
	for _, want := range []string{"/home/you/.local/bin/rta-plugin-weather", "has not been approved", "`rta plugin trust weather`"} {
		if !strings.Contains(hint, want) {
			t.Errorf("`rta weather` was sent to %q, which lacks %q", hint, want)
		}
	}
	if strings.Contains(hint, "index") {
		t.Errorf("`rta weather` was sent to an index for a plugin that is already here: %q", hint)
	}
	// One something else already answers to is not waiting on an approval:
	// approving it would earn a collision on the next start.
	if hint := hintOf("hello"); strings.Contains(hint, "plugin trust") {
		t.Errorf("a plugin whose name is taken was offered `rta plugin trust`: %q", hint)
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

// A flag the command line cannot leave out read as optional everywhere it was
// described: `rta explain` drew it in square brackets, as every optional flag
// is, and --help gave it the same line as the ones beside it, so the first the
// person heard that --data was required was the refusal for leaving it off.
// One that config can fill is the exception, because the command line may
// leave it off, and stays bracketed.
func TestARequiredFlagIsSaidToBeRequiredWhereItIsDescribed(t *testing.T) {
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "knob", Summary: "has flags",
		Capabilities: []plugin.Capability{{
			ID: "knob.turn", Summary: "turn it", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "by", Type: plugin.String, Required: true, Help: "which way"},
				{Name: "host", Type: plugin.String, Required: true, Config: "host", Help: "where"},
				{Name: "token", Type: plugin.Secret, Required: true, Local: true, EnvFallback: true, Help: "who you are"},
				{Name: "loud", Type: plugin.Bool, Help: "shout"},
			},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		}},
	}); err != nil {
		t.Fatal(err)
	}
	capability, ok := reg.Capability("knob.turn")
	if !ok {
		t.Fatal("knob.turn is not registered")
	}
	if got, want := cliForm(capability), "rta knob turn --by <string> [--host <string>] [--token <secret>] [--loud <bool>]"; got != want {
		t.Errorf("the explain card shows %q, want %q", got, want)
	}
	out, _, err := run(t, reg, "knob", "turn", "--help")
	if err != nil {
		t.Fatal(err)
	}
	// Lower-cased: help starts a description with a capital, which is not what
	// this is about.
	lower := strings.ToLower(out)
	for _, want := range []string{"which way (required)", "where"} {
		if !strings.Contains(lower, want) {
			t.Errorf("--help does not say %q:\n%s", want, out)
		}
	}
	for _, notRequired := range []string{"where (required)", "who you are (required)"} {
		if strings.Contains(lower, notRequired) {
			t.Errorf("--help calls a flag something other than the line can fill required on it: %q\n%s", notRequired, out)
		}
	}
}

// The environment is the other thing besides config that can fill an input the
// line leaves off: a credential declared Local and EnvFallback is read from
// RTA_<PLUGIN>_<INPUT>, and a profile's `secrets:` fills it too. Marking it
// required for cobra refused the call before either was looked at, so the
// hint that says "or export $RTA_KNOB_TOKEN" sent a person to a variable that
// did nothing.
func TestARequiredCredentialTheEnvironmentFillsNeedsNoFlag(t *testing.T) {
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "knob", Summary: "has a credential",
		Capabilities: []plugin.Capability{{
			ID: "knob.turn", Summary: "turn it", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "token", Type: plugin.Secret, Required: true, Local: true, EnvFallback: true, Help: "who you are"},
			},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return view.Text{Body: "turned with " + req.String("token")}, nil
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(plugin.LocalEnvVar("knob.turn", "token"), "from-env")
	out, _, err := run(t, reg, "knob", "turn")
	if err != nil {
		t.Fatalf("the credential is exported and the call was refused: %v", err)
	}
	if !strings.Contains(out, "turned with from-env") {
		t.Errorf("the handler did not read the exported credential:\n%s", out)
	}
}
