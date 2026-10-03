package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/internal/seal"
	"github.com/this-is-tobi/rta/pkg/view"
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

// maxSelection bounds the selection read, for the reason
// internal/atomicfile.ReadCapped states. This file names the active profile
// and so bounds what an agent may reach; it is a couple of fields and a seal,
// and 4 KiB is far past anything Save writes.
const maxSelection = 4 << 10

// selectionKey is the name of the key the selection is sealed with, beside the
// file as every sealed file's is.
const selectionKey = "profile.key"

// sealed is the file: the selection and the MAC over it.
type sealed struct {
	Selection
	MAC string `json:"mac"`
}

// Unverified is the name Fence answers when the selection file is there and
// cannot be trusted. It is not a profile — no reference can be spelled so — and
// that is the point: it is what the fence holds an agent to when it cannot say
// which environment is on.
const Unverified = "?"

// ErrUnverified is why a selection file was not believed.
var ErrUnverified = errors.New("the selection file is not one rta wrote")

// macOf is the MAC over the selection as it is written, with the key.
func macOf(key []byte, s Selection) (string, error) {
	body, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return seal.MAC(key, body), nil
}

// ReadSelection reads the selection, an empty one where there is no file, and
// an error where there is a file that cannot be believed: unreadable, not what
// Save writes, or not sealed with the key beside it.
//
// **A file that does not verify is not a file that says nothing is on.** The
// selection is the fence the operator puts around what an agent may reach —
// "while I work in staging, no grant for production is honoured" — and an
// unreadable or edited file used to answer empty, which lifted the fence on
// exactly the machine where somebody had been at the file. It is sealed now,
// like the grants, and a file that does not verify is the error here and the
// closed fence in Fence.
func ReadSelection() (Selection, error) {
	data, err := atomicfile.ReadCapped(SelectionPath(), maxSelection)
	if errors.Is(err, os.ErrNotExist) {
		// No file is nothing on only where rta never wrote one. `rta use --off`
		// writes a sealed empty selection and never removes the file, so the
		// key standing where the file is gone says it was taken away, which is
		// the way to lift the fence that needs no edit to verify.
		if _, statErr := os.Stat(seal.Path(selectionKey)); statErr == nil {
			return Selection{}, fmt.Errorf("%s is missing beside its key: %w", SelectionPath(), ErrUnverified)
		}
		return Selection{}, nil
	}
	if err != nil {
		return Selection{}, fmt.Errorf("reading %s: %w", SelectionPath(), err)
	}
	var file sealed
	if err := json.Unmarshal(data, &file); err != nil {
		return Selection{}, fmt.Errorf("%s: %w", SelectionPath(), ErrUnverified)
	}
	key, err := seal.Key(selectionKey, false)
	if err != nil {
		return Selection{}, fmt.Errorf("%s: %w (%w)", SelectionPath(), ErrUnverified, err)
	}
	want, err := macOf(key, file.Selection)
	if err != nil || !seal.Equal(file.MAC, want) {
		return Selection{}, fmt.Errorf("%s: %w", SelectionPath(), ErrUnverified)
	}
	return file.Selection, nil
}

// LoadSelection reads the selection for a person: the one the file holds, or
// an empty one for any failure, so that a data directory that cannot be read
// costs somebody a flag and not the command. What bounds an agent is Fence,
// which does not answer empty for a file it cannot believe; `rta doctor` says
// when that is the state.
func LoadSelection() Selection {
	s, err := ReadSelection()
	if err != nil {
		return Selection{}
	}
	return s
}

// Fence is the environment agents are held to right now: the profile switched
// on, "" for none, and Unverified when the file says something that cannot be
// believed. A caller that compares it with the profile a call names refuses
// every one while it is Unverified, which is the direction that can only take
// reach away.
func Fence() string {
	s, err := ReadSelection()
	if err != nil {
		return Unverified
	}
	return s.Name(time.Now())
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
	if dir, err := paths.EnsureData(); err != nil {
		return view.Errorf("core.profile.write", "creating %s: %v", dir, err)
	}
	key, err := seal.Key(selectionKey, true)
	if err != nil {
		return view.Errorf("core.profile.write", "the selection's seal key: %v", err)
	}
	mac, err := macOf(key, s)
	if err != nil {
		return view.Errorf("core.profile.write", "sealing the selection: %v", err)
	}
	data, err := json.MarshalIndent(sealed{Selection: s, MAC: mac}, "", "  ")
	if err != nil {
		return view.Errorf("core.profile.write", "encoding the selection: %v", err)
	}
	// 0600, like the grant file. It bounds what agents may reach, and it names
	// which environment somebody is working in — neither is another local
	// user's business.
	//
	// Sealed, like the grant file, which this used to differ from on the
	// ground that a seal stops authority being added by hand and this file
	// cannot add any. It cannot, and it can be edited, truncated or replaced to
	// *lift* the fence the operator relies on, and what answered for a file
	// nobody could believe was an empty selection: the environment off. The seal
	// is not a lock — whatever reads the key beside the file can seal one of its
	// own, as it can for the grants — but a change by anything that is not rta
	// is now one that is noticed, and an agent held to nothing for it.
	if err := atomicfile.Write(SelectionPath(), data, 0o600); err != nil {
		return view.Errorf("core.profile.write", "writing %s: %v", SelectionPath(), err)
	}
	return nil
}
