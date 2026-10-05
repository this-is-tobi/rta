package note

import (
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

func declared(t *testing.T, id string) plugin.Capability {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no capability %s", id)
	return plugin.Capability{}
}

// Taking several ids must not widen what one grant reaches: a grant for note 1
// covers a call naming note 1, and a call that names note 2 beside it is
// refused whole, so naming more notes is never a way past the record a grant
// was issued for.
func TestAGrantForOneNoteDoesNotCoverACallNamingTwo(t *testing.T) {
	setup(t)
	if verr := grant.Save([]grant.Grant{{
		Target: "note.rm", Scope: "1",
		Issued: time.Now(), Expires: time.Now().Add(15 * time.Minute),
	}}); verr != nil {
		t.Fatal(verr)
	}
	rm := declared(t, "note.rm")

	for name, tc := range map[string]struct {
		ids     []string
		covered bool
	}{
		"the note granted":        {[]string{"1"}, true},
		"the note granted, twice": {[]string{"1", "1"}, true},
		"another note":            {[]string{"2"}, false},
		"the note and another":    {[]string{"1", "2"}, false},
		"another and the note":    {[]string{"2", "1"}, false},
		"the note padded":         {[]string{"01"}, false},
	} {
		release, verr := grant.Reserve(rm, map[string]any{"id": tc.ids}, grant.Caller{})
		if verr == nil {
			release()
		}
		if got := verr == nil; got != tc.covered {
			t.Errorf("%s: covered = %v, want %v (%v)", name, got, tc.covered, verr)
		}
	}
}
