package app

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/render/theme"
)

// What a command is drawn in follows the environment it is about: the one named
// on the call, else the one switched on, and nothing for a call that leaves it.
// The palette is package state, and a command leaves it where it set it, so
// each case reads it back after the run.

const themedEnvironments = `theme:
  accent: "#5577AA"
profiles:
  staging:
    color: "#dd3333"
    theme:
      muted: "#00aa66"
    plugins:
      db:
        set:
          host: db.staging
  other:
    theme:
      primary: "#445566"
    plugins:
      db:
        set:
          host: db.other
  shards:
    theme:
      label: "#778899"
    plugins:
      db/a:
        set:
          host: a.internal
      db/b:
        set:
          host: b.internal
`

// restoreTheme puts the built-in palette back when the test ends, so a palette a
// command painted cannot reach the one that runs next.
func restoreTheme(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { theme.Apply(nil) })
}

// paletteAfter runs the command and answers the palette it left.
func paletteAfter(t *testing.T, yaml string, args ...string) map[string]string {
	t.Helper()
	restoreTheme(t)
	out, errOut, err := runSwitchedOutcome(t, connRegistry(t), yaml, args...)
	if err != nil {
		t.Fatalf("rta %s: %v\n%s\n%s", strings.Join(args, " "), err, out, errOut)
	}
	return theme.Current()
}

func builtin(slot string) string { return theme.Effective(nil)[slot] }

func TestACommandIsDrawnInTheColoursOfTheEnvironmentThatIsOn(t *testing.T) {
	on(t, "staging", nil)
	got := paletteAfter(t, themedEnvironments, "db", "status")
	// Its colour is its primary, its own theme lies over that, and the operator's
	// block is what both lie over.
	for slot, want := range map[string]string{"primary": "#dd3333", "muted": "#00aa66", "accent": "#5577AA"} {
		if got[slot] != want {
			t.Errorf("%s = %s inside staging, want %s", slot, got[slot], want)
		}
	}
	if got["bad"] != builtin("bad") {
		t.Errorf("bad = %s, want the built-in: staging states nothing about it", got["bad"])
	}
}

// Switched off, nothing of the environment is left behind.
func TestTheColoursGoBackWhenTheEnvironmentIsSwitchedOff(t *testing.T) {
	on(t, "staging", nil)
	if inside := paletteAfter(t, themedEnvironments, "db", "status"); inside["primary"] != "#dd3333" {
		t.Fatalf("the environment did not paint: primary = %s", inside["primary"])
	}
	if verr := profile.SaveSelection(profile.Selection{}); verr != nil {
		t.Fatal(verr)
	}
	got := paletteAfter(t, themedEnvironments, "db", "status")
	if got["primary"] != builtin("primary") || got["muted"] != builtin("muted") {
		t.Errorf("primary = %s, muted = %s after the switch went off, want the built-ins", got["primary"], got["muted"])
	}
	if got["accent"] != "#5577AA" {
		t.Errorf("accent = %s, want the operator's own block still", got["accent"])
	}
}

// A call that names an environment is about that one, whatever is switched on.
func TestAProfileOnTheCallPaintsInsteadOfTheOneThatIsOn(t *testing.T) {
	on(t, "staging", nil)
	got := paletteAfter(t, themedEnvironments, "db", "status", "--profile", "other")
	if got["primary"] != "#445566" {
		t.Errorf("primary = %s, want other's", got["primary"])
	}
	if got["muted"] != builtin("muted") {
		t.Errorf("muted = %s: staging's colours stayed on a call that named another environment", got["muted"])
	}
}

// `staging/analytics` is one of the places staging reaches, not a screen of its own.
func TestAnInstanceReferencePaintsInItsEnvironmentsColours(t *testing.T) {
	got := paletteAfter(t, themedEnvironments, "db", "status", "--profile", "shards/a")
	if got["label"] != "#778899" {
		t.Errorf("label = %s, want the environment's #778899", got["label"])
	}
}

// A call that leaves the environment is not drawn in it.
func TestACallThatLeavesTheEnvironmentIsDrawnInTheOperatorsOwnColours(t *testing.T) {
	on(t, "staging", nil)
	got := paletteAfter(t, themedEnvironments, "db", "status", "--no-profile")
	if got["primary"] != builtin("primary") || got["muted"] != builtin("muted") {
		t.Errorf("primary = %s, muted = %s on a call that left staging", got["primary"], got["muted"])
	}
	if got["accent"] != "#5577AA" {
		t.Errorf("accent = %s, want the operator's own block", got["accent"])
	}
}

// A connection typed on the line is where the call goes, over whatever is on,
// and is nobody's environment: the badge says so, and so does the colour.
func TestAConnectionTypedOnTheLineIsDrawnInTheOperatorsOwnColours(t *testing.T) {
	kubectlForwarding(t)
	on(t, "staging", nil)
	restoreTheme(t)
	_, errOut, err := runSwitchedOutcome(t, whoamiRegistry(t), themedEnvironments,
		"db", "whoami", "--kube", "homelab/databases/svc/postgres:5432")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if got := theme.Current()["primary"]; got != builtin("primary") {
		t.Errorf("primary = %s on a call that reached somewhere typed on the line, want the built-in", got)
	}
}

// A command with no environment of its own takes the one that is switched on:
// it resolves nothing, and nothing on its line says otherwise. And the
// `--profile` of a command that is not a capability is not an environment — it
// pins a tile to a connection and the command runs where it always did.
func TestACommandThatResolvesNoProfileTakesTheOneThatIsOn(t *testing.T) {
	on(t, "staging", nil)
	got := paletteAfter(t, themedEnvironments, "use")
	if got["primary"] != "#dd3333" {
		t.Errorf("primary = %s, want staging's", got["primary"])
	}
	// Whether the command itself is accepted does not matter here, only whose
	// colours it was painted in before it ran.
	restoreTheme(t)
	_, _, _ = runSwitchedOutcome(t, connRegistry(t), themedEnvironments,
		"dashboard", "add", "db.status", "--profile", "other", "--dry-run")
	if got := theme.Current()["primary"]; got != "#dd3333" {
		t.Errorf("primary = %s: the --profile of a tile's pin painted the command in another environment", got)
	}
}

// A switch is drawn in the colours of where it went, and a switch off in the
// operator's own: the receipt is not the one line left in the old palette.
func TestTheReceiptOfASwitchIsDrawnInTheColoursItSwitchedTo(t *testing.T) {
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
	got := paletteAfter(t, themedEnvironments, "use", "staging")
	if got["primary"] != "#dd3333" || got["muted"] != "#00aa66" {
		t.Errorf("primary = %s, muted = %s after `rta use staging`, want staging's", got["primary"], got["muted"])
	}
	got = paletteAfter(t, themedEnvironments, "use", "--off")
	if got["primary"] != builtin("primary") || got["muted"] != builtin("muted") {
		t.Errorf("primary = %s, muted = %s after `rta use --off`, want the built-ins", got["primary"], got["muted"])
	}
}

// The `--dry-run` of a switch changes nothing, the colours included.
func TestADryRunSwitchLeavesTheColoursAlone(t *testing.T) {
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
	got := paletteAfter(t, themedEnvironments, "use", "staging", "--dry-run")
	if got["primary"] != builtin("primary") {
		t.Errorf("primary = %s after a dry run, want the built-in", got["primary"])
	}
}

// A repository's own config is honoured for the operator's block and for nothing
// an environment says: its profiles are not honoured at all.
func TestAConfigNobodyNamedCannotRepaintAnEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("RTA_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if err := os.WriteFile(filepath.Join(dir, ".rta.yaml"), []byte(themedEnvironments), 0o600); err != nil {
		t.Fatal(err)
	}
	on(t, "staging", nil)
	restoreTheme(t)
	reg := connRegistry(t)
	SetInstalled(reg)
	t.Cleanup(func() { SetInstalled(nil) })
	root := NewRoot(reg, "test")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"use"})
	_ = root.Execute()

	got := theme.Current()
	if got["primary"] != builtin("primary") || got["muted"] != builtin("muted") {
		t.Errorf("primary = %s, muted = %s: a profile from a repository's config painted the screen", got["primary"], got["muted"])
	}
	if got["accent"] != "#5577AA" {
		t.Errorf("accent = %s, want the block it has always been able to state", got["accent"])
	}
}

func TestAnEnvironmentOfAnInstanceReferenceIsItsProfile(t *testing.T) {
	cmd := capability("db", "status", true)
	cmd.Flags().Bool("no-profile", false, "")
	if err := cmd.Flags().Set("profile", "shards/a"); err != nil {
		t.Fatal(err)
	}
	if got := environmentOf(cmd); got != "shards" {
		t.Errorf("environmentOf = %q, want shards", got)
	}
}
