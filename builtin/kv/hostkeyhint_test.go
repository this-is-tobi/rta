package kv

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// textclean.Record puts the name a host resolves as beside a record that names
// a host in anything but ASCII, and a key shaped like one is drawn so. The
// message names the key as Record draws it, note and all; the command the hint
// hands on is the one to type, and the note is no part of the key it creates.
func TestTheCommandThatMakesAKeyShapedLikeAHostHasNoNoteInIt(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	key := "g" + string(rune(0x456)) + "thub.com"

	_, verr := RevealAs(plugin.SurfaceCLI)(key)
	if verr == nil {
		t.Fatal("RevealAs found a key in a store that does not exist")
	}
	if !strings.Contains(verr.Message, key+" (xn--gthub-n2e.com)") {
		t.Errorf("the refusal does not name the key as a record is drawn: %q", verr.Message)
	}
	if want := "`rta kv set '" + key + "'` creates the store"; !strings.HasPrefix(verr.Hint, want) {
		t.Errorf("the hint is %q, want it to start with %q", verr.Hint, want)
	}
	_, verr = Reveal(key)
	if verr == nil {
		t.Fatal("Reveal found a key in a store that does not exist")
	}
	if !strings.HasPrefix(verr.Hint, plugin.AskOperator("kv set "+key)+" ") {
		t.Errorf("the hint handed on as the operator's is %q, want the command with the key alone", verr.Hint)
	}
}
