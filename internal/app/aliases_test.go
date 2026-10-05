package app

import (
	"strings"
	"testing"
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
