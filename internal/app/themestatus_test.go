package app

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/render/theme"
)

// theme.status is the one entry of the theme block that is a word, and every
// surface that writes or reads the block takes it as one: the file, `rta config
// set`, get and unset, `rta config check`, `rta doctor` and the palette a command
// is drawn in.

// Each mode is written as the word, reads back as it, and comes out again. "off" is
// the one a YAML 1.1 reader takes for a boolean, and what rta writes is quoted.
func TestConfigSetStatusWritesTheWordAndItLoadsBackAsOne(t *testing.T) {
	reg := configRegistry(t)
	for _, mode := range theme.GradeModes() {
		t.Run(mode, func(t *testing.T) {
			_, errOut, file, err := configRun(t, reg, "", "config", "set", "theme.status", mode)
			if err != nil {
				t.Fatalf("%v\n%s", err, errOut)
			}
			cfg, perr := config.Parse([]byte(file))
			if perr != nil || cfg.Theme[theme.GradesKey] != mode {
				t.Fatalf("the file it wrote reads back as %v (%v):\n%s", cfg.Theme, perr, file)
			}
			if mode == "off" && !strings.Contains(file, `status: "off"`) {
				t.Errorf("off is written bare, which a YAML 1.1 reader takes for false:\n%s", file)
			}
			out, _, _, err := configRun(t, reg, file, "config", "get", "theme.status", "-o", "pretty")
			if err != nil || out != mode+"\n" {
				t.Errorf("get theme.status = %q (%v), want %q", out, err, mode)
			}
			_, _, after, err := configRun(t, reg, file, "config", "unset", "theme.status")
			if err != nil || strings.Contains(after, "status") || strings.Contains(after, "theme") {
				t.Errorf("unset left %q (%v), want the key and the block it leaves empty gone", after, err)
			}
		})
	}
}

// A colour beside it is carried, and so is it beside a colour: the two live in one
// block and neither write may take the other with it.
func TestConfigSetStatusAndAColourLiveInOneBlock(t *testing.T) {
	reg := configRegistry(t)
	_, _, file, err := configRun(t, reg, "theme:\n  primary: \"#7aa2f7\"\n", "config", "set", "theme.status", "alerts")
	if err != nil || !strings.Contains(file, "primary: \"#7aa2f7\"") || !strings.Contains(file, "status: alerts") {
		t.Fatalf("%v\n%s", err, file)
	}
	_, _, file, err = configRun(t, reg, file, "config", "set", "theme.accent", "#C792EA")
	if err != nil || !strings.Contains(file, "status: alerts") {
		t.Fatalf("a colour written beside it took it away: %v\n%s", err, file)
	}
	_, _, file, err = configRun(t, reg, file, "config", "unset", "theme.primary")
	if err != nil || !strings.Contains(file, "status: alerts") || strings.Contains(file, "primary") {
		t.Fatalf("a colour taken out took it away: %v\n%s", err, file)
	}
}

func TestConfigSetStatusRefusesAWordThatIsNoMode(t *testing.T) {
	reg := configRegistry(t)
	for _, word := range []string{"loud", "Off", "false", "none", "quiet"} {
		_, errOut, file, err := configRun(t, reg, "", "config", "set", "theme.status", word)
		if err == nil || codeOf(t, errOut) != "core.config.set.option" {
			t.Errorf("%q: %v, code %q\n%s", word, err, codeOf(t, errOut), errOut)
			continue
		}
		if !strings.Contains(errOut, "all, alerts, off") || !strings.Contains(errOut, "rta config set theme.status alerts") {
			t.Errorf("%q: the refusal does not list the modes and a line that works:\n%s", word, errOut)
		}
		if file != "" {
			t.Errorf("%q: a refused value was written:\n%s", word, file)
		}
	}
}

// `rta config show` lists what the file states, and the setting is among it with
// the word, as every other key of the block is.
func TestConfigShowListsStatusWithItsWord(t *testing.T) {
	out, errOut, _, err := configRun(t, configRegistry(t), "theme:\n  status: alerts\n", "config", "show", "-o", "pretty")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if flat := squash(out); !strings.Contains(flat, "theme.status") || !strings.Contains(flat, "alerts") {
		t.Errorf("theme.status is not among what the file states:\n%s", out)
	}
}

// What the check says of the entry, and of the same entry where an environment
// states it, with the line to open.
func TestConfigCheckHoldsStatusToItsModesAndKeepsItOutOfAnEnvironment(t *testing.T) {
	restoreTheme(t)
	cfg := "theme:\n  status: sometimes\nprofiles:\n  prod:\n    theme:\n      status: \"off\"\n" +
		"    plugins:\n      db:\n        set:\n          host: p\n"
	out, _, _ := runWith(t, setRegistry(t), cfg, "config", "check", "-o", "json")
	said := map[string][]string{}
	for _, row := range jsonRows(t, out) {
		said[row[1]] = row
	}
	own, ok := said["theme.status"]
	if !ok || own[0] == "" || !strings.Contains(own[2], `"sometimes" is not a way to draw status`) ||
		!strings.Contains(own[2], "all, alerts, off") {
		t.Errorf("the check does not name the bad value with its line and the modes: %v", own)
	}
	env, ok := said["profiles.prod.theme.status"]
	if !ok || env[0] == "" || !strings.Contains(env[2], "only your own config file") ||
		!strings.Contains(env[2], "rta config set theme.status") {
		t.Errorf("the check does not refuse an environment's status with its line and whose it is: %v", env)
	}

	// A mode the operator states is no problem at all.
	for _, mode := range theme.GradeModes() {
		out, _, err := runWith(t, setRegistry(t), "theme:\n  status: "+mode+"\n", "config", "check", "-o", "json")
		if err != nil || len(jsonRows(t, out)) != 0 {
			t.Errorf("status: %s is reported: %v\n%s", mode, err, out)
		}
	}
}

// What doctor reports of a theme the palette could not use includes a status it
// could not, and an environment's, which never reaches the operator's screen.
func TestDoctorSaysWhenStatusCannotBeUsed(t *testing.T) {
	restoreTheme(t)
	out, _, _ := runWith(t, setRegistry(t), "theme:\n  status: loud\n", "doctor")
	if flat := squash(out); !strings.Contains(flat, "theme.status") || !strings.Contains(flat, "all, alerts, off") {
		t.Errorf("doctor does not say the status was not used:\n%s", out)
	}
	if got := theme.Grading(); got != theme.GradesAll {
		t.Errorf("a status that cannot be used left %s in force", got)
	}
}

// The palette a command is drawn in carries the setting from the file the command
// reads, and an environment's own theme cannot move it: the operator's block is
// the only one that states it, under whichever environment is on.
func TestACommandIsDrawnByTheStatusTheOperatorStates(t *testing.T) {
	gradingAfter := func(t *testing.T, yaml string, args ...string) theme.Grades {
		t.Helper()
		restoreTheme(t)
		if out, errOut, err := runSwitchedOutcome(t, connRegistry(t), yaml, args...); err != nil {
			t.Fatalf("rta %s: %v\n%s\n%s", strings.Join(args, " "), err, out, errOut)
		}
		return theme.Grading()
	}
	const environment = `profiles:
  staging:
    theme:
      status: "off"
    plugins:
      db:
        set:
          host: db.staging
`
	if got := gradingAfter(t, "theme:\n  status: alerts\n"+environment, "db", "status"); got != theme.GradesAlerts {
		t.Errorf("status: alerts in the file drew %s", got)
	}
	on(t, "staging", nil)
	if got := gradingAfter(t, "theme:\n  status: alerts\n"+environment, "db", "status"); got != theme.GradesAlerts {
		t.Errorf("under staging, whose theme states off, the command is drawn %s: an environment moved it", got)
	}
	if got := gradingAfter(t, environment, "db", "status"); got != theme.GradesAll {
		t.Errorf("under staging alone the command is drawn %s: an environment moved it", got)
	}
}
