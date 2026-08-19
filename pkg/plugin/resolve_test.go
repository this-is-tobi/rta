package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func numeric() Capability {
	return Capability{
		ID: "x.y", Summary: "s", Safety: Read,
		Inputs: []Field{
			{Name: "timeout", Type: Int, Default: 10, Min: 1, Max: 300},
			{Name: "ratio", Type: Float, Default: 0.5, Min: 0.0, Max: 1.0},
			{Name: "limit", Type: Int, Default: 15},
			{Name: "host", Type: String, Default: "localhost"},
			{Name: "verbose", Type: Bool},
		},
	}
}

// The bug: both the TUI and the dashboard filled declared defaults only when
// the caller supplied *no* values at all. Pinning a tile with one setting
// therefore dropped every other default, and the handler saw zero values it
// could not tell from real ones — while the same capability run from a shell
// worked, because cobra bakes defaults into the flag set.
func TestResolveKeepsDefaultsWhenSomeValuesAreGiven(t *testing.T) {
	got := Resolve(numeric(), map[string]any{"limit": 5})
	if got["limit"] != 5 {
		t.Errorf("the given value was lost: %v", got["limit"])
	}
	if got["timeout"] != 10 {
		t.Errorf("timeout default dropped: %v", got["timeout"])
	}
	if got["host"] != "localhost" {
		t.Errorf("host default dropped: %v", got["host"])
	}
	// A field with no declared default stays absent rather than becoming a
	// zero the handler cannot distinguish from a choice.
	if _, ok := got["verbose"]; ok {
		t.Errorf("a field with no default was invented: %v", got["verbose"])
	}
}

func TestResolveDoesNotMutateItsInput(t *testing.T) {
	in := map[string]any{"limit": 5}
	Resolve(numeric(), in)
	if len(in) != 1 {
		t.Errorf("Resolve wrote back into the caller's map: %v", in)
	}
}

// goccy-yaml decodes untyped non-negative integers as uint64, which is what
// the config loader hands the dashboard. Request.Int did not recognise it and
// returned 0, so every numeric tile input silently resolved to zero — with no
// error, because the config parsed fine.
func TestResolveNormalisesEveryShapeAnIntegerArrivesIn(t *testing.T) {
	for _, v := range []any{
		5, int8(5), int16(5), int32(5), int64(5),
		uint(5), uint8(5), uint16(5), uint32(5), uint64(5),
		float32(5), float64(5), json.Number("5"),
	} {
		req := NewRequest(Resolve(numeric(), map[string]any{"limit": v}), false, false)
		if got := req.Int("limit"); got != 5 {
			t.Errorf("%T(%v) resolved to %d, want 5", v, v, got)
		}
	}
	// And a value that is not a number at all is left alone rather than
	// replaced with a confident zero.
	got := Resolve(numeric(), map[string]any{"limit": "not a number"})
	if got["limit"] != "not a number" {
		t.Errorf("a non-numeric value was rewritten: %v", got["limit"])
	}
}

// `net ping --timeout 0` reached time.NewTicker(0) inside a library goroutine
// and aborted the process. Over MCP that is one schema-valid call from an
// unprivileged agent killing the server for every other tool.
func TestResolveClampsToDeclaredBounds(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{0, 1}, {-5, 1}, {1, 1}, {150, 150}, {300, 300}, {9000, 300},
	}
	for _, tc := range cases {
		req := NewRequest(Resolve(numeric(), map[string]any{"timeout": tc.in}), false, false)
		if got := req.Int("timeout"); got != tc.want {
			t.Errorf("timeout %v resolved to %d, want %d", tc.in, got, tc.want)
		}
	}
	// Floats too, and a bound of 0 is a real bound rather than "unset".
	for in, want := range map[float64]float64{-1: 0, 0: 0, 0.25: 0.25, 2: 1} {
		req := NewRequest(Resolve(numeric(), map[string]any{"ratio": in}), false, false)
		if got := req.Float("ratio"); got != want {
			t.Errorf("ratio %v resolved to %v, want %v", in, got, want)
		}
	}
	// An unbounded field is not clamped into existence.
	req := NewRequest(Resolve(numeric(), map[string]any{"limit": -3}), false, false)
	if got := req.Int("limit"); got != -3 {
		t.Errorf("an unbounded field was clamped to %d", got)
	}
}

// Undeclared values pass through: the MCP bridge and Page both overlay keys
// the capability never declared, and Resolve is not the place to police that.
func TestResolveLeavesUndeclaredValuesAlone(t *testing.T) {
	got := Resolve(numeric(), map[string]any{"detail": true, "surprise": uint64(7)})
	if got["detail"] != true {
		t.Errorf("detail was dropped: %v", got["detail"])
	}
	if got["surprise"] != uint64(7) {
		t.Errorf("an undeclared value was coerced: %T %v", got["surprise"], got["surprise"])
	}
}

// A capability declaring an input the host injects passed Validate and then
// panicked pflag with "flag redefined: detail" while the command tree was
// built — which kills every rta invocation, including the doctor that would
// have named the culprit.
func TestReservedInputNamesAreRejectedAtRegistration(t *testing.T) {
	c := Capability{
		ID: "acme.report", Summary: "reports things", Safety: Read, Detailed: true,
		Run:    func(context.Context, Request) (view.View, error) { return view.Text{}, nil },
		Inputs: []Field{{Name: "detail", Type: Bool, Help: "include per-item detail"}},
	}
	p := Plugin{Name: "acme", Summary: "acme things", Capabilities: []Capability{c}}

	err := p.Validate()
	if err == nil {
		t.Fatal(`a capability declaring the reserved input "detail" was accepted`)
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("the error should say why: %v", err)
	}

	// The same capability without the collision is fine, so the rule rejects
	// the name rather than the shape.
	p.Capabilities[0].Inputs = []Field{{Name: "verbose", Type: Bool, Help: "per-item detail"}}
	if err := p.Validate(); err != nil {
		t.Errorf("a capability with no reserved name was rejected: %v", err)
	}
}
