package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func completionsFor(t *testing.T, args ...string) []string {
	t.Helper()
	reg := configRegistry(t)
	root := NewRoot(reg, "test")
	cmd, rest, err := root.Find(args)
	if err != nil {
		t.Fatal(err)
	}
	got, directive := cmd.ValidArgsFunction(cmd, rest, "")
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 {
		t.Errorf("%v offers files", args)
	}
	return names(got)
}

// Every key the command takes is offered, the file's own and each plugin's, so
// a person never has to know the spelling before they type it.
func TestConfigSetCompletesEveryKeyItTakes(t *testing.T) {
	got := completionsFor(t, "config", "set")
	for _, want := range []string{"output", "dashboard.columns", "dashboard.hidden", "dashboard.order",
		"theme.primary", "plugins.net.timeout", "plugins.net.ping.count", "plugins.gen.password.symbols"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s is not offered: %v", want, got)
		}
	}
	for _, key := range got {
		if _, verr := resolveConfigKey(configRegistry(t), key); verr != nil {
			t.Errorf("%s is offered and refused: %v", key, verr.Message)
		}
	}
	if got := completionsFor(t, "config", "get"); !slices.Contains(got, "output") {
		t.Errorf("get offers %v", got)
	}
	if got := completionsFor(t, "config", "unset"); !slices.Contains(got, "plugins.net.timeout") {
		t.Errorf("unset offers %v", got)
	}
}

// Once the key is on the line, the values it takes are what is offered.
func TestConfigSetCompletesTheValuesAKeyTakes(t *testing.T) {
	reg := configRegistry(t)
	root := NewRoot(reg, "test")
	set, _, _ := root.Find([]string{"config", "set"})
	for key, want := range map[string][]string{
		"output":                       {"pretty", "json", "yaml", "csv", "md"},
		"plugins.net.encoding":         {"b64", "hex"},
		"plugins.gen.password.symbols": {"true", "false"},
	} {
		got, _ := set.ValidArgsFunction(set, []string{key}, "")
		if !slices.Equal(names(got), want) {
			t.Errorf("%s completes %v, want %v", key, names(got), want)
		}
	}
	if got, _ := set.ValidArgsFunction(set, []string{"plugins.net.timeout"}, ""); len(got) != 0 {
		t.Errorf("a free number is offered values: %v", got)
	}
}

func TestExplainEndsAPluginsKeysWithTheLineThatSetsOne(t *testing.T) {
	reg := configRegistry(t)
	out, _, err := run(t, reg, "explain", "net.ping", "-o", "pretty")
	if err != nil {
		t.Fatal(err)
	}
	if want := "rta config set plugins.net.timeout 10"; !strings.Contains(out, want) {
		t.Errorf("the card does not end with %q:\n%s", want, out)
	}
	// And the line it ends with is one that works.
	if _, errOut, _, err := configRun(t, reg, "", "config", "set", "plugins.net.timeout", "10"); err != nil {
		t.Errorf("the line on the card was refused: %v\n%s", err, errOut)
	}
	out, _, _ = run(t, reg, "explain", "gen.overview", "-o", "pretty")
	if strings.Contains(out, "set a key") {
		t.Errorf("a card with no config key offers one:\n%s", out)
	}
}
