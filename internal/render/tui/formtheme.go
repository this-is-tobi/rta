package tui

import (
	"charm.land/huh/v2"

	"github.com/this-is-tobi/rta/internal/render/theme"
)

// formTheme is the look every form in the TUI is built with, drawn from the
// palette in force (theme.Form). A form built without it is drawn in the form
// library's own indigo and green, whatever environment is on.
//
// A function and not a package-level variable, which the palette's own guard
// refuses for anything that touches it: nothing here is computed until a form is
// drawn.
func formTheme() huh.Theme { return huh.ThemeFunc(theme.Form) }
