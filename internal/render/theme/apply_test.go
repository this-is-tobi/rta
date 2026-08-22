package theme

import (
	"fmt"
	"image/color"
	"strings"
	"testing"
)

// toHex renders a color.Color back to "#rrggbb", so a test can compare what
// a style actually holds against the hex an override was made from. RGBA
// returns 16-bit premultiplied channels; >> 8 is the standard way back to
// the 8-bit value a hex digit pair encodes.
func toHex(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02X%02X%02X", r>>8, g>>8, b>>8)
}

// reset restores the built-in palette after a test that calls Apply, so a
// mutated package var never leaks into a test that runs after it — this
// package has no per-test isolation otherwise, since the palette is exactly
// the kind of state a renderer reads from a bare package var.
func reset(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { Apply(nil) })
}

func TestApplyOverridesAKnownFieldAndRebuildsItsStyles(t *testing.T) {
	reset(t)
	const custom = "#123456"
	problems := Apply(map[string]string{"primary": custom})
	if len(problems) != 0 {
		t.Fatalf("a valid override was reported as a problem: %v", problems)
	}
	if got := Current()["primary"]; got != custom {
		t.Errorf("Current()[primary] = %q, want %q", got, custom)
	}
	// Key, Header and Title all derive from Primary (rebuildStyles). A style
	// var assigned once at package init and never rebuilt would still show
	// the built-in color here, which is exactly the bug a var-block
	// initializer produces: it runs before Apply exists to call.
	if got := Key.GetForeground(); toHex(got) != custom {
		t.Errorf("Key's foreground = %s, want %s (rebuildStyles did not run)", toHex(got), custom)
	}
	if got := Title.GetForeground(); toHex(got) != custom {
		t.Errorf("Title's foreground = %s, want %s", toHex(got), custom)
	}
}

func TestApplyResetsFieldsTheNewCallDoesNotMention(t *testing.T) {
	reset(t)
	Apply(map[string]string{"primary": "#123456"})
	// A second call naming only bad does not owe primary anything — it must
	// come back to the built-in, not keep the first call's value. Without
	// the reset-every-field-first step, this is the mutation that survives:
	// override once, override something else, and the first override
	// lingers forever.
	Apply(map[string]string{"bad": "#654321"})
	if got := Current()["primary"]; got != primaryHex {
		t.Errorf("primary = %q after a call that did not mention it, want the built-in %q",
			got, primaryHex)
	}
	if got := Current()["bad"]; got != "#654321" {
		t.Errorf("bad = %q, want the override this call did make", got)
	}
}

func TestApplyRejectsAMalformedHexAndKeepsTheBuiltin(t *testing.T) {
	reset(t)
	problems := Apply(map[string]string{"primary": "not-a-color"})
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want exactly one", problems)
	}
	if problems[0].Field != "primary" {
		t.Errorf("Field = %q, want primary", problems[0].Field)
	}
	if !strings.Contains(problems[0].Reason, "not-a-color") {
		t.Errorf("Reason %q does not name the bad value", problems[0].Reason)
	}
	if got := Current()["primary"]; got != primaryHex {
		t.Errorf("primary = %q, want the built-in %q left in place", got, primaryHex)
	}
}

func TestApplyRejectsAnUnknownKeyAndNamesTheValidOnes(t *testing.T) {
	reset(t)
	problems := Apply(map[string]string{"chartreuse": "#00FF00"})
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want exactly one", problems)
	}
	if problems[0].Field != "chartreuse" {
		t.Errorf("Field = %q, want chartreuse", problems[0].Field)
	}
	for _, f := range Fields() {
		if !strings.Contains(problems[0].Hint, f) {
			t.Errorf("hint %q does not list valid field %q", problems[0].Hint, f)
		}
	}
}

func TestCurrentReflectsEveryBuiltinWhenNothingIsOverridden(t *testing.T) {
	reset(t)
	Apply(nil)
	got := Current()
	want := map[string]string{
		"primary": primaryHex, "accent": accentHex, "muted": mutedHex, "faint": faintHex,
		"label": labelHex, "good": goodHex, "warn": warnHex, "bad": badHex,
		"inverse": inverseHex, "ink": inkHex,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("Current()[%q] = %q, want the built-in %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("Current() has %d fields, want %d — Fields() and the built-in map disagree",
			len(got), len(want))
	}
}
