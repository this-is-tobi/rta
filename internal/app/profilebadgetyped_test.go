package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
)

// typedProfiles is an environment that states where pg is, the way a profile
// writes it.
func typedProfiles() config.Config {
	return config.Config{Profiles: map[string]config.Profile{
		"shop-dev": {Plugins: map[string]config.Connection{
			"pg": {Set: map[string]any{"host": "db.shop", "port": 5432}},
		}},
		"shop-prod": {Color: "#FF6B7A", Plugins: map[string]config.Connection{
			"pg": {Set: map[string]any{"host": "db.prod"}},
		}},
	}}
}

// connectionCommand is `rta pg status`, with the flags a connection's inputs
// become and the config key each one is filled from: what declareFlags records,
// and what lets the badge tell a typed value from one the environment states.
func connectionCommand(t *testing.T, typed map[string]string) *cobra.Command {
	t.Helper()
	cmd := capability("pg", "status", true)
	for flag, key := range map[string]string{"host": "host", "port": "port", "limit": "limit"} {
		cmd.Flags().String(flag, "", "")
		if key != "" {
			if err := cmd.Flags().SetAnnotation(flag, annotFills, []string{key}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for flag, value := range typed {
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	return cmd
}

// **The line above a result never names an environment the result did not come
// from.** A value typed on the command line wins over the environment's own
// (plugin.Resolve ranks the caller first), so a call typed `--host other`
// under `rta use shop-dev` went to `other` while the line above it said
// `shop-dev` and nothing else: the one place that line was wrong, on the one
// thing it is there to say.
func TestTheBadgeSaysWhichValuesTypedOnTheCommandReplaceTheEnvironments(t *testing.T) {
	on(t, "shop-dev", nil)

	var out bytes.Buffer
	WarnActiveProfile(&out, connectionCommand(t, map[string]string{"host": "other.example", "port": "5433"}),
		typedProfiles(), plain)
	if got := strings.TrimSpace(out.String()); got != "[ shop-dev, except --host other.example --port 5433 ]" {
		t.Errorf("a call typed over the environment's host and port said %q", got)
	}
}

// A value typed that the environment already says changes nothing, and a line
// that listed it would teach that the clause is decoration.
func TestTheBadgeSaysNothingOfATypedValueThatRepeatsTheEnvironment(t *testing.T) {
	on(t, "shop-dev", nil)

	var out bytes.Buffer
	WarnActiveProfile(&out, connectionCommand(t, map[string]string{"host": "db.shop", "port": "5432"}),
		typedProfiles(), plain)
	if got := strings.TrimSpace(out.String()); got != "[ shop-dev ]" {
		t.Errorf("a call that typed what the environment says said %q", got)
	}
}

// Nor of one the environment never stated: there is nothing it replaced, and
// the person who typed `--limit 5` knows it.
func TestTheBadgeSaysNothingOfATypedValueTheEnvironmentDoesNotState(t *testing.T) {
	on(t, "shop-dev", nil)

	var out bytes.Buffer
	WarnActiveProfile(&out, connectionCommand(t, map[string]string{"limit": "5"}), typedProfiles(), plain)
	if got := strings.TrimSpace(out.String()); got != "[ shop-dev ]" {
		t.Errorf("a typed value the environment does not set said %q", got)
	}
}

// What is printed is a destination somebody typed, onto a stream that may be
// logged somewhere the command line is not. A URL loses its credentials and its
// query, and a long value is cut.
func TestTheBadgeNeverPrintsWhatARemoteAddressCarries(t *testing.T) {
	on(t, "shop-dev", nil)

	var out bytes.Buffer
	WarnActiveProfile(&out, connectionCommand(t, map[string]string{
		"host": "postgres://app:s3cret@db.other:5432/orders?sslmode=disable&token=abc",
	}), typedProfiles(), plain)
	got := out.String()
	for _, leaked := range []string{"s3cret", "app:", "token", "abc", "sslmode"} {
		if strings.Contains(got, leaked) {
			t.Errorf("the badge printed %q: %q", leaked, got)
		}
	}
	if !strings.Contains(got, "--host postgres://db.other:5432/orders") {
		t.Errorf("the badge did not say where the call was going: %q", got)
	}

	out.Reset()
	long := strings.Repeat("x", 200)
	WarnActiveProfile(&out, connectionCommand(t, map[string]string{"host": long}), typedProfiles(), plain)
	if strings.Contains(out.String(), long) || !strings.Contains(out.String(), "--host xxx") {
		t.Errorf("a long value was not cut: %q", out.String())
	}
}

// Naming the environment is said once: a command that names its own has said
// so itself, and the typed values are on its own line.
func TestTheBadgeStaysQuietWhenTheCommandNamesItsOwnEnvironment(t *testing.T) {
	on(t, "shop-dev", nil)
	cmd := connectionCommand(t, map[string]string{"host": "other.example"})
	if err := cmd.Flags().Set("profile", "shop-dev"); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	WarnActiveProfile(&out, cmd, typedProfiles(), plain)
	if out.Len() != 0 {
		t.Errorf("a command that named its environment printed %q", out.String())
	}
}

// A marked environment announces itself before every command, the ones that
// name another environment included. What that call replaces is the other
// environment's, which the badge is not about, so it is not read against this
// one's.
func TestTheBadgeDoesNotReadTypedValuesAgainstAnEnvironmentTheCommandDoesNotUse(t *testing.T) {
	on(t, "shop-prod", nil)
	cmd := connectionCommand(t, map[string]string{"host": "other.example"})
	if err := cmd.Flags().Set("profile", "shop-dev"); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	WarnActiveProfile(&out, cmd, typedProfiles(), plain)
	if got := strings.TrimSpace(out.String()); got != "[ shop-prod ]" {
		t.Errorf("a command using shop-dev said %q about shop-prod", got)
	}
}

// The wiring, not only the function: the flags a capability declares carry the
// key their value is filled from, so the root command's badge knows
// `--host` replaced the environment's `host:`. A declaration that left the
// annotation off would pass every test above and say nothing here.
func TestTheRootCommandNamesWhatTypedValuesReplace(t *testing.T) {
	saved := stderrIsTerminal
	t.Cleanup(func() { stderrIsTerminal = saved })
	stderrIsTerminal = func() bool { return true }
	const cfg = "profiles:\n  staging:\n    plugins:\n      db:\n        set:\n          host: db.staging\n"
	reg := setRegistry(t)
	on(t, "staging", nil)

	out, errOut := runSwitched(t, reg, cfg, "db", "status", "--host", "elsewhere", "--no-color")
	if !strings.Contains(out, "reached elsewhere") {
		t.Fatalf("the typed host did not win: %q", out)
	}
	if !strings.Contains(errOut, "[ staging, except --host elsewhere ]") {
		t.Errorf("the call went to elsewhere and the line above it said %q", errOut)
	}

	// A password typed is a credential, not a place, and is never part of it.
	_, errOut = runSwitched(t, reg, cfg, "db", "status", "--password", "hunter2", "--no-color")
	if strings.Contains(errOut, "hunter2") || strings.Contains(errOut, "except") {
		t.Errorf("a typed credential reached the badge: %q", errOut)
	}
}
