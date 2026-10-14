package theme

import (
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// Form is the look of every form in the TUI, drawn from the palette that is in
// force: the pane-name colour for titles, the identity colour for the rail, the
// selector, the chosen option and the cursor, the failure colour for an error.
//
// It exists because a form is where a write is typed, which is where the colour
// of the environment matters most, and the form library's own theme (indigo,
// fuchsia, a green selection) did not know the palette: the picker's selected row
// was the colour of "ok" in every environment, and the profile that was switched
// on to be recognised changed everything around a form and nothing inside it.
//
// Read when a form is built and not when it is drawn, as every form is: the
// environment cannot change while one is open. isDark is ignored on purpose, the
// palette being chosen to read on both kinds of terminal (see primaryHex).
func Form(isDark bool) *huh.Styles {
	t := huh.ThemeBase(isDark)

	// Ink is chosen the way a badge's is, by the luminance of what it sits on, so
	// the button is readable on whatever colour an environment gave the identity.
	ink := lipgloss.Color(inverseHex)
	if relativeLuminance(Current()["primary"]) > 0.179 {
		ink = lipgloss.Color(inkHex)
	}

	t.Focused.Base = t.Focused.Base.BorderForeground(Primary)
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = t.Focused.Title.Foreground(Label).Bold(true)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(Label).Bold(true).MarginBottom(1)
	t.Focused.Directory = t.Focused.Directory.Foreground(Label)
	t.Focused.Description = t.Focused.Description.Foreground(Muted)
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(Bad)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(Bad)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(Primary)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(Primary)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(Primary)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(Primary)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(Primary)
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(Good).SetString("✓ ")
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(Muted).SetString("• ")
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(ink).Background(Primary)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(Muted).Background(Faint)
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(Primary)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(Muted)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(Primary)

	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description
	return t
}

// FormMono is Form for a terminal that shows no colour (NO_COLOR, TERM=dumb).
//
// A yes/no box tells its answer by the fill of one of two buttons and by
// nothing else, and with the fill taken away both read `yes     no`: a person
// pressing enter on a to-do or an allow box did not know which they had
// chosen. The chosen button is drawn between brackets, the mark the active
// environment's name takes when there is no colour to be a badge (Mark), and
// as text rather than as an attribute, so it survives a terminal that draws
// none and a copy of the screen. Every other part of a form already carries its
// state in a glyph: the pickers' selector, the check marks of a multiple choice,
// the focused field's heavy bar.
func FormMono(isDark bool) *huh.Styles {
	t := Form(isDark)
	bracketed := func(s string) string { return "[ " + s + " ]" }
	t.Focused.FocusedButton = t.Focused.FocusedButton.Transform(bracketed)
	t.Blurred.FocusedButton = t.Blurred.FocusedButton.Transform(bracketed)
	return t
}
