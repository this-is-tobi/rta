package theme

import (
	"image/color"
	"testing"
)

func sameColour(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == b
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// A form is where a write is typed, which is the moment the colour of the
// environment matters most, and it was drawn in the form library's own indigo,
// fuchsia and green whatever environment was on and whatever the operator had
// chosen: the picker's selected row was the green of "ok". It is drawn from the
// palette now, so it repaints with the screen it opens on.
func TestAFormIsDrawnInThePalette(t *testing.T) {
	defer Apply(nil)
	Apply(map[string]string{"primary": "#44AA99", "label": "#C792EA"})
	s := Form(true)
	for what, pair := range map[string][2]color.Color{
		"the title is the pane-name colour":          {s.Focused.Title.GetForeground(), Label},
		"the selector is the identity colour":        {s.Focused.SelectSelector.GetForeground(), Primary},
		"the chosen option is the identity colour":   {s.Focused.SelectedOption.GetForeground(), Primary},
		"an error is the failure colour":             {s.Focused.ErrorMessage.GetForeground(), Bad},
		"the cursor is the identity colour":          {s.Focused.TextInput.Cursor.GetForeground(), Primary},
		"the description is the secondary text":      {s.Focused.Description.GetForeground(), Muted},
		"the focused button is filled with identity": {s.Focused.FocusedButton.GetBackground(), Primary},
	} {
		if !sameColour(pair[0], pair[1]) {
			t.Errorf("%s: got %v, want %v", what, pair[0], pair[1])
		}
	}
}

// What the focused button says is readable on whatever the identity colour is:
// ink dark or light by the colour's luminance, as a badge picks it.
func TestTheFocusedButtonCanBeReadOnAnyIdentityColour(t *testing.T) {
	defer Apply(nil)
	for hex, want := range map[string]color.Color{"#FFE066": Ink, "#2B3A67": Inverse} {
		Apply(map[string]string{"primary": hex})
		if got := Form(true).Focused.FocusedButton.GetForeground(); !sameColour(got, want) {
			t.Errorf("primary %s: button text %v, want %v", hex, got, want)
		}
	}
}

// Dark or light terminal, the same palette: it is chosen to read on both.
func TestAFormLooksTheSameOnALightAndADarkTerminal(t *testing.T) {
	d, l := Form(true), Form(false)
	if !sameColour(d.Focused.Title.GetForeground(), l.Focused.Title.GetForeground()) ||
		!sameColour(d.Focused.SelectedOption.GetForeground(), l.Focused.SelectedOption.GetForeground()) {
		t.Error("the palette is drawn differently for the two kinds of terminal")
	}
}
