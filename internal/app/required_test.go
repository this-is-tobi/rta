package app

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginconf"
)

// A required input with nothing in it is refused before the handler, with the
// host's code and the input named as a flag or an argument — whichever layer
// left it empty. The CLI's own check covered an input the config can fill and
// counted an empty one as missing; cobra's covered the rest by presence
// alone, so `rta demo item pick ""` handed the handler a name it would read
// exactly as no name at all.
func TestAnEmptyRequiredInputIsRefusedOnTheCLI(t *testing.T) {
	reg, origin := configCapability(t)
	t.Cleanup(func() { SetPluginConfig(nil, nil) })

	SetPluginConfig(pluginconf.Resolve(trustedConfig(t, config.Config{Plugins: map[string]map[string]any{
		"pg@" + origin.Short(): {"host": ""},
	}}), reg.Origin))
	for _, args := range [][]string{{"pg", "query"}, {"pg", "query", "--host", ""}} {
		out, errOut, err := run(t, reg, args...)
		if err == nil {
			t.Fatalf("%v ran with an empty host:\n%s", args, out)
		}
		for _, want := range []string{"core.input.missing", "pg.query needs --host",
			"pass --host, or set host in your rta config"} {
			if !strings.Contains(errOut, want) {
				t.Errorf("%v: the refusal does not say %q:\n%s", args, want, errOut)
			}
		}
	}

	out, errOut, err := run(t, testRegistry(t), "demo", "item", "pick", "")
	if err == nil {
		t.Fatalf("an empty required argument ran:\n%s", out)
	}
	for _, want := range []string{"core.input.missing", "demo.item.pick needs <name>", "rta demo item pick --help"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, errOut)
		}
	}
	if ExitCode(err) != 1 {
		t.Errorf("exit %d, want 1: the command line was well formed", ExitCode(err))
	}
}
