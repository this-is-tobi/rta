package app

import (
	"strings"
	"testing"
)

// The root command has always answered a typo with a suggestion; every group
// below it lost that the day it grew a RunE of its own, so `rta sy cpu`
// suggested `sys` while `rta sys cpuu` only said unknown. One level down is
// where the typos actually happen.
func TestAnUnknownVerbBelowTheRootSuggestsTheNearest(t *testing.T) {
	_, _, err := run(t, testRegistry(t), "demo", "item", "lst")
	if err == nil {
		t.Fatal("`demo item lst` succeeded")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("error does not say the command is unknown: %v", err)
	}
	if !strings.Contains(err.Error(), `"list"`) {
		t.Errorf("error does not suggest `list`:\n%v", err)
	}
	if got := ExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2 (usage)", got)
	}
}

// A usage error reaches the reader through fang, which supplies the full stop
// itself — it renders err.Error() + "." — so a message carrying its own
// newlines gets that stop wherever its last line happened to end. cobra's
// suggestion block is exactly that shape, and this is what shipped:
//
//	Unknown command "kvv" for "rta"
//
//	Did you mean this?
//	    kv
//	.
//
// at the root, and `…get⏎    set.` one level down, where the stop attaches
// itself to a command name. The suggestion is the useful half of the message
// and it is the half the stray character lands on.
//
// So: one line, and the same line at every depth. An unknown command is one
// failure whether it is typed after `rta` or after `rta kv`, and the two were
// worded by different code — cobra's legacyArgs above, rta's groupRunE below
// — which is how one of them could be shaped for a renderer the other had
// never met.
func TestAnUnknownCommandIsOneSentenceAtEveryDepth(t *testing.T) {
	for _, c := range []struct {
		where string
		args  []string
		near  string
	}{
		{"at the root", []string{"demoo"}, `"demo"`},
		{"below it", []string{"demo", "item", "lst"}, `"list"`},
	} {
		_, _, err := run(t, testRegistry(t), c.args...)
		if err == nil {
			t.Fatalf("%s: `rta %s` succeeded", c.where, strings.Join(c.args, " "))
		}
		msg := err.Error()
		if strings.Contains(msg, "\n") {
			t.Errorf("%s: the message is more than one line, so the renderer's full stop "+
				"lands inside it rather than at the end of the sentence:\n%q", c.where, msg)
		}
		if strings.HasSuffix(msg, ".") || strings.HasSuffix(msg, "?") {
			t.Errorf("%s: the message punctuates itself, and the renderer adds a stop of "+
				"its own after it: %q", c.where, msg)
		}
		if !strings.Contains(msg, c.near) {
			t.Errorf("%s: the message does not name the nearest command %s: %q", c.where, c.near, msg)
		}
		if !strings.Contains(msg, "closest match") {
			t.Errorf("%s: %q — both depths say the same thing about a near miss, or the "+
				"wording drifts again the next time one of them is touched", c.where, msg)
		}
	}
}
