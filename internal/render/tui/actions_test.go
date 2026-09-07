package tui

import (
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// Every capability capActionSpecs can reach with a keypress is either Read
// (nothing to flash — runAction never sets refreshPending for it), or must
// be accounted for in exactly one of alwaysOwnPage and flashSafe. A
// capability left out of both does not fail closed on its own: flashText's
// default without this test would have been to draw its result as a
// one-liner whenever it happened to look like one, which is the shape C4
// found and flashSafe exists to stop. This test is what actually stops it —
// a capability added to capActionSpecs tomorrow without an opinion recorded
// here fails the build instead of shipping unclassified.
func TestEveryFlashableActionIsClassified(t *testing.T) {
	reg := realRegistry(t)
	seen := map[string]bool{}
	for capID := range capActionSpecs {
		for _, a := range capActions(reg, capID) {
			if seen[a.cap.ID] {
				continue
			}
			seen[a.cap.ID] = true
			if a.cap.Safety == plugin.Read {
				continue
			}
			own, safe := alwaysOwnPage[a.cap.ID], flashSafe[a.cap.ID]
			if own && safe {
				t.Errorf("%s is in both alwaysOwnPage and flashSafe — pick one", a.cap.ID)
			}
			if !own && !safe {
				t.Errorf("%s is reachable from a list action, is not Read, and is classified in "+
					"neither alwaysOwnPage nor flashSafe — read runSet/runAdd/whatever backs it and "+
					"say which: alwaysOwnPage if its result is (or could become) the value acted on, "+
					"flashSafe if it is always a fixed confirmation", a.cap.ID)
			}
		}
	}
}
