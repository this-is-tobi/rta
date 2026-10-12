package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `rta use` is how an environment stays on, and `--no-profile` is how one call
// leaves it: the call runs on the flags typed and the base configuration, as
// if nothing were switched on. These hold what that is allowed to be, which is
// a person's act at a terminal and nothing an agent can send.

const noProfileConfig = `profiles:
  staging:
    plugins:
      db:
        set:
          host: db.staging
  shards:
    plugins:
      db/a:
        set:
          host: a.internal
      db/b:
        set:
          host: b.internal
`

// runSwitchedOutcome is runSwitched for a call that may be refused: what the
// command printed and the error it ended in, with the environment switched on
// by the test.
func runSwitchedOutcome(t *testing.T, reg *registry.Registry, yaml string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if werr := os.WriteFile(path, []byte(yaml), 0o600); werr != nil {
		t.Fatal(werr)
	}
	t.Setenv("RTA_CONFIG", path)
	SetInstalled(reg)
	t.Cleanup(func() { SetInstalled(nil) })
	root := NewRoot(reg, "test")
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

// **The environment that is on is not consulted.** Not its values, not its
// credential, not which of its instances: the call is what the line says.
func TestNoProfileRunsTheCallOnTheBaseConfigurationAndNotTheEnvironment(t *testing.T) {
	on(t, "staging", nil)
	reg := connRegistry(t)

	out, errOut, err := runSwitchedOutcome(t, reg, noProfileConfig, "db", "status")
	if err != nil || !strings.Contains(out, "reached db.staging") {
		t.Fatalf("the switched-on environment did not apply: %q %v\n%s", out, err, errOut)
	}
	out, errOut, err = runSwitchedOutcome(t, reg, noProfileConfig, "db", "status", "--no-profile")
	// The base configuration of this registry is what the plugin declares: the
	// `plugins:` section is read at startup by main, which a test does not run.
	if err != nil || !strings.Contains(out, "reached localhost") {
		t.Errorf("--no-profile reached %q, want the declared default, localhost: %v\n%s", out, err, errOut)
	}

	// And the handler is told no environment was in play, which is what keeps
	// a hint it composes from pasting a profile name back.
	out, errOut, err = runSwitchedOutcome(t, whoamiRegistry(t), noProfileConfig, "db", "whoami", "--no-profile")
	if err != nil || !strings.Contains(out, `profile=""`) {
		t.Errorf("the handler was told %q: %v\n%s", strings.TrimSpace(out), err, errOut)
	}
}

// **An environment holding several instances of a plugin refused a call that
// typed every part of its connection.** Taking none of them is the one thing
// the call could not say, and the refusal now names it.
func TestNoProfileStatesAConnectionWhereTheEnvironmentHoldsSeveralInstances(t *testing.T) {
	on(t, "shards", nil)
	reg := connRegistry(t)

	_, errOut, err := runSwitchedOutcome(t, reg, noProfileConfig, "db", "status", "--host", "x.internal")
	if err == nil || !strings.Contains(errOut, "core.profile.instance.required") {
		t.Fatalf("a call naming no instance was not refused: %v\n%s", err, errOut)
	}
	for _, want := range []string{"shards/a", "shards/b", "--no-profile"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, errOut)
		}
	}

	out, errOut, err := runSwitchedOutcome(t, reg, noProfileConfig, "db", "status", "--no-profile", "--host", "x.internal")
	if err != nil || !strings.Contains(out, "reached x.internal") {
		t.Errorf("a call that left the environment was %q: %v\n%s", out, err, errOut)
	}

	// Naming one is still the way to use the environment.
	out, errOut, err = runSwitchedOutcome(t, reg, noProfileConfig, "db", "status", "--profile", "shards/a")
	if err != nil || !strings.Contains(out, "reached a.internal") {
		t.Errorf("naming an instance reached %q: %v\n%s", out, err, errOut)
	}
}

// The hint offers the way out of an environment to a call that did not name
// one. A call that named the environment itself, by a bare name that is
// ambiguous, asked for it, and is told which instance to add instead.
func TestTheRefusalOffersNoProfileOnlyToACallThatNamedNoEnvironment(t *testing.T) {
	reg := connRegistry(t)
	_, errOut, err := runWith(t, reg, noProfileConfig, "db", "status", "--profile", "shards")
	if err == nil || !strings.Contains(errOut, "core.profile.instance.required") {
		t.Fatalf("a bare name over two instances was not refused: %v\n%s", err, errOut)
	}
	if strings.Contains(errOut, "--no-profile") {
		t.Errorf("a call that asked for the environment was offered a way out of it:\n%s", errOut)
	}
}

// Two answers to which environment, and no way to rank them that a reader of
// the command would not have to look up.
func TestNoProfileIsRefusedBesideAProfile(t *testing.T) {
	_, errOut, err := runWith(t, connRegistry(t), noProfileConfig, "db", "status", "--profile", "staging", "--no-profile")
	if err == nil || !strings.Contains(errOut, "core.noprofile.both") {
		t.Errorf("a profile and no profile were both taken: %v\n%s", err, errOut)
	}
}

// A connection typed on the line already replaces the environment, so leaving
// it as well says the same thing twice and is not an error.
func TestNoProfileAgreesWithAConnectionTypedOnTheCommandLine(t *testing.T) {
	kubectlForwarding(t)
	on(t, "staging", nil)
	out, errOut, err := runSwitchedOutcome(t, whoamiRegistry(t), noProfileConfig, "db", "whoami",
		"--no-profile", "--kube", "homelab/databases/svc/postgres:5432")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(out, `profile="ad hoc"`) || !strings.Contains(out, `tunnel="kube"`) {
		t.Errorf("the handler was told %q, want the ad hoc connection the flags state", strings.TrimSpace(out))
	}
}

// Where a profile has nothing to fill the flag would do nothing, and a flag
// that exists everywhere and does something nowhere teaches that flags are
// decoration: it is on exactly the commands that take --profile, and listed in
// their help exactly where --profile is.
func TestNoProfileIsOnExactlyTheCommandsThatTakeAProfile(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(plugin.Plugin{Name: "dbfix", Summary: "a plugin with a connection",
		Capabilities: []plugin.Capability{{
			ID: "dbfix.status", Summary: "status", Safety: plugin.Read,
			Inputs: []plugin.Field{{Name: "address", Type: plugin.String, Default: "localhost:5432", Config: "address",
				Local: true, Endpoint: plugin.EndpointAddress, Help: "address"}},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		}}}); err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	with, without := 0, 0
	for _, c := range reg.Capabilities() {
		cmd, _, err := root.Find(c.Words())
		if err != nil || cmd == root {
			t.Fatalf("cannot reach the command for %s: %v", c.ID, err)
		}
		profile, noProfile := cmd.Flags().Lookup("profile"), cmd.Flags().Lookup("no-profile")
		// `grant allow --profile staging` takes the profile as *data*, the thing
		// being granted: that flag is the capability's own, not the host's, and
		// leaving the environment would mean nothing to a command that does not
		// run against one.
		if slices.ContainsFunc(c.Inputs, func(f plugin.Field) bool { return f.Name == "profile" }) {
			if noProfile != nil {
				t.Errorf("%s names a profile as its own input and has a --no-profile", c.ID)
			}
			continue
		}
		if (profile != nil) != (noProfile != nil) {
			t.Errorf("%s: --profile is %v and --no-profile is %v", c.ID, profile != nil, noProfile != nil)
			continue
		}
		if profile == nil {
			without++
			continue
		}
		with++
		if listsFlag(cmd, profile) != listsFlag(cmd, noProfile) {
			t.Errorf("%s: --no-profile is listed differently from --profile", c.ID)
		}
	}
	if with == 0 || without == 0 {
		t.Errorf("the registry holds %d commands with --profile and %d without, so this checks nothing", with, without)
	}
}

// Completion computes the connection a command would run against, and it has to
// read the same thing the run does: `rta db status --no-profile --host <tab>`
// offered what the environment holds while the call would have left it.
func TestCompletionReadsTheBaseConfigurationUnderNoProfile(t *testing.T) {
	on(t, "staging", nil)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(noProfileConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", path)
	reg := connRegistry(t)
	SetInstalled(reg)
	t.Cleanup(func() { SetInstalled(nil) })
	cmd, _, err := NewRoot(reg, "test").Find([]string{"db", "status"})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := reg.Capability("db.status")

	if name, bound, _ := bindProfile(cmd, c); name != "staging" || bound["host"] != "db.staging" {
		t.Fatalf("the environment was not bound: %q %v", name, bound)
	}
	if err := cmd.Flags().Set("no-profile", "true"); err != nil {
		t.Fatal(err)
	}
	if name, bound, _ := bindProfile(cmd, c); name != "" || len(bound) != 0 {
		t.Errorf("completion read %q (%v) under --no-profile", name, bound)
	}
}

// withNoProfile is the leaf of capability() with the flag a capability that
// takes a profile is given.
func withNoProfile(cmd *cobra.Command, set bool) *cobra.Command {
	cmd.Flags().Bool("no-profile", false, "")
	if set {
		_ = cmd.Flags().Set("no-profile", "true")
	}
	return cmd
}

// **The line above the result says the environment was left, and which.**
// Silent, it would be a call that ran on the base configuration with the
// environment's name nowhere on the screen; naming the environment as it does
// for a call that used it would be the lie the badge exists to prevent.
func TestTheLineAboveAResultSaysWhichEnvironmentTheCallLeft(t *testing.T) {
	on(t, "shop-dev", nil)

	var out bytes.Buffer
	WarnActiveProfile(&out, withNoProfile(capability("pg", "status", true), true), marked(), plain)
	if got := strings.TrimSpace(out.String()); got != "[ ad hoc — without shop-dev ]" {
		t.Errorf("a call that left the environment said %q", got)
	}

	// The same call without the flag is the one that used it.
	out.Reset()
	WarnActiveProfile(&out, withNoProfile(capability("pg", "status", true), false), marked(), plain)
	if got := strings.TrimSpace(out.String()); got != "[ shop-dev ]" {
		t.Errorf("a call that used the environment said %q", got)
	}

	// An environment that covers nothing here had nothing to leave.
	out.Reset()
	WarnActiveProfile(&out, withNoProfile(capability("kv", "get", true), true), marked(), plain)
	if out.Len() != 0 {
		t.Errorf("a call to a plugin the environment does not cover said %q", out.String())
	}

	// A marked environment is not painted on a call that left it: the colour
	// says "this one is on", and it is not.
	on(t, "shop-prod", nil)
	var painted bytes.Buffer
	WarnActiveProfile(&painted, withNoProfile(capability("pg", "status", true), true), marked(), BadgeStyle{})
	if strings.Contains(painted.String(), "255;107;122") || !strings.Contains(painted.String(), "ad hoc — without shop-prod") {
		t.Errorf("a marked environment left by the call was announced as in use: %q", painted.String())
	}
}

// Nothing switched on, nothing left.
func TestNoProfileSaysNothingWhereNoEnvironmentIsOn(t *testing.T) {
	on(t, "", nil)
	var out bytes.Buffer
	WarnActiveProfile(&out, withNoProfile(capability("pg", "status", true), true), marked(), plain)
	if out.Len() != 0 {
		t.Errorf("a call with no environment to leave said %q", out.String())
	}
}

// A call that names its own environment and leaves it is refused, and the line
// above the refusal is not one that names either as the one in use.
func TestTheLineAboveAContradictionNamesNoEnvironment(t *testing.T) {
	on(t, "shop-dev", nil)
	cmd := withNoProfile(capability("pg", "status", true), true)
	if err := cmd.Flags().Set("profile", "shop-dev"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	WarnActiveProfile(&out, cmd, marked(), plain)
	if out.Len() != 0 {
		t.Errorf("a call that contradicted itself said %q", out.String())
	}
}

// The wiring through the root command, on a terminal: the line the person sees
// before the result.
func TestTheRootCommandSaysWhichEnvironmentTheCallLeft(t *testing.T) {
	saved := stderrIsTerminal
	t.Cleanup(func() { stderrIsTerminal = saved })
	stderrIsTerminal = func() bool { return true }
	on(t, "staging", nil)

	out, errOut, err := runSwitchedOutcome(t, connRegistry(t), noProfileConfig, "db", "status", "--no-profile", "--no-color")
	if err != nil || !strings.Contains(out, "reached localhost") {
		t.Fatalf("%q %v\n%s", out, err, errOut)
	}
	if !strings.Contains(errOut, "[ ad hoc — without staging ]") {
		t.Errorf("the line above the result said %q", errOut)
	}
}
