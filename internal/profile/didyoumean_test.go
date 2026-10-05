package profile

import (
	"strings"
	"testing"
)

// A key one slip from a real one is told what it was meant to be, instead of
// being sent to read every capability's card for a spelling it nearly had.
func TestAMisspeltSetKeyIsToldWhatItWasMeantFor(t *testing.T) {
	cfg := load(t, "profiles:\n  staging:\n    plugins:\n      db:\n        set:\n          mdoe: fast\n")
	problems := Check(cfg, tlsRegistry(t))
	if len(problems) != 1 {
		t.Fatalf("want one problem, got %v", problems)
	}
	if !strings.Contains(problems[0].Reason, `nothing in db reads "mdoe"`) ||
		!strings.Contains(problems[0].Hint, `did you mean "mode"?`) {
		t.Errorf("got %q / %q", problems[0].Reason, problems[0].Hint)
	}
}

func TestASetKeyNothingResemblesKeepsThePointerToExplain(t *testing.T) {
	cfg := load(t, "profiles:\n  staging:\n    plugins:\n      db:\n        set:\n          zzzzzzzz: x\n")
	problems := Check(cfg, tlsRegistry(t))
	if len(problems) != 1 || !strings.Contains(problems[0].Hint, "rta explain") {
		t.Fatalf("got %v", problems)
	}
}
