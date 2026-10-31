package cli

import (
	"strings"
	"testing"
)

// One line of a megabyte with a separator every sixty characters — a JSON
// string body `rta http get` reads, a note body, a value a plugin returns — is
// laid out at a terminal by breakToken, which measured what was left of the
// line on every pass and the prefix again at every separator. The work grew
// with the square of the length: a minute and a half of CPU for the megabyte.
// What the layout measures is counted rather than timed, so the test holds on
// a loaded machine.
func TestALongUnbrokenLineIsMeasuredInProportionToItsLength(t *testing.T) {
	measured := 0
	was := cellWidth
	cellWidth = func(s string) int { measured += len(s); return was(s) }
	t.Cleanup(func() { cellWidth = was })

	line := strings.Repeat(strings.Repeat("x", 59)+":", 2000)
	pieces := hardBreakOverlong(line, 80)
	if got := strings.Join(pieces, ""); got != line {
		t.Fatal("the pieces do not put the line back together")
	}
	for _, p := range pieces {
		if w := was(p); w > 80 {
			t.Fatalf("a piece is %d cells wide, over the budget of 80", w)
		}
	}
	if measured > 8*len(line) {
		t.Errorf("laying out a %d-byte line measured %d bytes, %d times its length",
			len(line), measured, measured/len(line))
	}
}
