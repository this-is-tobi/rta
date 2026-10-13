package app

import (
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// `--set` states a default the caller can override; `--only` states the list a
// call cannot go outside. Both are written by a script, so both are held to the
// declaration before they are written, and the second states a boundary — the
// one block where a key quietly left behind is a wall nobody remembers writing.

func TestProfileSetWritesAFenceTypedAgainstTheDeclaration(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("profile", "set", "staging", "--plugin", "db",
		"--only", "dbname=app", "--only", "dbname=app-jobs",
		"--only", "port=5432", "--only", "port=5433"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	cfg, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	only := cfg.Profiles["staging"].Plugins["db"].Only
	// Whole numbers, not "5432": a flag is always text and the declaration is
	// what decides, as it does for `--set`.
	ports, ok := only["port"].([]any)
	if !ok || len(ports) != 2 {
		t.Fatalf("only.port = %#v", only["port"])
	}
	if strings.Contains(configOf(t), `"5432"`) {
		t.Errorf("a port was written as text:\n%s", configOf(t))
	}
	if first, _ := plugin.FenceList(only["port"]); len(first) != 2 {
		t.Errorf("only.port is not a list of two: %#v", only["port"])
	} else if !plugin.FenceAllows(plugin.Field{Type: plugin.Int}, first, 5433) ||
		plugin.FenceAllows(plugin.Field{Type: plugin.Int}, first, 80) {
		t.Errorf("the stored list does not hold exactly the two ports: %#v", first)
	}
	names, _ := only["dbname"].([]any)
	if len(names) != 2 || names[0] != "app" || names[1] != "app-jobs" {
		t.Errorf("only.dbname = %#v", only["dbname"])
	}
}

func TestProfileSetRefusesWhatAFenceCannotHold(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code string
	}{
		{"a credential", []string{"--only", "password=hunter2"}, "core.profile.only.secret"},
		{"a number outside the range", []string{"--only", "port=70000"}, "core.profile.only.range"},
		{"text where a number is declared", []string{"--only", "port=high"}, "core.profile.only.type"},
		{"an option nothing accepts", []string{"--only", "sslmode=never"}, "core.profile.only.option"},
		{"no value", []string{"--only", "dbname="}, "core.profile.only.empty"},
		{"no pair", []string{"--only", "dbname"}, "core.profile.only.shape"},
		{"a key nothing reads", []string{"--only", "dbnaem=app"}, "core.profile.unusable"},
		{"both ways at once", []string{"--only", "dbname=app", "--unfence"}, "core.profile.only.both"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := session(t, setRegistry(t))
			_, errOut, err := run(append([]string{"profile", "set", "staging", "--plugin", "db"}, tc.args...)...)
			if err == nil || !strings.Contains(errOut, tc.code) {
				t.Fatalf("err = %v, stderr = %q, want %s", err, errOut, tc.code)
			}
			for _, echoed := range []string{"hunter2", "70000", "never"} {
				if strings.Contains(errOut, echoed) {
					t.Errorf("the refusal repeats %q:\n%s", echoed, errOut)
				}
			}
			if _, statErr := os.Stat(config.Path()); statErr == nil {
				t.Errorf("a refused run wrote the file:\n%s", configOf(t))
			}
		})
	}
}

// A `--set` the fence would refuse on every call is refused before it is
// written, in the sentence the loader uses — one rule, not one for the writer
// and another for the reader.
func TestProfileSetRefusesADefaultOutsideTheFenceItStates(t *testing.T) {
	run := session(t, setRegistry(t))
	_, errOut, err := run("profile", "set", "staging", "--plugin", "db",
		"--set", "dbname=prod", "--only", "dbname=app")
	if err == nil || !strings.Contains(errOut, "`set: dbname` is not one of the values `only: dbname` allows") {
		t.Fatalf("err = %v, stderr = %q", err, errOut)
	}
	if strings.Contains(errOut, "prod") {
		t.Errorf("the refusal repeats the value:\n%s", errOut)
	}
	// And against the fence that is already stored, when only `--set` is restated.
	if _, errOut, err := run("profile", "set", "staging", "--plugin", "db", "--only", "dbname=app"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	_, errOut, err = run("profile", "set", "staging", "--plugin", "db", "--set", "dbname=prod")
	if err == nil || !strings.Contains(errOut, "is not one of the values `only: dbname` allows") {
		t.Fatalf("a default outside the stored fence was written: err = %v, stderr = %q\n%s", err, errOut, configOf(t))
	}
}

// Stated whole, like `--set`: a restatement drops what it leaves out and says
// so, and `--unfence` is the way to take every fence off.
func TestProfileSetOnlyStatesTheWholeBlockAndUnfenceRemovesIt(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("profile", "set", "staging", "--plugin", "db",
		"--only", "dbname=app", "--only", "sslmode=require"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	out, errOut, err := run("profile", "set", "staging", "--plugin", "db", "--only", "dbname=app-jobs")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "unfenced") || !strings.Contains(out, "only.sslmode") {
		t.Errorf("a restatement that dropped a fence did not say a fence came off:\n%s", out)
	}
	if strings.Contains(out, "dropped") {
		t.Errorf("a fence that came off was reported as a default that was dropped:\n%s", out)
	}
	if got := configOf(t); strings.Contains(got, "sslmode") || !strings.Contains(got, "app-jobs") {
		t.Errorf("the restated block was not written whole:\n%s", got)
	}
	// A run that states no fence leaves the stored one alone.
	if _, errOut, err := run("profile", "set", "staging", "--plugin", "db", "--set", "host=db.internal"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(configOf(t), "app-jobs") {
		t.Errorf("a run that stated no fence removed the stored one:\n%s", configOf(t))
	}
	out, errOut, err = run("profile", "set", "staging", "--plugin", "db", "--unfence")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "unfenced") || !strings.Contains(out, "only.dbname") {
		t.Errorf("removing a fence did not say what it removed:\n%s", out)
	}
	if strings.Contains(configOf(t), "only") {
		t.Errorf("--unfence left a fence behind:\n%s", configOf(t))
	}
	out, _, err = run("profile", "set", "staging", "--plugin", "db", "--unfence")
	if err != nil || !strings.Contains(out, "already reads this way") {
		t.Errorf("removing no fence was not a no-op: %v\n%s", err, out)
	}
}

// What a restatement leaves out is said in the words for what it was. A default
// that goes is "dropped"; a fence that goes is a lock coming off and is said as
// `--unfence` says it, in one row, in an order that does not change between runs
// — whichever block the key was in, and however many.
func TestARestatementSaysADroppedDefaultAndAnUnfencedKeyApart(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("profile", "set", "staging", "--plugin", "db",
		"--set", "host=db.internal", "--set", "tls=false",
		"--only", "dbname=app", "--only", "sslmode=require", "--only", "port=5432"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	out, errOut, err := run("profile", "set", "staging", "--plugin", "db",
		"--set", "host=db.other", "--only", "dbname=app")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	var dropped, unfenced string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(strings.TrimSpace(line), "dropped"):
			dropped = line
		case strings.HasPrefix(strings.TrimSpace(line), "unfenced"):
			unfenced = line
		}
	}
	if !strings.Contains(dropped, "set.tls") || strings.Contains(dropped, "only.") {
		t.Errorf("the dropped row = %q, want the default and no fence\n%s", dropped, out)
	}
	if !strings.Contains(unfenced, "only.port, only.sslmode") || strings.Contains(unfenced, "set.") {
		t.Errorf("the unfenced row = %q, want both fences, in order, and no default\n%s", unfenced, out)
	}
}

// Naming no plugin is refused for `--only` as it is for `--set`, in the same
// sentence, listing the flag.
func TestOnlyNamesNoPluginIsRefused(t *testing.T) {
	run := session(t, setRegistry(t))
	_, errOut, err := run("profile", "set", "staging", "--only", "dbname=app")
	if err == nil || !strings.Contains(errOut, "--only") || !strings.Contains(errOut, "core.profile.noplugin") {
		t.Fatalf("err = %v, stderr = %q", err, errOut)
	}
}

// What `rta profile show` prints is what a later run will load: the fence is a
// row of its own, said as the list nothing outside can be named from.
func TestProfileShowPrintsTheFence(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("profile", "set", "staging", "--plugin", "db",
		"--set", "dbname=app", "--only", "dbname=app", "--only", "dbname=app-jobs"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	out, errOut, err := run("profile", "show", "staging")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "only:dbname") || !strings.Contains(out, "app, app-jobs (and nothing else)") {
		t.Errorf("the page does not print the fence:\n%s", out)
	}
}

// A fence written by hand that the loader refuses is reported by every page
// that reports a profile, not only by the call that trips over it.
func TestAFenceTheLoaderRefusesIsReportedByListShowAndDoctor(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("profile", "set", "staging", "--plugin", "db", "--set", "host=db.internal"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	handWritten := strings.Replace(configOf(t), "      db:\n", "      db:\n        only:\n          dbname: app\n", 1)
	if err := os.WriteFile(config.Path(), []byte(handWritten), 0o600); err != nil {
		t.Fatal(err)
	}

	list, _, err := run("profile", "list")
	if err != nil || !strings.Contains(list, "invalid") || !strings.Contains(list, "only: dbname") {
		t.Errorf("profile list does not report the fence: %v\n%s", err, list)
	}
	show, _, err := run("profile", "show", "staging")
	if err != nil || !strings.Contains(show, "problem") || !strings.Contains(show, "`only: dbname`") {
		t.Errorf("profile show does not report the fence: %v\n%s", err, show)
	}
	doctor, _, _ := run("doctor")
	if !strings.Contains(doctor, "`only: dbname`") {
		t.Errorf("doctor does not report the fence:\n%s", doctor)
	}
}

// The card is where an operator looks up what a profile can do for a
// capability, and it said a profile supplies a connection and nothing about the
// one thing in it that a caller cannot override.
func TestExplainSaysAProfileCanFenceAnInput(t *testing.T) {
	out, _, err := run(t, setRegistry(t), "explain", "db.status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "`only:` in a profile's entry fences an input to the values it reaches") {
		t.Errorf("the card does not say a profile can fence an input:\n%s", out)
	}
}
