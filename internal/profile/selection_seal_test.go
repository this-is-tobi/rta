package profile

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/seal"
)

func sealedDir(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
}

func TestASealedSelectionRoundTrips(t *testing.T) {
	sealedDir(t)
	if s, err := ReadSelection(); err != nil || s.Active != "" || Fence() != "" {
		t.Fatalf("with no file the selection is %+v, %v, fence %q; want nothing on", s, err, Fence())
	}
	for _, until := range []time.Time{
		time.Now().Add(time.Hour).UTC(),
		time.Now().Add(time.Hour).In(time.FixedZone("x", 5*3600+1800)).Add(123456789),
		time.Now().Add(time.Hour),
	} {
		until := until
		if verr := SaveSelection(Selection{Active: "staging", Until: &until}); verr != nil {
			t.Fatal(verr)
		}
		s, err := ReadSelection()
		if err != nil || s.Active != "staging" || s.Until == nil || !s.Until.Equal(until) {
			t.Fatalf("a sealed selection read back as %+v, %v", s, err)
		}
		if Fence() != "staging" {
			t.Errorf("the fence is %q, want staging", Fence())
		}
	}
	if verr := SaveSelection(Selection{}); verr != nil {
		t.Fatal(verr)
	}
	if s, err := ReadSelection(); err != nil || s.Active != "" {
		t.Fatalf("switched off, the selection is %+v, %v", s, err)
	}
}

// The selection is the fence an operator puts around what an agent may reach,
// and a file that was edited, replaced by one nothing sealed, truncated, or left
// beside no key used to answer an empty selection — the fence lifted on the
// machine where something had been at the file. It is the error now, and the
// fence is held shut.
func TestASelectionThatDoesNotVerifyClosesTheFence(t *testing.T) {
	sealedDir(t)
	if verr := SaveSelection(Selection{Active: "staging"}); verr != nil {
		t.Fatal(verr)
	}
	sealed, err := os.ReadFile(SelectionPath())
	if err != nil {
		t.Fatal(err)
	}

	for name, content := range map[string]string{
		"edited to another environment": strings.Replace(string(sealed), "staging", "prod", 1),
		"unsealed, as it used to be":    `{"active":"staging"}`,
		"truncated":                     string(sealed[:len(sealed)/2]),
		"not json at all":               "not a selection",
		"emptied":                       "",
	} {
		if err := os.WriteFile(SelectionPath(), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadSelection(); !errors.Is(err, ErrUnverified) {
			t.Errorf("%s: read as %v, want ErrUnverified", name, err)
		}
		if got := Fence(); got != Unverified {
			t.Errorf("%s: the fence is %q, want it held shut", name, got)
		}
		if LoadSelection().Active != "" {
			t.Errorf("%s: what a person is shown is not empty", name)
		}
	}

	// Sealed with a key that is not the one beside it.
	if err := os.WriteFile(SelectionPath(), sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(seal.Path(selectionKey)); err != nil {
		t.Fatal(err)
	}
	if got := Fence(); got != Unverified {
		t.Errorf("with its key gone the fence is %q, want it held shut", got)
	}

	// And writing it again is the way out.
	if verr := SaveSelection(Selection{}); verr != nil {
		t.Fatal(verr)
	}
	if got := Fence(); got != "" {
		t.Errorf("after `rta use --off` the fence is %q, want none", got)
	}
}

// A file that is gone is not a file that says nothing is on, once rta has
// written one: `rta use --off` writes an empty sealed selection and never
// removes the file, so its key standing without it is the file taken away, and
// that lifted the fence as surely as an edit would have.
func TestASelectionRemovedBesideItsKeyClosesTheFence(t *testing.T) {
	sealedDir(t)
	if verr := SaveSelection(Selection{Active: "staging"}); verr != nil {
		t.Fatal(verr)
	}
	if err := os.Remove(SelectionPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSelection(); !errors.Is(err, ErrUnverified) {
		t.Fatalf("a selection removed beside its key read as %v, want ErrUnverified", err)
	}
	if got := Fence(); got != Unverified {
		t.Errorf("the fence is %q, want it held shut", got)
	}
	if verr := SaveSelection(Selection{}); verr != nil {
		t.Fatal(verr)
	}
	if got := Fence(); got != "" {
		t.Errorf("after `rta use --off` the fence is %q, want none", got)
	}
}

func TestUnverifiedIsNotAProfile(t *testing.T) {
	if Unverified == "" || config.ValidRef(Unverified) {
		t.Fatalf("%q is spelled as no fence or as a profile an operator could name", Unverified)
	}
}
