package note

import (
	"context"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A refusal of one input names it (view.Error.Input), by the name the
// capability that refused it declares, so a form opens again on that box and
// an agent knows which argument to send again. A refusal no one box fixes —
// an edit that changes nothing — names none.
func TestARefusalOfOneInputNamesIt(t *testing.T) {
	cases := []struct {
		capID  string
		values map[string]any
		code   string
		input  string
	}{
		{"note.show", map[string]any{"id": 9}, "note.notfound", "id"},
		{"note.toggle", map[string]any{"id": 9}, "note.notfound", "id"},
		{"note.edit", map[string]any{"id": 9, "title": "y", "parent": noParentChange}, "note.notfound", "id"},
		{"note.done", ids("1", "9"), "note.notfound", "id"},
		{"note.reopen", ids("9"), "note.notfound", "id"},
		{"note.rm", ids("9"), "note.notfound", "id"},
		{"note.done", ids("#1"), "note.id.invalid", "id"},
		{"note.rm", map[string]any{}, "note.id.none", "id"},
		{"note.add", map[string]any{"title": "  "}, "note.add.empty", "title"},
		{"note.add", map[string]any{"title": "x", "parent": 9}, "note.add.badparent", "parent"},
		{"note.add", map[string]any{"title": "x", "due": "whenever"}, "note.add.baddue", "due"},
		{"note.edit", map[string]any{"id": 1, "parent": 1}, "note.edit.selfparent", "parent"},
		{"note.edit", map[string]any{"id": 1, "parent": 9}, "note.edit.badparent", "parent"},
		{"note.edit", map[string]any{"id": 1, "parent": 2}, "note.edit.cycle", "parent"},
		{"note.edit", map[string]any{"id": 1, "due": "whenever", "parent": noParentChange}, "note.edit.baddue", "due"},
		{"note.edit", map[string]any{"id": 1, "parent": noParentChange}, "note.edit.nochange", ""},
	}
	for _, tc := range cases {
		t.Run(tc.capID+" "+tc.code, func(t *testing.T) {
			setup(t)
			addTodo(t, "alpha")
			text(t, runAdd, map[string]any{"title": "beta", "parent": 1}, false)
			c := declared(t, tc.capID)

			_, err := c.Run(context.Background(), req(tc.values, false))
			ve := view.AsError(err, "x")
			if ve == nil || ve.Code != tc.code {
				t.Fatalf("refused as %+v, want %s", ve, tc.code)
			}
			if tc.input == "" {
				if ve.Input != "" {
					t.Errorf("%s names the input %q, want none", ve.Code, ve.Input)
				}
				return
			}
			if f, ok := c.ErrorInput(ve); !ok || f.Name != tc.input {
				t.Errorf("%s names the input %q, want %s, which %s declares", ve.Code, ve.Input, tc.input, c.ID)
			}
		})
	}
}

// The edit form asks for the note before it shows the note's content in the
// boxes, and a note that is not there is refused there, on the id: Prefill
// names it as the run does.
func TestTheEditFormsPrefillNamesTheIDOfANoteThatIsNotThere(t *testing.T) {
	setup(t)
	edit := declared(t, "note.edit")
	_, err := edit.Prefill(context.Background(), req(map[string]any{"id": 9}, false))
	ve := view.AsError(err, "x")
	if ve == nil || ve.Code != "note.notfound" {
		t.Fatalf("prefill of note 9 = %+v, want note.notfound", ve)
	}
	if f, ok := edit.ErrorInput(ve); !ok || f.Name != "id" {
		t.Errorf("prefill's refusal names the input %q, want id", ve.Input)
	}
}
