package plugin

import (
	"strings"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	good := map[string]time.Duration{
		"0":      0,
		"30s":    30 * time.Second,
		"5m":     5 * time.Minute,
		"2h":     2 * time.Hour,
		"1h30m":  90 * time.Minute,
		"1d":     24 * time.Hour,
		"2d":     48 * time.Hour,
		"1w":     7 * 24 * time.Hour,
		"1w2d3h": (7+2)*24*time.Hour + 3*time.Hour,
		"1.5h":   90 * time.Minute,
		".5s":    500 * time.Millisecond,
		"250ms":  250 * time.Millisecond,
		"10us":   10 * time.Microsecond,
		"10µs":   10 * time.Microsecond,
		"7ns":    7 * time.Nanosecond,
	}
	for in, want := range good {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	// The ones that must be refused: a bare number says nothing about its
	// unit, and the rest are not durations at all.
	for _, in := range []string{"", "30", "-5s", "+5s", "5 s", "s", ".", "1x", "5m3", "1.2.3s", "99999999999d",
		"9223372036854775807s", "1h-5m"} {
		if got, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) = %v, want an error", in, got)
		}
	}
}

func TestAFormattedDurationReadsBackAsItself(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                                  "0s",
		30 * time.Second:                   "30s",
		5 * time.Minute:                    "5m",
		time.Hour:                          "1h",
		90 * time.Minute:                   "1h30m",
		time.Hour + 5*time.Second:          "1h0m5s",
		24 * time.Hour:                     "1d",
		72 * time.Hour:                     "3d",
		36 * time.Hour:                     "36h",
		1500 * time.Millisecond:            "1.5s",
		2*time.Minute + 30*time.Second:     "2m30s",
		7*24*time.Hour + 24*time.Hour:      "8d",
		25*time.Hour + 30*time.Minute:      "25h30m",
		time.Microsecond * 250:             "250µs",
		time.Minute + 500*time.Millisecond: "1m0.5s",
	} {
		got := FormatDuration(d)
		if got != want {
			t.Errorf("FormatDuration(%v) = %q, want %q", d, got, want)
		}
		if back, err := ParseDuration(got); err != nil || back != d {
			t.Errorf("%q reads back as %v, %v; want %v", got, back, err, d)
		}
	}
}

// timed is a capability with a bounded Duration input.
func timed(f Field) (Capability, Plugin) {
	f.Name, f.Type, f.Help = "timeout", Duration, "how long to wait"
	c := Capability{ID: "demo.item.wait", Summary: "wait for an item", Safety: Read, Run: noop, Inputs: []Field{f}}
	return c, Plugin{Name: "demo", Summary: "demo plugin", Capabilities: []Capability{c}}
}

func TestADurationInputIsDeclaredWithTextAndUnits(t *testing.T) {
	_, ok := timed(Field{Default: "30s", Min: "1s", Max: "1h"})
	if err := ok.Validate(); err != nil {
		t.Fatalf("a bounded duration was refused: %v", err)
	}
	for name, tc := range map[string]struct {
		f    Field
		want string
	}{
		"a default with no unit":       {Field{Default: "30"}, "is a bare number, which does not say its unit"},
		"a default that is a number":   {Field{Default: 30}, "is a bare number, which does not say its unit"},
		"a default that is not time":   {Field{Default: "soon"}, "is text that is not a duration"},
		"a Go duration as the default": {Field{Default: 30 * time.Second}, "count of nanoseconds"},
		"a minimum that is a number":   {Field{Min: 1}, "bounds are text with a unit"},
		"a maximum that is not time":   {Field{Max: "forever"}, "not a duration"},
		"a minimum above the maximum":  {Field{Min: "1h", Max: "1m"}, "so no value could ever be accepted"},
		"a default under the minimum":  {Field{Default: "1s", Min: "5s"}, "its range is of at least 5s"},
		"options on a duration":        {Field{Options: []string{"30s"}}, "declares Options"},
	} {
		t.Run(name, func(t *testing.T) {
			_, p := timed(tc.f)
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestAHandlerReadsADurationAsATimeDuration(t *testing.T) {
	c, _ := timed(Field{Default: "30s", Min: "1s", Max: "1h"})
	req := ResolveRequest(c, Inputs{Caller: map[string]any{"timeout": "2d"}}, false, false)
	if got := req.Duration("timeout"); got != 48*time.Hour {
		t.Errorf("Duration = %v, want 48h", got)
	}
	// Nothing given reads as the declared default; an absent input as zero.
	if got := ResolveRequest(c, Inputs{}, false, false).Duration("timeout"); got != 30*time.Second {
		t.Errorf("the default read as %v", got)
	}
	if got := NewRequest(nil, false, false).Duration("timeout"); got != 0 {
		t.Errorf("an absent input read as %v", got)
	}
}

// The host refuses a value that is not a duration, or is outside the bounds,
// before any handler runs, in the words its surface spells.
func TestTheHostRefusesAValueThatIsNotADurationOrIsOutOfRange(t *testing.T) {
	c, _ := timed(Field{Default: "30s", Min: "1s", Max: "1h"})
	check := func(v any) *string {
		req := NewRequest(map[string]any{"timeout": v}, false, false).WithSurface(SurfaceCLI)
		if verr := CheckInputs(c, req); verr != nil {
			msg := verr.Code + ": " + verr.Message + " | " + verr.Hint
			return &msg
		}
		return nil
	}
	for _, v := range []any{"30s", "1h", "90m", time.Minute} {
		if refused := check(v); v != "90m" && refused != nil {
			t.Errorf("%v was refused: %s", v, *refused)
		}
	}
	for v, want := range map[any]string{
		// What a flag hands over for the number 30: every flag arrives as text,
		// and "not text" would name the one thing it is.
		"30":    "core.input.range: `rta demo item wait` takes a duration from 1s to 1h for --timeout, not a bare number",
		30:      "core.input.range: `rta demo item wait` takes a duration from 1s to 1h for --timeout, not a bare number",
		"soon":  "core.input.range:",
		true:    "core.input.range:",
		"500ms": "core.input.range: `rta demo item wait` takes a duration from 1s to 1h for --timeout, not 500ms",
		"2h":    "core.input.range: `rta demo item wait` takes a duration from 1s to 1h for --timeout, not 2h",
	} {
		refused := check(v)
		if refused == nil || !strings.Contains(*refused, want) {
			t.Errorf("%v: want a refusal containing %q, got %v", v, want, refused)
		}
	}
	// An unbounded duration refuses what is no duration as a wrong type.
	loose, _ := timed(Field{})
	req := NewRequest(map[string]any{"timeout": 30}, false, false).WithSurface(SurfaceMCP)
	verr := CheckInputs(loose, req)
	if verr == nil || verr.Code != "core.input.type" || !strings.Contains(verr.Hint, "write it with a unit") {
		t.Errorf("a bare number for an unbounded duration: %v", verr)
	}
}

// One config key serves every capability that declares it, and they bound it
// differently, so the operator's layers are held inside this capability's range
// instead of refused, as an Int is.
func TestADurationFromTheOperatorIsHeldInsideTheRange(t *testing.T) {
	c, _ := timed(Field{Default: "30s", Min: "1s", Max: "5m", Config: "timeout"})
	for given, want := range map[string]string{"10m": "5m", "100ms": "1s", "2m": "2m", "1d": "5m"} {
		got := Resolve(c, Inputs{Config: map[string]any{"timeout": given}})["timeout"]
		if got != want {
			t.Errorf("config %q resolved to %v, want %q", given, got, want)
		}
	}
	// What the caller sent is never moved: it is refused by CheckInputs.
	if got := Resolve(c, Inputs{Caller: map[string]any{"timeout": "10m"}})["timeout"]; got != "10m" {
		t.Errorf("a caller's value was moved to %v", got)
	}
}
