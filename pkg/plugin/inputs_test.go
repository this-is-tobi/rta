package plugin

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

func optionCap(run Handler) Capability {
	return Capability{
		ID: "demo.token", Summary: "demo", Safety: Read,
		Inputs: []Field{
			{Name: "encoding", Type: String, Default: "hex", Options: []string{"hex", "base64"}},
			{Name: "tags", Type: StringSlice, Options: []string{"red", "blue"}},
			{Name: "free", Type: String},
		},
		Run: run,
	}
}

// A closed set is held to: a value naming none of its options is refused
// before the handler sees it, naming the input and the set.
func TestAValueOutsideItsOptionsIsRefused(t *testing.T) {
	ran := false
	guarded := GuardInputs(optionCap(func(context.Context, Request) (view.View, error) {
		ran = true
		return nil, nil
	}))
	for _, values := range []map[string]any{
		{"encoding": "b64"},
		{"tags": []string{"red", "green"}},
	} {
		_, err := guarded(context.Background(), NewRequest(values, false, false))
		verr := view.AsError(err, "test")
		if err == nil || verr.Code != "core.input.option" {
			t.Errorf("%v: err = %v, want core.input.option", values, err)
			continue
		}
		if !strings.Contains(verr.Message, "demo.token takes one of") {
			t.Errorf("message = %q", verr.Message)
		}
	}
	if ran {
		t.Error("the handler ran on a refused value")
	}
	// An empty value is not a choice, and a field without Options is not
	// checked at all.
	if _, err := guarded(context.Background(), NewRequest(map[string]any{"encoding": "", "free": "anything"}, false, false)); err != nil {
		t.Errorf("empty and free-form values were refused: %v", err)
	}
	if !ran {
		t.Error("the handler did not run on valid values")
	}
}

// Resolve rewrites an option typed in another case to the declared spelling,
// so a handler sees one spelling and `--encoding HEX` is not refused.
func TestResolveSpellsAnOptionTheWayItIsDeclared(t *testing.T) {
	out := Resolve(optionCap(nil), Inputs{Caller: map[string]any{
		"encoding": "BASE64", "tags": []string{"Red", "BLUE"}, "free": "KeepMe",
	}})
	if out["encoding"] != "base64" {
		t.Errorf("encoding = %v", out["encoding"])
	}
	if tags, _ := out["tags"].([]string); strings.Join(tags, ",") != "red,blue" {
		t.Errorf("tags = %v", out["tags"])
	}
	if out["free"] != "KeepMe" {
		t.Errorf("a free-form value was rewritten: %v", out["free"])
	}
}

// A list from YAML or JSON is []any, and a bare string is one value, and
// only a []string was rewritten: `tags: [Red]` in a config section or a
// tile's with: was refused as naming no option, where `--tags Red` ran.
// Each keeps its shape.
func TestResolveSpellsAListOptionAsDeclaredWhateverItsShape(t *testing.T) {
	var seen []string
	c := optionCap(func(_ context.Context, r Request) (view.View, error) { seen = r.StringSlice("tags"); return nil, nil })
	c.Inputs[1].Config = "tags" // so a profile may fill it
	guarded := GuardInputs(c)
	for _, v := range []any{[]any{"Red", "BLUE"}, "Red"} {
		for _, in := range []Inputs{{Caller: map[string]any{"tags": v}}, {Profile: map[string]any{"tags": v}, ProfileName: "p"}} {
			seen = nil
			resolved := Resolve(c, in)
			if _, err := guarded(context.Background(), NewRequest(resolved, false, false)); err != nil {
				t.Errorf("%#v: %v", v, err)
				continue
			}
			if seen[0] != "red" || (len(seen) > 1 && seen[1] != "blue") {
				t.Errorf("%#v: the handler read %v", v, seen)
			}
			if fmt.Sprintf("%T", resolved["tags"]) != fmt.Sprintf("%T", v) {
				t.Errorf("%#v became a %T", v, resolved["tags"])
			}
		}
	}
	// The rule the host's other readers ask by name.
	f := Field{Options: []string{"MX", "AAAA"}}
	if o, ok := f.CanonicalOption("mx"); o != "MX" || !ok {
		t.Errorf("CanonicalOption(mx) = %q, %v", o, ok)
	}
	if o, ok := f.CanonicalOption("txt"); o != "txt" || ok {
		t.Errorf("CanonicalOption(txt) = %q, %v", o, ok)
	}
}

// Options on a number or a boolean are published over MCP as the enum of
// every value the input accepts, and the MCP boundary held them there; the
// host held only the two string types, so `level: 9` on a field offering 1,
// 2 and 4 was refused over MCP and ran from the CLI, a form and a config
// file. Compared in the spelling the boundary uses.
func TestOptionsOnANumberOrABooleanAreHeldToo(t *testing.T) {
	c := Capability{
		ID: "demo.level", Summary: "demo", Safety: Read,
		Inputs: []Field{
			{Name: "level", Type: Int, Options: []string{"1", "2", "4"}},
			{Name: "ratio", Type: Float, Options: []string{"0.5", "1"}},
			{Name: "strict", Type: Bool, Options: []string{"true"}},
		},
		Run: func(context.Context, Request) (view.View, error) { return nil, nil },
	}
	guarded := GuardInputs(c)
	call := func(values map[string]any) error {
		_, err := guarded(context.Background(), NewRequest(Resolve(c, Inputs{Caller: values}), false, false))
		return err
	}
	for _, values := range []map[string]any{
		{"level": 3}, {"level": uint64(9)}, {"ratio": 0.25}, {"strict": false},
	} {
		if err := call(values); err == nil || view.AsError(err, "test").Code != "core.input.option" {
			t.Errorf("%v: err = %v, want core.input.option", values, err)
		}
	}
	for _, values := range []map[string]any{
		{"level": 4}, {"level": float64(2)}, {"ratio": 1.0}, {"ratio": 0.5}, {"strict": true},
	} {
		if err := call(values); err != nil {
			t.Errorf("%v was refused: %v", values, err)
		}
	}
}

func TestGuardInputsLeavesAnUnguardedHandlerAlone(t *testing.T) {
	if GuardInputs(Capability{ID: "demo.none"}) != nil {
		t.Error("a nil handler became non-nil")
	}
}

// A default outside its own set would be refused on every call that leaves
// the input alone, so the declaration is refused instead, where its author
// sees it.
func TestADefaultOutsideItsOptionsFailsValidation(t *testing.T) {
	p := validPlugin()
	p.Capabilities[0].Inputs = []Field{{Name: "mode", Type: String, Default: "fast", Options: []string{"slow", "safe"}}}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "not one of its options") {
		t.Errorf("err = %v, want the default refused", err)
	}
	// A list is held element by element, and a number in the spelling the
	// guard compares with: either one validated, and every call that left
	// the input alone was then refused.
	for _, f := range []Field{
		{Name: "kinds", Type: StringSlice, Default: []string{"red", "green"}, Options: []string{"red", "blue"}},
		{Name: "level", Type: Int, Default: 3, Options: []string{"1", "2", "4"}},
	} {
		p.Capabilities[0].Inputs = []Field{f}
		if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "not one of its options") {
			t.Errorf("%s: err = %v, want the default refused", f.Name, err)
		}
	}
	for _, f := range []Field{
		{Name: "kinds", Type: StringSlice, Default: []string{"red"}, Options: []string{"red", "blue"}},
		{Name: "level", Type: Int, Default: 2, Options: []string{"1", "2", "4"}},
	} {
		p.Capabilities[0].Inputs = []Field{f}
		if err := p.Validate(); err != nil {
			t.Errorf("%s: a default among its options was refused: %v", f.Name, err)
		}
	}
	p.Capabilities[0].Inputs = []Field{{Name: "limit", Type: Int, Default: 0, Min: 1, Max: 10}}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "its range is from 1 to 10") {
		t.Errorf("err = %v, want a default outside its range refused", err)
	}
	p.Capabilities[0].Inputs = []Field{{Name: "limit", Type: Int, Default: 5, Min: 1, Max: 10}}
	if err := p.Validate(); err != nil {
		t.Errorf("a default inside its range was refused: %v", err)
	}
}

// A refused value nobody sent on the call read as a flag somebody typed:
// `encoding: b64` in the config answered a bare `rta gen token` with the
// option list and a pointer at `rta explain`, naming neither the file nor
// the key — and over MCP told an agent about a value only the operator can
// change. The refusal says which layer set it now, and whom to ask.
func TestARefusedValueFromConfigOrAProfileSaysWhereItCameFrom(t *testing.T) {
	c := optionCap(func(context.Context, Request) (view.View, error) { return nil, nil })
	c.Inputs[0].Config = "encoding"
	c.Inputs = append(c.Inputs, Field{Name: "timeout", Type: Int, Default: 5, Min: 1, Max: 60, Config: "timeout"})
	refusal := func(req Request) *view.Error {
		t.Helper()
		verr := CheckInputs(c, req)
		if verr == nil {
			t.Fatal("accepted")
		}
		return verr
	}
	for _, tc := range []struct {
		in        Inputs
		code      string
		where     string
		hintNames string
	}{
		{Inputs{Config: map[string]any{"encoding": "b64"}}, "core.input.option",
			"which the config's plugins.demo.encoding sets", "rta explain demo.token"},
		{Inputs{Config: map[string]any{"timeout": "90"}}, "core.input.range",
			"which the config's plugins.demo.timeout sets", "rta explain demo.token"},
		{Inputs{Profile: map[string]any{"encoding": "b64"}, ProfileName: "prod"}, "core.input.option",
			`which the profile "prod" sets`, "rta profile show prod"},
	} {
		verr := refusal(ResolveRequest(c, tc.in, false, false).WithSurface(SurfaceCLI))
		if verr.Code != tc.code || !strings.Contains(verr.Message, tc.where) || !strings.Contains(verr.Hint, tc.hintNames) {
			t.Errorf("%+v: %s %q / %q", tc.in, verr.Code, verr.Message, verr.Hint)
		}
		// An agent is told who can change it, and how to step round it.
		verr = refusal(ResolveRequest(c, tc.in, false, false).WithSurface(SurfaceMCP))
		if !strings.Contains(verr.Hint, "the operator can change it") || !strings.Contains(verr.Hint, "overrides it for this call") {
			t.Errorf("%+v over MCP: hint %q", tc.in, verr.Hint)
		}
	}
	// The caller's own value is refused in the caller's words, and so is a
	// value a page laid over the operator's.
	for _, req := range []Request{
		ResolveRequest(c, Inputs{Caller: map[string]any{"encoding": "b64"}, Config: map[string]any{"encoding": "hex"}}, false, false),
		ResolveRequest(c, Inputs{Config: map[string]any{"encoding": "hex"}}, false, false).With(map[string]any{"encoding": "b64"}),
	} {
		if verr := refusal(req); strings.Contains(verr.Message, "sets") {
			t.Errorf("a value the caller sent was put on the config: %q", verr.Message)
		}
	}
	// And the values are Resolve's.
	in := Inputs{Caller: map[string]any{"tags": []string{"RED"}}, Config: map[string]any{"timeout": uint64(90)}}
	if got, want := ResolveRequest(c, in, false, false).Values(), Resolve(c, in); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("ResolveRequest carries %v, Resolve %v", got, want)
	}
}
