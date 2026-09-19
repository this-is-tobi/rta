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
}

// The other half of the same question, and it was answered by a sentence
// written for one case only: "one argument too many, %q" counted in a word
// and named args[len(fields)], so three extra arguments were reported as one,
// and the two the reader still had to find were not mentioned at all.
//
// A count that disagrees with the list beside it is the defect the counting
// vocabulary in pkg/format exists to stop, so the count comes from there —
// and what the reader actually needs is not the arithmetic but which words to
// delete, so every extra argument is named.
func TestEveryUnexpectedArgumentIsNamed(t *testing.T) {
	reg := testRegistry(t)
	_, _, err := run(t, reg, "demo", "item", "pick", "alpha", "extra")
	if err == nil || !strings.Contains(err.Error(), `unexpected argument "extra" — usage: rta demo item pick <name>`) {
		t.Fatalf("one extra: err = %v", err)
	}

	_, _, err = run(t, reg, "demo", "item", "pick", "alpha", "one", "two", "three")
	if err == nil {
		t.Fatal("three extra arguments were accepted")
	}
	if !strings.Contains(err.Error(), `unexpected arguments "one", "two", "three"`) {
		t.Fatalf("three extra: err = %v", err)
	}
}
