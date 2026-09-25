package plugin

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// **The bug this closes, stated once.**
//
// Resolve normalises the shapes an integer legitimately arrives in and leaves
// anything else alone; the accessor downstream is a type assertion. Between
// them, a value of the wrong shape did not fail and was not ignored — it was
// read as the zero. So `tls: "true"` in a connection left the handler
// reading false, and nothing anywhere said so. The host refuses such a value
// now, and this is the report of it before any call.
//
// These assert the predicate. TestWhatTheRunDoesMatchesTheVerdict below is
// the one that matters: it checks the predicate against the run.

func TestStatedTypeProblemAcceptsWhatAHandlerCanRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    Field
		v    any
	}{
		{"bool", Field{Name: "tls", Type: Bool}, true},
		{"int as int", Field{Name: "port", Type: Int}, 5432},
		// The shapes Resolve exists to normalise: YAML decodes a bare
		// non-negative integer as uint64 and JSON gives float64. Both are
		// ordinary and neither is a mistake.
		{"int as uint64", Field{Name: "port", Type: Int}, uint64(5432)},
		{"int as float64", Field{Name: "port", Type: Int}, float64(5432)},
		{"float as int", Field{Name: "ratio", Type: Float}, 2},
		{"string", Field{Name: "host", Type: String}, "db.internal"},
		{"text", Field{Name: "body", Type: Text}, "hello"},
		{"path", Field{Name: "out", Type: Path}, "/tmp/x"},
		{"secret", Field{Name: "password", Type: Secret}, "x"},
		{"slice", Field{Name: "tags", Type: StringSlice}, []string{"a"}},
		{"slice as []any", Field{Name: "tags", Type: StringSlice}, []any{"a"}},
		// Documented on StringSlice itself: a scalar in a list slot means
		// "just this one", and every reader of that value agrees.
		{"slice as scalar", Field{Name: "tags", Type: StringSlice}, "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if problem, _ := StatedTypeProblem(tc.f, tc.v); problem != "" {
				t.Errorf("a value a handler reads correctly was reported: %s", problem)
			}
		})
	}
}

func TestStatedTypeProblemCatchesWhatTheHostRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    Field
		v    any
	}{
		// The two spellings that cost transport security. A quoted `true` is
		// a string because somebody quoted it; a bare `yes` is a string
		// because YAML 1.2 stopped treating it as a boolean.
		{"quoted true", Field{Name: "tls", Type: Bool}, "true"},
		{"bare yes", Field{Name: "tls", Type: Bool}, "yes"},
		{"number for bool", Field{Name: "tls", Type: Bool}, 1},
		{"quoted port", Field{Name: "port", Type: Int}, "5432"},
		{"bool for int", Field{Name: "port", Type: Int}, true},
		{"quoted float", Field{Name: "ratio", Type: Float}, "1.5"},
		// The other direction: an unquoted value YAML types for you. A
		// database called 2024, or a host that looks like a number.
		{"number for text", Field{Name: "host", Type: String}, 2024},
		{"bool for text", Field{Name: "host", Type: String}, true},
		{"block for text", Field{Name: "host", Type: String}, map[string]any{"a": 1}},
		{"block for a list", Field{Name: "tags", Type: StringSlice}, map[string]any{"a": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problem, hint := StatedTypeProblem(tc.f, tc.v)
			if problem == "" {
				t.Fatalf("a value the handler reads as the zero was accepted")
			}
			if hint == "" {
				t.Error("no hint — the operator is told they are wrong and not how to be right")
			}
		})
	}
}

// A key written with nothing after it is still reported, since the file
// states a value no run uses, but not as a zero the handler reads: Resolve
// drops it, and the input keeps its default. `tls:` left empty was reported
// as a connection reading false while it ran with the declared true, and
// `port:` as a 0 while it ran on 5432.
func TestAKeyWrittenWithNoValueIsReportedAsSettingNothing(t *testing.T) {
	c := Capability{
		ID: "db.status", Summary: "s", Safety: Read,
		Inputs: []Field{
			{Name: "tls", Type: Bool, Default: true, Config: "tls", Local: true},
			{Name: "port", Type: Int, Default: 5432, Min: 1, Max: 65535, Config: "port", Local: true},
			{Name: "host", Type: String, Default: "localhost", Config: "host", Local: true},
			{Name: "tags", Type: StringSlice, Config: "tags", Local: true},
		},
	}
	for _, f := range c.Inputs {
		t.Run(f.Name, func(t *testing.T) {
			problem, hint := StatedTypeProblem(f, nil)
			if !strings.Contains(problem, "sets nothing") || strings.Contains(problem, "would read") {
				t.Errorf("problem %q", problem)
			}
			if hint != "write a value, or remove the key" {
				t.Errorf("hint %q", hint)
			}
			for _, in := range []Inputs{
				{Config: map[string]any{f.Config: nil}},
				{Profile: map[string]any{f.Name: nil}, ProfileName: "staging"},
			} {
				got := Resolve(c, in)[f.Name]
				if want := f.Default; fmt.Sprint(got) != fmt.Sprint(want) {
					t.Errorf("%+v resolved %s to %v, not its default %v — the report is no longer true",
						in, f.Name, got, want)
				}
			}
		})
	}
}

// **The value never appears in the message.** This runs over configuration an
// operator wrote, and one mistyped block is all it takes for the thing being
// described to be a credential.
func TestStatedTypeProblemNeverEchoesTheValue(t *testing.T) {
	const secret = "hunter2-should-never-appear"
	for _, f := range []Field{
		{Name: "tls", Type: Bool},
		{Name: "port", Type: Int},
		{Name: "ratio", Type: Float},
		{Name: "tags", Type: StringSlice},
		{Name: "host", Type: String},
	} {
		problem, hint := StatedTypeProblem(f, secret)
		if strings.Contains(problem+hint, secret) {
			t.Errorf("%s: the message quotes the value: %s / %s", f.Type, problem, hint)
		}
	}
	// And the same for a value that is not text, since the shape describer is
	// the other half of the same function.
	if problem, _ := StatedTypeProblem(Field{Name: "tls", Type: Bool},
		map[string]any{secret: secret}); strings.Contains(problem, secret) {
		t.Errorf("a block's keys reached the message: %s", problem)
	}
}

// The example in the hint is one the field takes, since a person follows it:
// `5432` beside a limit running from 1 to 100 named a value every call would
// then refuse. The value written is never the example — see above.
func TestStatedTypeProblemShowsANumberTheFieldTakes(t *testing.T) {
	for _, tc := range []struct {
		f    Field
		want string
	}{
		{Field{Name: "limit", Type: Int, Default: 15, Min: 1, Max: 100}, "`15`, not `\"15\"`"},
		{Field{Name: "count", Type: Int, Min: 1, Max: 1000}, "`1`, not `\"1\"`"},
		{Field{Name: "id", Type: Int}, "`5`, not `\"5\"`"},
		{Field{Name: "ratio", Type: Float, Min: 0.25}, "`0.25`, not `\"0.25\"`"},
	} {
		if _, hint := StatedTypeProblem(tc.f, "text"); !strings.Contains(hint, tc.want) {
			t.Errorf("%s: hint %q, want it to show %s", tc.f.Name, hint, tc.want)
		}
	}
}

// A type this does not recognise gets no opinion. Validate refuses such a
// field at registration, so inventing a problem here would be a report about
// a declaration that cannot reach a run.
func TestStatedTypeProblemHasNoOpinionOnATypeItDoesNotKnow(t *testing.T) {
	if problem, _ := StatedTypeProblem(Field{Name: "x", Type: FieldType("gnarly")}, 1); problem != "" {
		t.Errorf("invented a problem about an unknown type: %s", problem)
	}
	if problem, _ := StatedTypeProblem(Field{Name: "x"}, 1); problem != "" {
		t.Errorf("invented a problem about an absent type: %s", problem)
	}
}

// **The verdict and the run must agree.** A predicate that says "fine" while
// the run refuses — or while a handler reads a zero — is worse than no
// predicate: it is the page telling the operator their file is right.
//
// So this asks the actual question — resolve the value the way every surface
// does, and check that a value reported is exactly a value the host refuses,
// and that one not reported reaches the handler as stated.
func TestWhatTheRunDoesMatchesTheVerdict(t *testing.T) {
	c := Capability{
		ID: "db.status", Summary: "s", Safety: Read,
		Inputs: []Field{
			{Name: "tls", Type: Bool, Config: "tls", Local: true},
			{Name: "port", Type: Int, Config: "port", Local: true},
			{Name: "host", Type: String, Config: "host", Local: true},
			{Name: "tags", Type: StringSlice, Config: "tags", Local: true},
		},
	}
	for _, tc := range []struct {
		name  string
		input string
		value any
		// reads is what a handler gets when the value is accepted, or ""
		// when the host refuses it. Compared as text so one table covers
		// four accessors.
		reads string
	}{
		{"bool kept", "tls", true, "true"},
		{"bool as text", "tls", "true", ""},
		{"bool as yes", "tls", "yes", ""},
		{"int kept", "port", uint64(5432), "5432"},
		{"int as text", "port", "5432", ""},
		{"string kept", "host", "db", "db"},
		{"string as a number", "host", 2024, ""},
		{"string as a boolean", "host", true, ""},
		{"slice kept", "tags", []any{"a"}, "[a]"},
		{"slice as a scalar", "tags", "a", "[a]"},
		// A block is left out: under a config key it is a section rather
		// than a value, whose leaves pluginconf reports as keys nothing
		// reads, and from a profile it is refused like the rest.
	} {
		t.Run(tc.name, func(t *testing.T) {
			var f Field
			for _, in := range c.Inputs {
				if in.Config == tc.input {
					f = in
				}
			}
			problem, _ := StatedTypeProblem(f, tc.value)

			req := ResolveRequest(c, Inputs{Config: map[string]any{tc.input: tc.value}}, false, true)
			verr := CheckInputs(c, req)
			var got string
			switch f.Type {
			case Bool:
				got = boolText(req.Bool(f.Name))
			case Int:
				got = strconv.Itoa(req.Int(f.Name))
			case String:
				got = req.String(f.Name)
			case StringSlice:
				got = sliceText(req.StringSlice(f.Name))
			}
			if tc.reads == "" {
				if verr == nil || problem == "" {
					t.Fatalf("refused = %v, reported = %q: want both, since the handler would read %q",
						verr, problem, got)
				}
				if verr.Code != "core.input.type" && verr.Code != "core.input.range" {
					t.Errorf("code %s", verr.Code)
				}
				return
			}
			if verr != nil || problem != "" {
				t.Fatalf("refused = %v, reported = %q: want neither", verr, problem)
			}
			if got != tc.reads {
				t.Errorf("the handler read %q, want %q", got, tc.reads)
			}
		})
	}
}

func boolText(b bool) string { return strconv.FormatBool(b) }

func sliceText(s []string) string { return "[" + strings.Join(s, " ") + "]" }

// **The same mismatch one layer earlier.** A Default is what every accessor
// reads when nobody states a value, so `{Type: Bool, Default: "true"}` ships
// a capability whose declared default is false — and unlike an operator's
// config file, nobody but the plugin author can fix it. Refused at
// registration.
func TestValidateRefusesADefaultTheFieldCannotHold(t *testing.T) {
	base := func(f Field) Plugin {
		return Plugin{Name: "db", Summary: "db", Capabilities: []Capability{{
			ID: "db.status", Summary: "s", Safety: Read,
			Run:    func(context.Context, Request) (view.View, error) { return view.Text{}, nil },
			Inputs: []Field{f},
		}}}
	}
	for _, tc := range []struct {
		name string
		f    Field
		bad  bool
	}{
		{"bool default as string", Field{Name: "tls", Type: Bool, Default: "true"}, true},
		{"int default as string", Field{Name: "port", Type: Int, Default: "5432"}, true},
		{"string default as number", Field{Name: "host", Type: String, Default: 2024}, true},
		{"bool default", Field{Name: "tls", Type: Bool, Default: true}, false},
		{"int default", Field{Name: "port", Type: Int, Default: 5432}, false},
		{"float default written as an int", Field{Name: "r", Type: Float, Default: 2}, false},
		{"slice default", Field{Name: "tags", Type: StringSlice, Default: []string{"a"}}, false},
		{"no default at all", Field{Name: "host", Type: String}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := base(tc.f).Validate()
			if tc.bad && err == nil {
				t.Fatal("a default the field cannot hold was accepted")
			}
			if !tc.bad && err != nil {
				t.Fatalf("a legitimate default was refused: %v", err)
			}
		})
	}
}
