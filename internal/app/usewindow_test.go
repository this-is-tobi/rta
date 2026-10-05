package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `rta lock add --ttl 1d` and `rta agent log --since 1d` take a day, and
// `rta use staging --for 1d` was refused by a Duration flag that knows no unit
// above the hour.
func TestUseTakesTheDeadlineInDaysLikeEveryOtherWindow(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, stderr, err := run("profile", "set", "staging", "--plugin", "db", "--set", "host=staging.internal"); err != nil {
		t.Fatalf("set: %v\n%s", err, stderr)
	}
	before := time.Now()
	if _, stderr, err := run("use", "staging", "--for", "1d"); err != nil {
		t.Fatalf("use --for 1d: %v\n%s", err, stderr)
	}
	sel := profile.LoadSelection()
	if sel.Active != "staging" || sel.Until == nil {
		t.Fatalf("selection = %+v, want staging with a deadline", sel)
	}
	if got := sel.Until.Sub(before); got < 23*time.Hour+59*time.Minute || got > 24*time.Hour+time.Minute {
		t.Errorf("the deadline is %v away, want a day", got)
	}
}

// Text that is no length of time is refused as the usage mistake it is, naming
// what was typed and the spellings that work.
func TestUseRefusesADeadlineThatIsNoLengthOfTime(t *testing.T) {
	_, _, err := run(t, testRegistry(t), "use", "staging", "--for", "soon")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != CodeUsage || !strings.Contains(ve.Message, `--for "soon" is not a length of time`) ||
		!strings.Contains(ve.Hint, "`1d`") {
		t.Errorf("err = %#v, want a usage error naming soon and the spellings", err)
	}
}
