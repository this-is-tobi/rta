package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Every form the TUI builds takes the palette's look. A form added without it
// would be the one place the environment's colour stops, and nothing on the
// screen would say why, so the rule is held where the forms are made.
func TestEveryFormTheTUIBuildsIsDrawnInThePalette(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forms, themed := 0, 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		forms += strings.Count(string(b), "huh.NewForm(")
		// capForm's own is formTheme with what the model learned of the terminal.
		themed += strings.Count(string(b), "WithTheme(formTheme())") + strings.Count(string(b), "WithTheme(cf.theme())")
	}
	if forms == 0 {
		t.Fatal("found no form; the scan reads the wrong files")
	}
	if forms != themed {
		t.Errorf("%d forms are built and %d take the palette's look: every huh.NewForm needs .WithTheme(formTheme())", forms, themed)
	}
}

// With no colour the fill that says which button of a yes/no box is chosen is
// gone, and both answers read the same: the chosen one is between brackets, and
// is the other one after the answer changes. With colour nothing is added.
func TestTheChosenAnswerOfAYesNoBoxIsReadableWithoutColour(t *testing.T) {
	todo := plugin.Capability{
		ID: "demo.mark", Summary: "mark it", Safety: plugin.Write,
		Inputs: []plugin.Field{{Name: "todo", Type: plugin.Bool, Help: "make it a to-do"}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "marked"}, nil },
	}
	drawn := func(noColor bool) (Model, string) {
		m := New(fastFormRegistry(t, todo), config.Dashboard{}, nil)
		m.noColor = noColor
		m = openFormOn(t, m, todo)
		return m, ansi.Strip(m.formView())
	}

	m, got := drawn(true)
	if !strings.Contains(got, "[ no ]") || strings.Contains(got, "[ yes ]") {
		t.Errorf("with no colour the default answer is not marked:\n%s", got)
	}
	m.form.form = settleForm(m.form.form, tea.KeyPressMsg{Code: 'l', Text: "l"})
	if got := ansi.Strip(m.formView()); !strings.Contains(got, "[ yes ]") || strings.Contains(got, "[ no ]") {
		t.Errorf("after choosing yes the mark did not move:\n%s", got)
	}

	if _, got := drawn(false); strings.Contains(got, "[ no ]") || strings.Contains(got, "[ yes ]") {
		t.Errorf("a terminal with colour was given brackets as well:\n%s", got)
	}
}
