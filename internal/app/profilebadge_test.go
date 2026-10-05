package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
)

// on switches an environment on for the duration of one test.
func on(t *testing.T, name string, until *time.Time) {
	t.Helper()
	if verr := profile.SaveSelection(profile.Selection{Active: name, Until: until}); verr != nil {
		t.Fatalf("switching to %s: %v", name, verr)
	}
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
}

// covers is a plugin entry the way a profile writes one; what is in it does
// not matter to whether the environment is on.
var covers = map[string]config.Connection{"pg": {}, "sys": {}}

func marked() config.Config {
	return config.Config{Profiles: map[string]config.Profile{
		"shop-prod":    {Color: "#FF6B7A", Plugins: covers},
		"shop-dev":     {Plugins: covers},
		"shop-broken":  {Color: "red", Plugins: covers},
		"shop-pale":    {Color: "#FFC24B", Plugins: covers},
		"shop-unknown": {Color: "#3ED598", Plugins: covers},
	}}
}

// capability is the leaf command a plugin's capability becomes, `rta sys cpu`,
// with the --profile flag a capability a profile can fill gets.
func capability(namespace, name string, profilable bool) *cobra.Command {
	root := &cobra.Command{Use: "rta"}
	group := &cobra.Command{Use: namespace}
	leaf := &cobra.Command{Use: name, Run: func(*cobra.Command, []string) {}}
	if profilable {
		leaf.Flags().String("profile", "", "")
	}
	group.AddCommand(leaf)
	root.AddCommand(group)
	return leaf
}

var plain = BadgeStyle{NoColor: true}

// **An environment somebody marked announces itself before every command.**
//
// Writing `color:` is the operator saying this is the environment worth
// interrupting them about, and it is the one that does it on `rta version` as
// well as on `rta pg status`.
func TestAMarkedEnvironmentAnnouncesItselfBeforeEveryCommand(t *testing.T) {
	on(t, "shop-prod", nil)
	for name, cmd := range map[string]*cobra.Command{
		"a command of a plugin it covers":     capability("pg", "status", true),
		"a command of a plugin it does not":   capability("kv", "get", true),
		"a command that takes no environment": capability("pg", "status", false),
		"no command at all":                   nil,
	} {
		var out bytes.Buffer
		WarnActiveProfile(&out, cmd, marked(), plain)
		if !strings.Contains(out.String(), "[ shop-prod ]") {
			t.Errorf("%s: a marked environment did not announce itself: %q", name, out.String())
		}
	}
}

// **An unmarked one announces itself where it acts.**
//
// A bare `rta use staging` stays on tomorrow, and every command of every plugin
// it covers then runs there with nothing on screen saying so. The line is the
// plain bullet the TUI's header draws for it, and it is what a person sees
// before the result of `rta sys cpu`, the way they see a marked one.
func TestAnUnmarkedEnvironmentAnnouncesItselfBeforeTheCommandsItActsOn(t *testing.T) {
	on(t, "shop-dev", nil)
	var out bytes.Buffer
	WarnActiveProfile(&out, capability("sys", "cpu", true), marked(), plain)
	if got := strings.TrimSpace(out.String()); got != "[ shop-dev ]" {
		t.Errorf("an unmarked environment said %q before a command it covers", got)
	}

	var painted bytes.Buffer
	WarnActiveProfile(&painted, capability("sys", "cpu", true), marked(), BadgeStyle{})
	if !strings.Contains(painted.String(), "\x1b[") || !strings.Contains(painted.String(), "● shop-dev") {
		t.Errorf("the default badge is not the TUI's painted bullet: %q", painted.String())
	}

	var ascii bytes.Buffer
	WarnActiveProfile(&ascii, capability("sys", "cpu", true), marked(), BadgeStyle{ASCII: true})
	if nonASCII := strings.IndexFunc(ascii.String(), func(r rune) bool { return r > 127 }); nonASCII >= 0 ||
		!strings.Contains(ascii.String(), "* shop-dev") {
		t.Errorf("the bullet was not an asterisk in ASCII: %q", ascii.String())
	}
}

// **And nowhere else, which is what keeps it meaning something.** A banner on
// every command is one nobody reads within a week; the commands an environment
// does not touch have nothing to say about it.
func TestAnUnmarkedEnvironmentStaysQuietWhereItActsOnNothing(t *testing.T) {
	on(t, "shop-dev", nil)
	named := capability("sys", "cpu", true)
	if err := named.Flags().Set("profile", "shop-prod"); err != nil {
		t.Fatal(err)
	}
	group := capability("sys", "cpu", true).Parent()
	for name, cmd := range map[string]*cobra.Command{
		"a plugin it does not cover":               capability("kv", "get", true),
		"a command no profile can change":          capability("sys", "disk", false),
		"a command that names its own environment": named,
		"a group, which prints help":               group,
		"a command that is not a plugin's":         capability("use", "x", false),
		"no command at all":                        nil,
		"the root":                                 group.Parent(),
	} {
		var out bytes.Buffer
		WarnActiveProfile(&out, cmd, marked(), plain)
		if out.Len() != 0 {
			t.Errorf("%s: an unmarked environment printed %q", name, out.String())
		}
	}

	// An environment the config does not know is on in name only.
	on(t, "shop-unknown-to-config", nil)
	var out bytes.Buffer
	WarnActiveProfile(&out, capability("sys", "cpu", true), marked(), plain)
	if out.Len() != 0 {
		t.Errorf("an environment the config does not know printed %q", out.String())
	}
}

// Nothing switched on says nothing. The badge answers "which environment", and
// there is no answer to give.
func TestNothingSwitchedOnPrintsNothing(t *testing.T) {
	on(t, "", nil)
	var out bytes.Buffer
	WarnActiveProfile(&out, capability("sys", "cpu", true), marked(), plain)
	if out.Len() != 0 {
		t.Errorf("a session with no environment printed %q", out.String())
	}
}

// The same rule the untrusted-plugin notice learned the hard way: a person
// running `-o json` at a prompt is building a pipeline, and the output they
// copy off the screen is the output they paste into a parser.
func TestTheBadgeStaysOutOfMachineReadableOutput(t *testing.T) {
	on(t, "shop-prod", nil)
	var out bytes.Buffer
	WarnActiveProfile(&out, capability("sys", "cpu", true), marked(), BadgeStyle{MachineReadable: true})
	if out.Len() != 0 {
		t.Errorf("a run asking for a parseable format got a badge: %q", out.String())
	}
}

// **A colour that is not a colour marks nothing, rather than falling back to
// one nobody chose.** A badge in a default colour would say "this environment
// is marked" about one whose marking rta could not read — and the operator who
// typed it believes something is happening. Such an environment is announced as
// an unmarked one is, which is true of it, and `rta doctor` is where the colour
// is reported, by profile.Check.
func TestAColourThatIsNotAColourMarksNothing(t *testing.T) {
	on(t, "shop-broken", nil)
	var out bytes.Buffer
	WarnActiveProfile(&out, capability("kv", "get", true), marked(), BadgeStyle{})
	if out.Len() != 0 {
		t.Errorf("an unreadable colour was announced before a command it does not act on: %q", out.String())
	}
	out.Reset()
	WarnActiveProfile(&out, capability("pg", "status", true), marked(), BadgeStyle{})
	if !strings.Contains(out.String(), "● shop-broken") {
		t.Errorf("an unreadable colour was not announced as an unmarked environment: %q", out.String())
	}
}

// A colour that is not a colour is noted, and nothing refuses the profile
// over it: a badge left unpainted is not a reason to be unable to switch to
// an environment. It sat among the problems Check reports, which every reader
// takes as a refusal, so `rta use` refused the profile and `rta profile list`
// called it invalid — while `--profile` ran through it.
func TestAColourThatIsNotAColourIsNotedAndTheProfileStaysUsable(t *testing.T) {
	const cfg = `
profiles:
  broken:
    color: red
    plugins:
      db:
        set:
          dbname: orders
`
	out, errOut, err := runWith(t, setRegistry(t), cfg, "profile", "list")
	if err != nil {
		t.Fatalf("%v %s", err, errOut)
	}
	if strings.Contains(out, "invalid") || !strings.Contains(out, "which is not a colour") {
		t.Errorf("profile list:\n%s", out)
	}
	out, _, _ = runWith(t, setRegistry(t), cfg, "doctor")
	if !strings.Contains(out, "has color red, which is not a colour") {
		t.Errorf("doctor does not report it:\n%s", out)
	}
	if _, errOut, err := runWith(t, setRegistry(t), cfg, "use", "broken"); err != nil {
		t.Errorf("the profile could not be switched to: %v %s", err, errOut)
	}
}

// --no-color is a statement about ANSI, not about wanting to know less: the
// badge keeps its brackets and loses its paint.
func TestWithoutColourTheBadgeKeepsItsBrackets(t *testing.T) {
	on(t, "shop-prod", nil)

	var unpainted bytes.Buffer
	WarnActiveProfile(&unpainted, capability("pg", "status", true), marked(), plain)
	if strings.Contains(unpainted.String(), "\x1b[") {
		t.Errorf("--no-color still emitted escapes: %q", unpainted.String())
	}
	if !strings.Contains(unpainted.String(), "[ shop-prod ]") {
		t.Errorf("--no-color lost the badge entirely: %q", unpainted.String())
	}

	// …and the coloured form really is coloured, or the whole feature is a
	// line of text that happens to name the profile.
	var painted bytes.Buffer
	WarnActiveProfile(&painted, capability("pg", "status", true), marked(), BadgeStyle{})
	if !strings.Contains(painted.String(), "\x1b[") {
		t.Errorf("the badge was not painted at all: %q", painted.String())
	}
}

// Being on production and being on it for six more minutes are different
// situations, and both are decided before the command runs rather than after.
func TestADeadlineRidesAlongWithTheBadge(t *testing.T) {
	until := time.Now().Add(47 * time.Minute)
	on(t, "shop-prod", &until)

	var out bytes.Buffer
	WarnActiveProfile(&out, capability("pg", "status", true), marked(), plain)
	if !strings.Contains(out.String(), "left") {
		t.Errorf("a switch with a deadline did not say how long is left: %q", out.String())
	}

	// A switch with no deadline says nothing about one, rather than "forever",
	// which is a claim about a file somebody can edit.
	on(t, "shop-prod", nil)
	out.Reset()
	WarnActiveProfile(&out, capability("pg", "status", true), marked(), plain)
	if strings.Contains(out.String(), "left") {
		t.Errorf("a switch with no deadline invented one: %q", out.String())
	}
}

// runSwitched runs a command with the environment `on` switched on, which
// runWith cannot: it points every call at a data directory of its own, and a
// switch lives in the data directory.
func runSwitched(t *testing.T, reg *registry.Registry, yaml string, args ...string) (stdout, stderr string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", path)
	SetInstalled(reg)
	t.Cleanup(func() { SetInstalled(nil) })
	root := NewRoot(reg, "test")
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("rta %s: %v\n%s", strings.Join(args, " "), err, errOut.String())
	}
	return out.String(), errOut.String()
}

// The wiring, not only the function: an environment with no `color:` is named
// before the command that runs against it, once the root command hands the
// hook the command it is running. A hook given nil would pass every test of
// the function above and say nothing for an unmarked environment.
func TestTheRootCommandNamesTheEnvironmentBeforeACommandItActsOn(t *testing.T) {
	saved := stderrIsTerminal
	t.Cleanup(func() { stderrIsTerminal = saved })
	stderrIsTerminal = func() bool { return true }
	const cfg = "profiles:\n  staging:\n    plugins:\n      db:\n        set:\n          host: db.staging\n"
	reg := setRegistry(t)
	on(t, "staging", nil)

	out, errOut := runSwitched(t, reg, cfg, "db", "status", "--no-color")
	if !strings.Contains(out, "reached db.staging") {
		t.Fatalf("the command did not run against the switched environment: %q", out)
	}
	if !strings.Contains(errOut, "[ staging ]") {
		t.Errorf("the command ran against staging and said nothing about it: %q", errOut)
	}

	for name, args := range map[string][]string{
		"asking for json":                {"db", "status", "-o", "json"},
		"naming its own profile":         {"db", "status", "--profile", "staging"},
		"a command that acts on nothing": {"profile", "list"},
	} {
		if _, errOut := runSwitched(t, reg, cfg, args...); strings.Contains(errOut, "staging") {
			t.Errorf("%s: the environment was announced anyway: %q", name, errOut)
		}
	}

	stderrIsTerminal = func() bool { return false }
	if _, errOut := runSwitched(t, reg, cfg, "db", "status"); strings.Contains(errOut, "staging") {
		t.Errorf("the environment was announced on a stream nobody is watching: %q", errOut)
	}
}

// The result of a command follows the terminal's colour profile, and so does
// the line above it: under NO_COLOR or TERM=dumb it is the bracketed text and
// not a green bullet, and on a terminal of sixteen colours it is not a truecolor
// escape the terminal would print as noise.
func TestTheBadgeFollowsWhatTheTerminalCanShow(t *testing.T) {
	on(t, "shop-dev", nil)
	for name, c := range map[string]struct {
		environ []string
		plain   bool
	}{
		"a terminal with colour":   {[]string{"TTY_FORCE=1", "TERM=xterm-256color"}, false},
		"NO_COLOR":                 {[]string{"TTY_FORCE=1", "TERM=xterm-256color", "NO_COLOR=1"}, true},
		"a dumb terminal":          {[]string{"TTY_FORCE=1", "TERM=dumb"}, true},
		"a terminal of 16 colours": {[]string{"TTY_FORCE=1", "TERM=xterm-color"}, false},
	} {
		var out bytes.Buffer
		stream, plain := badgeStream(&out, c.environ)
		if plain != c.plain {
			t.Errorf("%s: plain = %v, want %v", name, plain, c.plain)
		}
		WarnActiveProfile(stream, capability("sys", "cpu", true), marked(), BadgeStyle{NoColor: plain})
		switch {
		case c.plain && (strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "[ shop-dev ]")):
			t.Errorf("%s: %q, want the bracketed text and no escape", name, out.String())
		case !c.plain && !strings.Contains(out.String(), "\x1b["):
			t.Errorf("%s: %q, want it painted", name, out.String())
		}
	}

	var sixteen bytes.Buffer
	stream, _ := badgeStream(&sixteen, []string{"TTY_FORCE=1", "TERM=xterm-color"})
	WarnActiveProfile(stream, capability("sys", "cpu", true), marked(), BadgeStyle{})
	if strings.Contains(sixteen.String(), "38;2;") {
		t.Errorf("a truecolor escape on a terminal of sixteen colours: %q", sixteen.String())
	}
}

// And through the root command, which is where NO_COLOR is read from.
func TestTheRootCommandHonoursNoColorForTheBadge(t *testing.T) {
	saved := stderrIsTerminal
	t.Cleanup(func() { stderrIsTerminal = saved })
	stderrIsTerminal = func() bool { return true }
	t.Setenv("TTY_FORCE", "1")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LC_ALL", "C.UTF-8")
	const cfg = "profiles:\n  staging:\n    plugins:\n      db:\n        set:\n          host: db.staging\n"
	reg := setRegistry(t)
	on(t, "staging", nil)

	t.Setenv("NO_COLOR", "")
	if _, errOut := runSwitched(t, reg, cfg, "db", "status"); !strings.Contains(errOut, "\x1b[") || !strings.Contains(errOut, "● staging") {
		t.Errorf("a terminal with colour: %q, want the painted bullet", errOut)
	}
	t.Setenv("NO_COLOR", "1")
	if _, errOut := runSwitched(t, reg, cfg, "db", "status"); strings.Contains(errOut, "\x1b") || !strings.Contains(errOut, "[ staging ]") {
		t.Errorf("NO_COLOR: %q, want the bracketed text", errOut)
	}
}
