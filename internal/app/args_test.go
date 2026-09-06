package app

import (
	"strings"
	"testing"
)

// cobra's "requires at least 1 arg(s), only received 0" named neither the
// thing missing nor the command to type.
func TestAMissingArgumentIsNamedWithTheLineToType(t *testing.T) {
	_, _, err := run(t, testRegistry(t), "demo", "item", "pick")
	if err == nil || !strings.Contains(err.Error(), "missing <name> — usage: rta demo item pick <name>") {
		t.Fatalf("err = %v", err)
	}
	_, _, err = run(t, testRegistry(t), "demo", "item", "pick", "alpha", "extra")
	if err == nil || !strings.Contains(err.Error(), `one argument too many, "extra"`) {
		t.Fatalf("err = %v", err)
	}
}
