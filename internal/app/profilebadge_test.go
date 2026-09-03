package app

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/profile"
)

// on switches an environment on for the duration of one test.
func on(t *testing.T, name string, until *time.Time) {
	t.Helper()
	if verr := profile.SaveSelection(profile.Selection{Active: name, Until: until}); verr != nil {
		t.Fatalf("switching to %s: %v", name, verr)
	}
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
}

func marked() config.Config {
	return config.Config{Profiles: map[string]config.Profile{
		"shop-prod":    {Color: "#FF6B7A"},
		"shop-dev":     {},
		"shop-broken":  {Color: "red"},
		"shop-pale":    {Color: "#FFC24B"},
		"shop-unknown": {Color: "#3ED598"},
	}}
}

// **The opt-in is the whole design, so it is the first thing pinned.**
//
// This prints before every command, which is only tolerable because writing
// `color:` is the operator asking for it. A badge on every environment would
// be a banner nobody reads within a week, and the one that matters would go
// with it.
func TestTheBadgePrintsOnlyForAnEnvironmentSomebodyMarked(t *testing.T) {
	cfg := marked()

	on(t, "shop-prod", nil)
	var out bytes.Buffer
	WarnActiveProfile(&out, cfg, false, true)
	if !strings.Contains(out.String(), "shop-prod") {
		t.Errorf("a marked environment did not announce itself: %q", out.String())
	}

	for _, name := range []string{"shop-dev", "shop-unknown-to-config"} {
		if verr := profile.SaveSelection(profile.Selection{Active: name}); verr != nil {
			t.Fatal(verr)
		}
		out.Reset()
		WarnActiveProfile(&out, cfg, false, true)
		if out.Len() != 0 {
			t.Errorf("%s is not marked and still printed %q", name, out.String())
		}
	}
}

// Nothing switched on says nothing. The badge answers "which environment", and
// there is no answer to give.
func TestNothingSwitchedOnPrintsNothing(t *testing.T) {
	on(t, "", nil)
	var out bytes.Buffer
	WarnActiveProfile(&out, marked(), false, true)
	if out.Len() != 0 {
		t.Errorf("a session with no environment printed %q", out.String())
	}
}

// The same rule the untrusted-plugin notice learned the hard way: a person
// running `-o json` at a prompt is building a pipeline, and the output they
// copy off the screen is the output they paste into a parser.
func TestTheBadgeStaysOutOfMachineReadableOutput(t *testing.T) {
	on(t, "shop-prod", nil)
	var out bytes.Buffer
	WarnActiveProfile(&out, marked(), true, false)
	if out.Len() != 0 {
		t.Errorf("a run asking for a parseable format got a badge: %q", out.String())
	}
}

// **A colour that is not a colour paints nothing, rather than falling back to
// one nobody chose.** A badge in a default colour would say "this environment
// is marked" about one whose marking rta could not read — and the operator
// who typed it believes something is happening. `rta doctor` is where that
// gets reported, by profile.Check.
func TestAColourThatIsNotAColourPrintsNothing(t *testing.T) {
	on(t, "shop-broken", nil)
	var out bytes.Buffer
	WarnActiveProfile(&out, marked(), false, false)
	if out.Len() != 0 {
		t.Errorf("an unreadable colour still painted something: %q", out.String())
	}
}

// --no-color is a statement about ANSI, not about wanting to know less: the
// badge keeps its brackets and loses its paint.
func TestWithoutColourTheBadgeKeepsItsBrackets(t *testing.T) {
	on(t, "shop-prod", nil)

	var plain bytes.Buffer
	WarnActiveProfile(&plain, marked(), false, true)
	if strings.Contains(plain.String(), "\x1b[") {
		t.Errorf("--no-color still emitted escapes: %q", plain.String())
	}
	if !strings.Contains(plain.String(), "[ shop-prod ]") {
		t.Errorf("--no-color lost the badge entirely: %q", plain.String())
	}

	// …and the coloured form really is coloured, or the whole feature is a
	// line of text that happens to name the profile.
	var painted bytes.Buffer
	WarnActiveProfile(&painted, marked(), false, false)
	if !strings.Contains(painted.String(), "\x1b[") {
		t.Errorf("the badge was not painted at all: %q", painted.String())
	}
}

// Being on production and being on it for six more minutes are different
// situations, and both are decided before the command runs rather than after.
func TestADeadlineRidesAlongWithTheBadge(t *testing.T) {
	until := time.Now().Add(47 * time.Minute)
	on(t, "shop-prod", &until)

	var out bytes.Buffer
	WarnActiveProfile(&out, marked(), false, true)
	if !strings.Contains(out.String(), "left") {
		t.Errorf("a switch with a deadline did not say how long is left: %q", out.String())
	}

	// A switch with no deadline says nothing about one, rather than "forever",
	// which is a claim about a file somebody can edit.
	on(t, "shop-prod", nil)
	out.Reset()
	WarnActiveProfile(&out, marked(), false, true)
	if strings.Contains(out.String(), "left") {
		t.Errorf("a switch with no deadline invented one: %q", out.String())
	}
}
