package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A form is longer than its panel when it has a dozen boxes, and a web form
// says how long it is and where you are. Its head also says the one thing the
// glyph in its title only hints at: that the call writes.

func positionCap(safety plugin.Safety, names ...string) plugin.Capability {
	c := plugin.Capability{
		ID: "demo.pos", Summary: "do a thing", Safety: safety,
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
	}
	for _, n := range names {
		c.Inputs = append(c.Inputs, plugin.Field{Name: n, Type: plugin.String, Required: true})
	}
	return c
}

// headOf is the form's top border, the row the panel's head is drawn in.
func headOf(m Model) string {
	return strings.SplitN(plain(m.formView()), "\n", 2)[0]
}

func sizedForm(t *testing.T, c plugin.Capability, w, h int) Model {
	t.Helper()
	m := openFormOn(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), c)
	// What open sets before it asks for the form, and the head is drawn from.
	m.current = c
	m.width, m.height = w, h
	m.fitForm()
	return m
}

func TestAWriteFormsHeadSaysWriteAndWhereTheCursorIs(t *testing.T) {
	m := sizedForm(t, positionCap(plugin.Write, "a", "b", "c"), 100, 30)
	if head := headOf(m); !strings.Contains(head, "write · 1 of 3") {
		t.Errorf("head = %q, want write · 1 of 3", head)
	}
	m = pressEnter(t, press(t, m, "x"))
	if head := headOf(m); !strings.Contains(head, "write · 2 of 3") {
		t.Errorf("after one enter head = %q, want write · 2 of 3", head)
	}
}

func TestADestructiveFormsHeadSpellsItOut(t *testing.T) {
	m := sizedForm(t, positionCap(plugin.Destructive, "a", "b"), 100, 30)
	if head := headOf(m); !strings.Contains(head, "destructive · 1 of 2") {
		t.Errorf("head = %q, want destructive · 1 of 2", head)
	}
}

// A read has nothing to warn about and does not say so: the count alone.
func TestAReadFormsHeadHasTheCountAndNoSafetyWord(t *testing.T) {
	m := sizedForm(t, positionCap(plugin.Read, "a", "b"), 100, 30)
	head := headOf(m)
	if !strings.Contains(head, "1 of 2") || strings.Contains(head, "read") {
		t.Errorf("head = %q, want 1 of 2 and no safety word", head)
	}
}

func TestAOneBoxReadFormsHeadSaysNeither(t *testing.T) {
	m := sizedForm(t, positionCap(plugin.Read, "a"), 100, 30)
	head := headOf(m)
	if strings.Contains(head, " of ") || strings.Contains(head, "read") {
		t.Errorf("head = %q, want neither a count nor a safety word", head)
	}
}

// One box is no length to speak of, but a write is still a write.
func TestAOneBoxWriteFormsHeadStillSaysWrite(t *testing.T) {
	m := sizedForm(t, positionCap(plugin.Write, "a"), 100, 30)
	head := headOf(m)
	if !strings.Contains(head, "write") || strings.Contains(head, " of ") {
		t.Errorf("head = %q, want write and no count", head)
	}
}

// The count is of the boxes being asked, not of the ones a condition hides.
func TestTheCountSkipsTheBoxesAConditionHides(t *testing.T) {
	c := positionCap(plugin.Write, "kind", "extra", "last")
	cf := newCapForm(c, c.Inputs, nil, true, nil,
		hideUnless("extra", func(cf *capForm) bool { return *cf.bindings["kind"] == "x" }))
	m := formModel(t, cf)
	m.current = c
	m.fitForm()
	if head := headOf(m); !strings.Contains(head, "write · 1 of 2") {
		t.Fatalf("with the extra box hidden head = %q, want write · 1 of 2", head)
	}
	m = press(t, m, "x")
	if head := headOf(m); !strings.Contains(head, "write · 1 of 3") {
		t.Errorf("with the extra box shown head = %q, want write · 1 of 3", head)
	}
}

// The head gives the right-hand text up before it starves the title, and the
// frame keeps its width either way.
func TestTheHeadKeepsItsWidthWhereTheTerminalIsNarrow(t *testing.T) {
	for _, w := range []int{44, 30, 20} {
		m := sizedForm(t, positionCap(plugin.Write, "a", "b", "c"), w, 20)
		for _, row := range strings.Split(plain(m.formView()), "\n") {
			if got := lipgloss.Width(row); got > w {
				t.Fatalf("width %d: a row is %d wide: %q", w, got, row)
			}
		}
	}
}
