package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/internal/paths"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The per-machine record of which environment is switched on.
//
// One at a time, deliberately. A profile now spans every plugin that has
// something in that environment, so "which database did that touch" has one
// answer — and two active profiles would need a rule for what happens when
// both configure pg, which is a rule nobody should have to know to be sure
// they are not in production.
//
// **Read by the CLI, the TUI and — only to refuse — internal/mcp.** The
// direction is the whole safety argument. An agent names the profile in the
// tool call, the call is filled from the name it gave, and a grant is what
// permits it; the selection is consulted afterwards and can only take the call
// away. That is why it may be session state at all: something that can only
// subtract cannot make the gate and the run disagree about which server was
// touched, which is exactly what an *expanding* session input would do.
type Selection struct {
	// Active is the profile switched on, or "".
	Active string `json:"active,omitempty"`

	// Until is when it switches itself off. Nil means no deadline.
	//
	// Enforced on every read rather than by anything running in the background.
	// A timer belongs to a process, and the whole point of a deadline on a
	// production environment is that it survives the process — closing the
	// laptop must not be what keeps it switched on.
	Until *time.Time `json:"until,omitempty"`
}

// SelectionPath is where the selection is kept.
func SelectionPath() string { return filepath.Join(paths.Data(), "profile.json") }

// LoadSelection reads the selection, or an empty one.
//
// Every failure answers empty, on purpose. This is a convenience; a data
// directory that cannot be read should cost somebody a flag, not the command.
// It is also the safe direction now that the selection bounds agents: an
// unreadable file switches everything off rather than leaving a stale name in
// force.
// maxSelection bounds the selection read, for the reason
// internal/atomicfile.ReadCapped states. This file names the active profile
// and so bounds what an agent may reach; it is a couple of fields, and 4 KiB
// is far past anything Save writes.
const maxSelection = 4 << 10

func LoadSelection() Selection {
	var s Selection
	data, err := atomicfile.ReadCapped(SelectionPath(), maxSelection)
	if err != nil {
		return Selection{}
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Selection{}
	}
	return s
}

// Expired reports whether this selection's deadline has passed.
func (s Selection) Expired(now time.Time) bool {
	return s.Until != nil && !now.Before(*s.Until)
}

// Name is the profile in force at now, or "".
func (s Selection) Name(now time.Time) string {
	if s.Active == "" || s.Expired(now) {
		return ""
	}
	return s.Active
}

// Left is how long this selection has to run, and whether it has a deadline.
func (s Selection) Left(now time.Time) (time.Duration, bool) {
	if s.Until == nil {
		return 0, false
	}
	return s.Until.Sub(now), true
}

// Active is the profile switched on right now, or "".
func Active() string { return LoadSelection().Name(time.Now()) }

// ShortDuration renders a window the way somebody reads a clock.
//
// Written out rather than handed to time.Duration.String, which always carries
// the smaller units along: 47 minutes prints as "47m0s" and an hour and a half
// as "1h30m0s". The trailing zero is noise on a badge somebody glances at, and
// the badge is the whole point — a number that has to be parsed is one that
// gets read after the command instead of before it.
//
// Here rather than in the TUI that first needed it, because the CLI needs the
// same answer: the durations `rta use --for` offers have to be both readable
// and re-typeable, and "1h30m0s" is neither.
func ShortDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		d = d.Round(time.Minute)
		if m := int(d.Minutes()) % 60; m > 0 {
			return fmt.Sprintf("%dh%dm", int(d.Hours()), m)
		}
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Round(time.Minute).Minutes()))
	default:
		return fmt.Sprintf("%ds", max(int(d.Round(time.Second).Seconds()), 1))
	}
}

// SaveSelection persists the selection.
func SaveSelection(s Selection) *view.Error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return view.Errorf("core.profile.write", "encoding the selection: %v", err)
	}
	if err := os.MkdirAll(paths.Data(), 0o755); err != nil {
		return view.Errorf("core.profile.write", "creating %s: %v", paths.Data(), err)
	}
	// 0600, like the grant file. It bounds what agents may reach, and it names
	// which environment somebody is working in — neither is another local
	// user's business.
	//
	// Unsealed, unlike the grant file, and the difference is deliberate: a seal
	// exists to stop authority being *added* by hand, and this file cannot add
	// any. Tampering with it removes a bound, which lands back on "grants alone
	// decide" — where an operator can already put themselves with `rta use
	// --off`. It is worth naming as a limit rather than a property: somebody who
	// can write this file can lift the bound, and the answer to that threat is
	// the grants, which are sealed.
	if err := atomicfile.Write(SelectionPath(), data, 0o600); err != nil {
		return view.Errorf("core.profile.write", "writing %s: %v", SelectionPath(), err)
	}
	return nil
}
