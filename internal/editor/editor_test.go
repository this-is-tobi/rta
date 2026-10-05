package editor

import (
	"reflect"
	"testing"
)

func TestTheEditorIsVisualThenEditorThenVi(t *testing.T) {
	for _, c := range []struct {
		visual, editor string
		want           []string
	}{
		{"", "", []string{"vi"}},
		{"", "nano", []string{"nano"}},
		{"code --wait", "nano", []string{"code", "--wait"}},
		{"  ", "emacsclient -nw", []string{"emacsclient", "-nw"}},
	} {
		t.Setenv("VISUAL", c.visual)
		t.Setenv("EDITOR", c.editor)
		if got := Command(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("VISUAL=%q EDITOR=%q: %v, want %v", c.visual, c.editor, got, c.want)
		}
	}
}

// An editor is split into words and never expanded: $(…) in a variable is text.
func TestTheEditorIsNeverHandedToAShell(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim; touch pwned $(id)")
	got := Command()
	if got[0] != "vim;" || len(got) != 4 {
		t.Errorf("Command = %v", got)
	}
}
