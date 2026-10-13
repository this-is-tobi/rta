package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// exampleCommands is the command lines a command's own Example holds, one per
// entry: a line that ends in a backslash continues on the next, a trailing
// comment is kept for commandWords to drop, and the indent help gives is gone.
func exampleCommands(example string) []string {
	var out []string
	var line strings.Builder
	for _, raw := range strings.Split(example, "\n") {
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		if rest, continued := strings.CutSuffix(text, `\`); continued {
			line.WriteString(strings.TrimSpace(rest) + " ")
			continue
		}
		line.WriteString(text)
		out = append(out, line.String())
		line.Reset()
	}
	return out
}

// An example in a command's own help is only worth printing if it parses: the
// flags are ones it has, the arguments are as many as it takes. The map in
// examples.go is held to this by TestEveryExampleParsesAgainstTheRealTree; the
// ones a command carries as its own Example are not in that map, and were held
// to nothing.
func TestEveryExampleACommandCarriesParsesAgainstTheRealTree(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	seen := 0
	walkCommands(NewRoot(reg, "test"), func(c *cobra.Command) {
		if _, inMap := examples[c.CommandPath()]; inMap || c.Example == "" {
			return
		}
		for _, line := range exampleCommands(c.Example) {
			words := commandWords(line)
			if len(words) == 0 {
				continue
			}
			seen++
			if words[0] != "rta" {
				t.Errorf("%s: %q does not start with rta", c.CommandPath(), line)
				continue
			}
			cmd, rest, err := NewRoot(reg, "test").Find(words[1:])
			if err != nil {
				t.Errorf("%s: %q: %v", c.CommandPath(), line, err)
				continue
			}
			if err := cmd.ParseFlags(rest); err != nil {
				t.Errorf("%s: %q: %v", c.CommandPath(), line, err)
				continue
			}
			if err := cmd.ValidateArgs(cmd.Flags().Args()); err != nil {
				t.Errorf("%s: %q: %v", c.CommandPath(), line, err)
			}
			if err := cmd.ValidateRequiredFlags(); err != nil {
				t.Errorf("%s: %q: %v", c.CommandPath(), line, err)
			}
		}
	})
	if seen == 0 {
		t.Fatal("no command carries an Example of its own: the walk found nothing to hold")
	}
}

// pgish declares the keys the example in `rta profile set --help` uses, as pg
// declares them: a host, a port, a database and a credential.
func pgish(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "pg", Summary: "pg",
		Capabilities: []plugin.Capability{{
			ID: "pg.status", Summary: "status", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Default: "localhost", Config: "host", Local: true,
					Endpoint: plugin.EndpointHost},
				{Name: "port", Type: plugin.Int, Default: 5432, Config: "port", Local: true,
					Endpoint: plugin.EndpointPort, Min: 1, Max: 65535},
				{Name: "database", Type: plugin.String, Config: "database"},
				{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true},
			},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// **An example that fails when it is run is worse than none.** The help of
// `rta profile set` showed `--only dbname=app` for pg, whose key is `database`,
// and running it was refused: the one example of the flag did not work. Every
// line of it that is a `rta profile ...` command is run, in order, against a
// registry that declares the keys the line uses — so a key renamed under it, or
// a flag that no longer takes what it is shown taking, fails here.
func TestEveryProfileSetExampleIsAcceptedAsWritten(t *testing.T) {
	run := session(t, pgish(t))
	root := NewRoot(pgish(t), "test")
	cmd, _, err := root.Find([]string{"profile", "set"})
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, line := range exampleCommands(cmd.Example) {
		words := commandWords(line)
		if len(words) < 3 || words[0] != "rta" || words[1] != "profile" || words[2] != "set" {
			continue
		}
		ran++
		if _, errOut, err := run(words[1:]...); err != nil {
			t.Errorf("%q was refused as written: %v\n%s", line, err, errOut)
		}
	}
	if ran == 0 {
		t.Fatal("the help of `profile set` shows no `rta profile set` line")
	}
}
