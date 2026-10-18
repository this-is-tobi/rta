package all

import (
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A length of time is written with its unit, everywhere. `--timeout` was a
// number of seconds in twenty-one built-in inputs and `--ttl` and `--refresh` a
// duration, so one command line held both grammars, and a bare `30` meant what
// the help beside it said. A built-in that needs a time declares a
// plugin.Duration, which refuses a number that carries no unit.
func TestNoBuiltinDeclaresALengthOfTimeAsANumber(t *testing.T) {
	reg, err := Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	timeWords := []string{"timeout", "wait", "ttl", "interval", "delay", "duration", "age"}
	for _, c := range reg.Capabilities() {
		for _, f := range c.Inputs {
			words := strings.Split(f.Name, "-")
			if (f.Type == plugin.Int || f.Type == plugin.Float) && slices.Contains(timeWords, words[len(words)-1]) {
				t.Errorf("%s declares %q as a %s: a length of time is a plugin.Duration", c.ID, f.Name, f.Type)
			}
		}
	}
}
