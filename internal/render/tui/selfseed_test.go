package tui

import (
	"testing"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// sampleOf is a value of the type an input declares, for a page that ran with
// every input given.
func sampleOf(f plugin.Field) (any, bool) {
	switch f.Type {
	case plugin.Int:
		return 1, true
	case plugin.Float:
		return 1.5, true
	case plugin.Bool:
		return true, true
	case plugin.String, plugin.Text, plugin.Secret, plugin.Path:
		if len(f.Options) > 0 {
			return f.Options[0], true
		}
		return "x", true
	case plugin.StringSlice:
		return []string{"x"}, true
	}
	return nil, false
}

// A record's page acts on its own subject: d on a note's page checks that note
// off. What the page ran with is carried to the form of the capability it
// opens, and that capability may declare the same input as another type — a
// note's page takes one id as a number, and done, reopen and rm take one or
// several as text. The carried number was refused as "takes a list for the id
// box, not a number" on every one of those keys, with the note in front of the
// person who pressed it. So every seed a page gives is held to what the target
// accepts, which no fixture declaring the two inputs alike can show.
func TestEveryOneKeyActionFromARecordsPageSeedsWhatItsTargetAccepts(t *testing.T) {
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, from := range reg.Capabilities() {
		for _, a := range capActions(reg, from.ID) {
			if a.src != srcSelf {
				continue
			}
			ran := map[string]any{}
			for _, f := range from.Inputs {
				if v, ok := sampleOf(f); ok {
					ran[f.Name] = v
				}
			}
			m := Model{reg: reg, current: from, lastValues: ran}
			seed, ok := m.actionSeed(a, view.Table{})
			if !ok {
				continue
			}
			seen++
			req := plugin.NewRequest(seed, false, false).WithSurface(plugin.SurfaceTUI)
			if verr := plugin.CheckInputs(a.cap, req); verr != nil {
				t.Errorf("%s on the page of %s seeds %v, which %s refuses: %s", a.key, from.ID, seed, a.cap.ID, verr.Message)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no action on a record's page was checked: the declarations changed shape under this test")
	}
}
