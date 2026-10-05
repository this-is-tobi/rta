package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/builtin/audit"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `rta plugin ls` worked and `rta kv ls`, `rta note delete`, `rta plugin rm` and
// `rta grant rm` were "unknown command": three aliases that happened to exist,
// not a rule anyone could learn. The synonyms of a verb are the same command
// wherever it is in the tree.
func TestAVerbAnswersToItsSynonymsInEveryNamespace(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	for _, c := range []struct{ typed, is string }{
		{"kv ls", "rta kv list"},
		{"note ls", "rta note list"},
		{"grant ls", "rta grant list"},
		{"lock ls", "rta lock list"},
		{"plugin ls", "rta plugin list"},
		{"profile ls", "rta profile list"},
		{"note delete", "rta note rm"},
		{"note remove", "rta note rm"},
		{"kv delete", "rta kv rm"},
		{"lock remove", "rta lock rm"},
		{"plugin rm", "rta plugin remove"},
		{"plugin delete", "rta plugin remove"},
		{"profile delete", "rta profile rm"},
		{"grant rm", "rta grant revoke"},
		{"grant remove", "rta grant revoke"},
		{"profile get", "rta profile show"},
		{"policy get", "rta policy show"},
	} {
		cmd, rest, err := root.Find(strings.Fields(c.typed))
		if err != nil || len(rest) > 0 || cmd.CommandPath() != c.is {
			t.Errorf("`rta %s` is %v (rest %v, err %v), want %s", c.typed, cmd.CommandPath(), rest, err, c.is)
		}
	}
}

// A word that is already a different command beside its synonym stays that
// command: `kv show` is the entry without its value and `kv get` is the value.
// And a protocol's verbs are not synonyms — `http delete` is a request.
func TestASynonymNeverReplacesACommandThatIsAlreadyThere(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	for typed, is := range map[string]string{
		"kv show":     "rta kv show",
		"kv get":      "rta kv get",
		"http get":    "rta http get",
		"http delete": "rta http delete",
	} {
		cmd, _, err := root.Find(strings.Fields(typed))
		if err != nil || cmd.CommandPath() != is {
			t.Errorf("`rta %s` is %v (err %v), want %s", typed, cmd.CommandPath(), err, is)
		}
	}
	for _, typed := range []string{"http show", "http rm", "http ls"} {
		if cmd, rest, _ := root.Find(strings.Fields(typed)); cmd.CommandPath() != "rta http" || len(rest) == 0 {
			t.Errorf("`rta %s` found %s; an HTTP method has no synonym", typed, cmd.CommandPath())
		}
	}
}

// The aliases are hidden, so no help screen grows by them.
func TestAliasesAreNotListedInHelp(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	for _, path := range [][]string{{"kv"}, {"note"}, {"grant"}, {"plugin"}} {
		cmd, _, _ := root.Find(path)
		for _, alias := range []string{"ls", "delete"} {
			for _, line := range strings.Split(plainHelp(cmd, 100), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), alias+" ") {
					t.Errorf("`rta %s --help` lists %q: %s", strings.Join(path, " "), alias, line)
				}
			}
		}
	}
}

// `rta doctor` and `rta audit doctor` print the same report, so only the first
// is listed. The second still runs: the deny list `audit clients` derives and
// the hints other commands print name it.
func TestTheDuplicateDoctorIsNotListedButStillRuns(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	audit, _, _ := root.Find([]string{"audit"})
	if strings.Contains(plainHelp(audit, 100), "doctor") {
		t.Errorf("`rta audit --help` lists the second doctor:\n%s", plainHelp(audit, 100))
	}
	if !strings.Contains(plainHelp(root, 100), "doctor ") {
		t.Error("`rta --help` does not list doctor")
	}
	if cmd, _, err := root.Find([]string{"audit", "doctor"}); err != nil || cmd.CommandPath() != "rta audit doctor" {
		t.Errorf("`rta audit doctor` no longer resolves: %v %v", cmd.CommandPath(), err)
	}
}

// `rta lock claude` is `rta lock add claude`, flags and all — freezing is what
// a person who types a name after the noun means — and a typo of a verb is
// still a typo: `rta lock lsit` must not freeze a principal called lsit.
func TestTheLockNounTakesTheArgumentsOfAdd(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	short, _, err := run(t, reg, "lock", "claude", "--ttl", "30m", "--dry-run", "--no-color")
	if err != nil {
		t.Fatal(err)
	}
	long, _, err := run(t, reg, "lock", "add", "claude", "--ttl", "30m", "--dry-run", "--no-color")
	if err != nil {
		t.Fatal(err)
	}
	// The clock reads differently a minute apart; only what is frozen and for
	// how long is the same.
	if !strings.Contains(short, "would lock agent claude") || !strings.Contains(short, "lifting itself") ||
		strings.Split(short, "lifting itself")[0] != strings.Split(long, "lifting itself")[0] {
		t.Errorf("`lock claude` answered %q, `lock add claude` answered %q", short, long)
	}

	_, errOut, err := run(t, reg, "lock", "lsit", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), `unknown command "lsit"`) {
		t.Errorf("a typo of a verb froze something: err = %v, stderr = %q", err, errOut)
	}
	if _, _, err := run(t, reg, "lock", "a", "b", "--dry-run"); err == nil {
		t.Error("two names after the noun were accepted")
	}
	if out, _, err := run(t, reg, "lock"); err != nil || !strings.Contains(out, "USAGE") {
		t.Errorf("bare `rta lock` is not help: %q %v", out, err)
	}
	if cmd, _, _ := NewRoot(reg, "test").Find([]string{"lock", "list"}); cmd.CommandPath() != "rta lock list" {
		t.Errorf("`rta lock list` is %s, not the command that lists", cmd.CommandPath())
	}
}

// The deny list names a verb that only the person may run by its spelling, and
// a plugin can declare one. TestAVerbTheDenyListNamesAloneHasNoAlias reads the
// built-in catalogue; this is the same rule held to a plugin that mixes a
// human-only `show` with a `list` an agent may call: the list keeps `ls`, and
// the show gets no `get` that the harness's `Bash(rta mix thing show:*)` would
// not match.
func TestAHumanOnlyVerbOfAPluginHasNoAlias(t *testing.T) {
	reg := registry.New()
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	if err := reg.Register(plugin.Plugin{
		Name: "mix", Summary: "a plugin that mixes",
		Capabilities: []plugin.Capability{
			{ID: "mix.thing.list", Summary: "list things", Safety: plugin.Read, Run: run},
			{ID: "mix.thing.show", Summary: "show a thing", Safety: plugin.Read, HumanOnly: true, Run: run,
				Inputs: []plugin.Field{{Name: "id", Type: plugin.String, Positional: true, Required: true}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	list, show := commandAt(root, "rta mix thing list"), commandAt(root, "rta mix thing show")
	if list == nil || show == nil {
		t.Fatalf("the plugin's commands are not in the tree: %v %v", list, show)
	}
	if len(list.Aliases) == 0 {
		t.Error("the verb an agent may call lost its alias")
	}
	if len(show.Aliases) != 0 {
		t.Errorf("a human-only verb the deny list names alone answers to %v as well", show.Aliases)
	}
}

// A word that is a verb to whoever types it — look at the locks, lift one — is
// not a principal to freeze: `rta lock unlock` took effect and said "locked",
// which is a lock on a mistake. Each is refused and nothing is written, and the
// same word is a name when `lock add` is the one asked.
func TestAWordThatReadsAsAVerbIsNotFrozenAsAName(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"unlock", "status", "show", "info", "lift", "all"} {
		out, _, err := run(t, reg, "lock", word, "--no-color")
		var ve *view.Error
		if strings.Contains(out, "locked") {
			t.Errorf("`rta lock %s` froze something: %q", word, out)
		}
		if !errors.As(err, &ve) || ve.Code != CodeUsage || !strings.Contains(ve.Message, `unknown command "`+word+`"`) {
			t.Errorf("`rta lock %s` answered %v, want it refused as an unknown command", word, err)
			continue
		}
		if !strings.Contains(ve.Hint, "`rta lock") {
			t.Errorf("`rta lock %s` hints %q, want the command that means it or the verbs the noun has", word, ve.Hint)
		}
	}
	if out, _, err := run(t, reg, "lock", "add", "unlock", "--dry-run", "--no-color"); err != nil ||
		!strings.Contains(out, "would lock agent unlock") {
		t.Errorf("`lock add unlock` is a name when asked for as one: %q %v", out, err)
	}
}

// A flag of the verb with no name beside it is not a request for help: the
// verb says what it is missing, instead of an answer that ignores the flag, and
// says the same thing `rta lock add --ttl 30m` does. The name is not a required
// positional since `--all` stands in for it, so the refusal is the capability's
// own and names both ways out.
func TestAFlagOfTheVerbWithNoNameIsMissingTheName(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"lock", "--ttl", "30m"}, {"lock", "add", "--ttl", "30m"}} {
		_, _, err = run(t, reg, args...)
		var ve *view.Error
		if !errors.As(err, &ve) || ve.Code != "core.lock.name" || !strings.Contains(ve.Hint, "--all") {
			t.Errorf("`rta %s` answered %v, want the missing name and the way to take in every agent", strings.Join(args, " "), err)
		}
	}
	if out, _, err := run(t, reg, "lock", "-o", "json"); err != nil || !strings.Contains(out, "USAGE") {
		t.Errorf("a flag every command has turned bare `rta lock` into %q %v", out, err)
	}
}

// `show` leaves the value out and `get` is where it comes out, so `get` may stand
// for a show and a show may never stand for a get: a person who types `rta vault
// kv show` for a secret would be handed it by a word that promises not to.
func TestAShowNeverRevealsWhatAGetDoes(t *testing.T) {
	reg := registry.New()
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	key := []plugin.Field{{Name: "key", Type: plugin.String, Positional: true, Required: true}}
	if err := reg.Register(plugin.Plugin{
		Name: "vaultish", Summary: "a store with a value behind a get",
		Capabilities: []plugin.Capability{
			{ID: "vaultish.entry.get", Summary: "reveal the value", Safety: plugin.Write, Inputs: key, Run: run},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(plugin.Plugin{
		Name: "plain", Summary: "a store with a look and no value",
		Capabilities: []plugin.Capability{
			{ID: "plain.entry.show", Summary: "describe it", Safety: plugin.Read, Inputs: key, Run: run},
		},
	}); err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	if cmd, rest, _ := root.Find([]string{"vaultish", "entry", "show", "a"}); cmd.CommandPath() == "rta vaultish entry get" {
		t.Errorf("`rta vaultish entry show a` reaches the get that reveals the value (rest %v)", rest)
	}
	if cmd, _, err := root.Find([]string{"plain", "entry", "get", "a"}); err != nil || cmd.CommandPath() != "rta plain entry show" {
		t.Errorf("`get` for a show is %v (err %v), want rta plain entry show", cmd.CommandPath(), err)
	}
}

// The deny list `audit clients --fix` prints names a verb by its spelling —
// "Bash(rta keys add:*)" — and a string match is blind to a second spelling of
// the same command. A whole namespace is denied whole, so an alias inside it is
// covered; a verb that is denied by itself must have no alias at all, or the
// alias is a way round the harness's refusal. The day a verb of one of these
// is named rm, show or list, this is what says it cannot also answer to the
// other spellings.
func TestAVerbTheDenyListNamesAloneHasNoAlias(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	verbs := append(audit.HumanOnlyVerbs(reg.Capabilities), "plugin trust", "plugin allow", "plugin install")
	for _, verb := range verbs {
		cmd := commandAt(root, "rta "+verb)
		if cmd == nil {
			t.Errorf("the deny list names `rta %s`, which is not a command", verb)
			continue
		}
		if len(cmd.Aliases) > 0 {
			t.Errorf("`rta %s` is denied by name and answers to %v as well", verb, cmd.Aliases)
		}
	}
}
