package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// tlsDemo is a capability for each of the ways the plugins carry TLS: a mode that
// takes one of a few words (postgres, mysql), a switch (etcd), an address whose
// scheme decides (vault, keycloak), and none.
func tlsDemo(id string, inputs ...plugin.Field) plugin.Capability {
	return plugin.Capability{ID: id, Summary: "demo", Safety: plugin.Read, Inputs: inputs,
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }}
}

// usageOf runs one capability's command line and returns the usage error it is
// refused with.
func usageOf(t *testing.T, c plugin.Capability, args ...string) *view.Error {
	t.Helper()
	root := &cobra.Command{Use: "rta", SilenceUsage: true, SilenceErrors: true}
	attach(root, c, &globalOpts{})
	codeUsageErrors(root)
	root.SetArgs(append(slices.Clone(c.Words()[1:]), args...))
	err := root.Execute()
	var ve *view.Error
	if !errors.As(err, &ve) {
		t.Fatalf("%v: refused with %v, want a usage error", args, err)
	}
	return ve
}

// `--tls`, `--ssl` and `--https` were answered with the nearest spelling, which
// for `rta pg status --tls` was `--tls-server-name`, an input that does nothing
// until TLS is on. The capability says which input does turn it on.
func TestAFlagThatSaysTLSIsAnsweredWithTheInputThatTurnsItOn(t *testing.T) {
	mode := plugin.Field{Name: "sslmode", Type: plugin.String, Local: true, Endpoint: plugin.EndpointTLS,
		Options: []string{"disable", "require", "verify-full"}}
	adjacent := plugin.Field{Name: "tls-server-name", Type: plugin.String, TLSAdjacent: true}
	address := plugin.Field{Name: "address", Type: plugin.String, Local: true, Endpoint: plugin.EndpointURL}

	for _, word := range []string{"tls", "ssl", "https"} {
		hint := usageOf(t, tlsDemo("demo.db.status", mode, adjacent), "--"+word).Hint
		if !strings.Contains(hint, "--sslmode turns TLS on") || !strings.Contains(hint, "disable, require, verify-full") ||
			strings.Contains(hint, "tls-server-name") {
			t.Errorf("--%s on a capability with a TLS mode: hint = %q", word, hint)
		}
	}
	hint := usageOf(t, tlsDemo("demo.vault.status", address, adjacent), "--tls").Hint
	if !strings.Contains(hint, "https:// address in --address") || strings.Contains(hint, "tls-server-name") {
		t.Errorf("--tls on a capability whose address carries the scheme: hint = %q", hint)
	}
	// A capability with no notion of TLS keeps the answer it had, and so does a
	// flag that is not about TLS.
	if hint := usageOf(t, tlsDemo("demo.sys.cpu"), "--tls").Hint; strings.Contains(hint, "turns TLS on") {
		t.Errorf("a capability with no TLS input claims one: %q", hint)
	}
	if hint := usageOf(t, tlsDemo("demo.db.status", mode), "--nope").Hint; strings.Contains(hint, "turns TLS on") {
		t.Errorf("a flag that is not about TLS was answered about TLS: %q", hint)
	}
}

// A switch is on by being named, so `--tls true` leaves "true" over, and the
// refusal was "unexpected argument" under a list of the flags that take a value.
func TestASwitchGivenAValueIsToldItNeedsNone(t *testing.T) {
	etcd := tlsDemo("demo.etcd.overview", plugin.Field{Name: "tls", Type: plugin.Bool, Local: true, Endpoint: plugin.EndpointTLS},
		plugin.Field{Name: "endpoint", Type: plugin.String})
	ve := usageOf(t, etcd, "--tls", "true")
	if want := "a switch needs no value: `--tls` alone turns it on, `--tls=false` off"; ve.Hint != want {
		t.Errorf("--tls true: hint = %q, want %q", ve.Hint, want)
	}
	// A stray word that is no boolean keeps the hint it had, and so does a
	// boolean word with no switch before it.
	if hint := usageOf(t, etcd, "--tls", "prod").Hint; strings.Contains(hint, "switch") {
		t.Errorf("a word that is no boolean was told about switches: %q", hint)
	}
	if hint := usageOf(t, etcd, "true").Hint; strings.Contains(hint, "switch") {
		t.Errorf("a boolean word with no switch before it was told about one: %q", hint)
	}
}

// A flag that takes a value swallows the next word whatever it is, so `--tls
// --host db` gave --tls the value "--host" and refused "db"; with nothing after,
// it ran with a TLS mode called "--host".
func TestAFlagThatSwallowedTheNextFlagSaysItNeedsAValue(t *testing.T) {
	mysql := tlsDemo("demo.mysql.status",
		plugin.Field{Name: "tls", Type: plugin.String, Options: []string{"false", "preferred", "true"}},
		plugin.Field{Name: "host", Type: plugin.String},
		plugin.Field{Name: "database", Type: plugin.String})
	for _, args := range [][]string{{"--tls", "--host", "db"}, {"--tls", "--host"}, {"--tls", "--host=db"}} {
		ve := usageOf(t, mysql, args...)
		if ve.Message != "--tls needs a value, and the next word, "+args[1]+", looks like a flag" ||
			!strings.Contains(ve.Hint, "--tls takes one of false, preferred, true") {
			t.Errorf("%v: %q / %q", args, ve.Message, ve.Hint)
		}
	}
	// No options declared: the hint says where the value goes, and a value that
	// is not the spelling of a flag is a value.
	free := tlsDemo("demo.mysql.query", plugin.Field{Name: "note", Type: plugin.String}, plugin.Field{Name: "host", Type: plugin.String})
	if hint := usageOf(t, free, "--note", "--host", "db").Hint; !strings.Contains(hint, "its value first") {
		t.Errorf("a flag with no options: hint = %q", hint)
	}
	root := &cobra.Command{Use: "rta", SilenceUsage: true, SilenceErrors: true}
	attach(root, mysql, &globalOpts{})
	root.SetArgs([]string{"status", "--note-to-self", "x"})
	if err := root.Execute(); err == nil || strings.Contains(err.Error(), "needs a value") {
		t.Errorf("a flag that does not exist was told it needs a value: %v", err)
	}
}

// `--json` is the first thing typed by anyone who has used a tool that has it,
// and the format here is a value of the one flag every command takes.
func TestAFlagThatNamesAnOutputFormatIsAnsweredWithTheOutputFlag(t *testing.T) {
	cpu := tlsDemo("demo.sys.cpu", plugin.Field{Name: "cores", Type: plugin.Bool})
	for flag, want := range map[string]string{
		"--json": "-o json", "--yaml": "-o yaml", "--csv": "-o csv", "--md": "-o md",
		"--format": "-o <format>",
	} {
		ve := usageOf(t, cpu, flag)
		if !strings.Contains(ve.Hint, want) || !strings.Contains(ve.Hint, "rta sys cpu --help") {
			t.Errorf("%s: hint = %q, want it to say %q and keep the pointer to --help", flag, ve.Hint, want)
		}
		if ve.Message != "unknown flag: "+flag {
			t.Errorf("%s: message = %q", flag, ve.Message)
		}
	}
	// `--format json` is the same mistake with its value after it.
	if ve := usageOf(t, cpu, "--format", "json"); !strings.Contains(ve.Hint, "-o <format>") {
		t.Errorf("--format json: hint = %q", ve.Hint)
	}
	// A capability that has a flag of that very name keeps it.
	own := tlsDemo("demo.sys.cpu", plugin.Field{Name: "format", Type: plugin.String})
	root := &cobra.Command{Use: "rta", SilenceUsage: true, SilenceErrors: true}
	attach(root, own, &globalOpts{})
	root.SetArgs([]string{"sys", "cpu", "--format", "x"})
	if err := root.Execute(); err != nil {
		t.Errorf("a capability's own --format was refused: %v", err)
	}
}

// A list flag swallows the next flag as a string flag does, and a flag's
// one-letter form is a flag's spelling too, a persistent one included.
func TestAListFlagOrAShorthandSwallowedSaysTheFlagNeedsAValue(t *testing.T) {
	ports := tlsDemo("demo.net.port", plugin.Field{Name: "host", Type: plugin.String, Positional: true},
		plugin.Field{Name: "ports", Type: plugin.StringSlice}, plugin.Field{Name: "timeout", Type: plugin.String})
	for _, args := range [][]string{{"localhost", "--ports", "--timeout", "2s"}, {"localhost", "--ports", "-o", "json"}} {
		root := &cobra.Command{Use: "rta", SilenceUsage: true, SilenceErrors: true}
		root.PersistentFlags().StringP("output", "o", "", "how to print it")
		attach(root, ports, &globalOpts{})
		codeUsageErrors(root)
		root.SetArgs(append(slices.Clone(ports.Words()[1:]), args...))
		var ve *view.Error
		if err := root.Execute(); !errors.As(err, &ve) || ve.Message != "--ports needs a value, and the next word, "+args[2]+", looks like a flag" {
			t.Errorf("%v: refused with %v, want --ports to need a value", args, err)
		}
	}
	// A value that only starts with a dash is a value: the stdin marker, a
	// negative number.
	free := tlsDemo("demo.net.port", plugin.Field{Name: "note", Type: plugin.String}, plugin.Field{Name: "ports", Type: plugin.StringSlice})
	for _, word := range []string{"-", "-5"} {
		root := &cobra.Command{Use: "rta", SilenceUsage: true, SilenceErrors: true}
		attach(root, free, &globalOpts{})
		codeUsageErrors(root)
		root.SetArgs(append(slices.Clone(free.Words()[1:]), "--ports", word))
		if err := root.Execute(); err != nil {
			t.Errorf("--ports %s: refused with %v, want it taken as a value", word, err)
		}
	}
}
