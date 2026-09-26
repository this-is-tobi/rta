package plugin

import (
	"context"
	"fmt"
	"strconv"
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

// Options are a closed set of text. On a number or a boolean the MCP schema
// published an integer enum of strings no JSON value satisfies, the TUI
// handed a Bool's pick back as text the handler read as false, and `rta
// profile set` wrote a number the profile check then refused — so the
// declaration is refused where its author is, and a number or a switch
// bounds itself with Min and Max.
func TestOptionsAreDeclaredOnTextAlone(t *testing.T) {
	p := validPlugin()
	for _, f := range []Field{
		{Name: "level", Type: Int, Options: []string{"1", "2", "4"}},
		{Name: "ratio", Type: Float, Options: []string{"0.5", "1"}},
		{Name: "strict", Type: Bool, Options: []string{"true"}},
		{Name: "body", Type: Text, Options: []string{"a"}},
		{Name: "out", Type: Path, Options: []string{"/tmp/a"}},
		{Name: "key", Type: Secret, Local: true, Options: []string{"a"}},
		{Name: "keys", Type: SecretSlice, Local: true, Options: []string{"a"}},
	} {
		p.Capabilities[0].Inputs = []Field{f}
		if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "closed set of text") {
			t.Errorf("%s: err = %v, want Options on a %s refused", f.Name, err, f.Type)
		}
	}
	for _, f := range []Field{
		{Name: "mode", Type: String, Options: []string{"fast", "safe"}},
		{Name: "kinds", Type: StringSlice, Options: []string{"red", "blue"}},
	} {
		p.Capabilities[0].Inputs = []Field{f}
		if err := p.Validate(); err != nil {
			t.Errorf("%s: Options on a %s were refused: %v", f.Name, f.Type, err)
		}
	}
}

// A required input with no value reaches no handler, on any surface — absent,
// nil, empty text or an empty list, each of which the handler would read as
// nobody having given it. A form that forgot to ask for it is held here.
func TestAMissingRequiredInputIsRefusedBeforeTheHandler(t *testing.T) {
	ran := false
	c := Capability{ID: "demo.pick", Summary: "picks", Safety: Read,
		Inputs: []Field{
			{Name: "kinds", Type: StringSlice, Required: true, Options: []string{"table", "view"}},
			{Name: "limit", Type: Int, Required: true},
		},
		Run: func(context.Context, Request) (view.View, error) { ran = true; return nil, nil },
	}
	guarded := GuardInputs(c)
	for _, s := range []Surface{SurfaceUnknown, SurfaceCLI, SurfaceTUI, SurfaceMCP} {
		for _, kinds := range []any{nil, []string{}, []any{}, ""} {
			values := map[string]any{"limit": 3}
			if kinds != nil {
				values["kinds"] = kinds
			}
			_, err := guarded(context.Background(), NewRequest(values, false, false).WithSurface(s))
			if verr := view.AsError(err, "test"); err == nil || verr.Code != "core.input.missing" {
				t.Errorf("%q, kinds %#v: err = %v, want core.input.missing", s, kinds, err)
			}
		}
	}
	// 0 is a number somebody gave, not the absence of one.
	if _, err := guarded(context.Background(), NewRequest(map[string]any{"kinds": "view", "limit": 0}, false, false)); err != nil {
		t.Errorf("a zero was read as missing: %v", err)
	}
	if !ran {
		t.Error("the handler did not run with every input given")
	}
}

// The refusal names the input the way the caller's surface names it, since
// that is what they type back, and says who can give it.
func TestAMissingInputIsNamedTheWayItsSurfaceNamesIt(t *testing.T) {
	c := Capability{ID: "db.query", Summary: "query", Safety: Read}
	for _, tc := range []struct {
		f             Field
		s             Surface
		message, hint string
	}{
		{Field{Name: "host", Type: String, Required: true, Config: "host"}, SurfaceCLI,
			"db.query needs --host", "pass --host, or set host in your rta config"},
		{Field{Name: "table", Type: String, Required: true, Positional: true}, SurfaceCLI,
			"db.query needs <table>", "give it as an argument — `rta db query --help` says where"},
		{Field{Name: "password", Type: Secret, Required: true, Local: true, EnvFallback: true}, SurfaceCLI,
			"db.query needs --password", "pass --password, or export $RTA_DB_PASSWORD"},
		{Field{Name: "table", Type: String, Required: true, Positional: true}, SurfaceMCP,
			`db.query needs the argument "table"`, `pass "table" in the arguments`},
		{Field{Name: "host", Type: String, Required: true, Local: true, Config: "host"}, SurfaceMCP,
			"db.query needs host, which only the operator can give", "ask the operator to set it in the rta config"},
		{Field{Name: "password", Type: Secret, Required: true, Local: true, EnvFallback: true}, SurfaceMCP,
			"db.query needs password, which only the operator can give",
			"ask the operator to set it in the environment rta mcp serve runs in"},
		{Field{Name: "dir", Type: Path, Required: true, Local: true, Positional: true}, SurfaceMCP,
			"db.query needs dir, which only the operator can give",
			"ask the operator to run it from their own terminal"},
		{Field{Name: "host", Type: String, Required: true, Config: "host"}, SurfaceTUI,
			"db.query needs host", "fill in the host box, or set host in your rta config"},
		{Field{Name: "host", Type: String, Required: true}, SurfaceUnknown, "db.query needs host", ""},
	} {
		verr := MissingInput(c, tc.f, tc.s)
		if verr.Code != "core.input.missing" || verr.Message != tc.message || verr.Hint != tc.hint {
			t.Errorf("%s over %q:\n got %s: %q (%q)\nwant core.input.missing: %q (%q)",
				tc.f.Name, tc.s, verr.Code, verr.Message, verr.Hint, tc.message, tc.hint)
		}
	}
}

// Piped is required wherever there is no pipe to read — MCP and the TUI — and
// left out on the CLI, which reads the pipe then, and by an in-process caller.
func TestAPipedInputIsRequiredWhereThereIsNoPipe(t *testing.T) {
	f := Field{Name: "token", Type: Secret, Piped: true}
	for s, want := range map[Surface]bool{
		SurfaceMCP: true, SurfaceTUI: true, SurfaceCLI: false, SurfaceUnknown: false, SurfaceCompletion: false,
	} {
		if got := f.RequiredOn(s); got != want {
			t.Errorf("RequiredOn(%q) = %v, want %v", s, got, want)
		}
	}
}

// What must be there is asked before what each value declares, so a call
// missing an input and carrying a bad value elsewhere is told what it lacks.
func TestCheckRequestAsksForWhatIsMissingFirst(t *testing.T) {
	c := optionCap(nil)
	c.Inputs = append(c.Inputs, Field{Name: "name", Type: String, Required: true})
	verr := CheckRequest(c, NewRequest(map[string]any{"encoding": "b64"}, false, false))
	if verr == nil || verr.Code != "core.input.missing" {
		t.Errorf("err = %v, want core.input.missing", verr)
	}
	verr = CheckRequest(c, NewRequest(map[string]any{"encoding": "b64", "name": "x"}, false, false))
	if verr == nil || verr.Code != "core.input.option" {
		t.Errorf("err = %v, want core.input.option once nothing is missing", verr)
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
	// A list is held element by element: a check of the string case alone
	// validated this, and every call that left the input alone was then
	// refused.
	p.Capabilities[0].Inputs = []Field{{Name: "kinds", Type: StringSlice, Default: []string{"red", "green"}, Options: []string{"red", "blue"}}}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "not one of its options") {
		t.Errorf("err = %v, want the list default refused", err)
	}
	p.Capabilities[0].Inputs = []Field{{Name: "kinds", Type: StringSlice, Default: []string{"red"}, Options: []string{"red", "blue"}}}
	if err := p.Validate(); err != nil {
		t.Errorf("a default among its options was refused: %v", err)
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

// A value whose shape the accessor cannot read was handed to the handler as
// the zero. mysql's tls is a String offering false, preferred, true and
// skip-verify: `tls: true` in the config arrived as a boolean, the handler
// read "", and go-sql-driver reads "" as no TLS — plaintext, where the
// operator asked for verified TLS. `tls: "true"` or `tls: yes` on a Bool read
// false the same way. Refused now, on every surface, naming the config key
// and never the value.
func TestAValueOfAShapeTheAccessorCannotReadIsRefused(t *testing.T) {
	ran := false
	c := Capability{
		ID: "db.status", Summary: "s", Safety: Read,
		Run: func(context.Context, Request) (view.View, error) { ran = true; return nil, nil },
		Inputs: []Field{
			{Name: "tls", Type: String, Default: "preferred", Config: "tls", Local: true,
				Options: []string{"false", "preferred", "true", "skip-verify"}},
			{Name: "rtls", Type: Bool, Config: "rtls", Local: true},
			{Name: "tags", Type: StringSlice, Config: "tags"},
			{Name: "out", Type: Path, Config: "out"},
		},
	}
	guarded := GuardInputs(c)
	for _, tc := range []struct {
		key   string
		value any
		want  string
	}{
		{"tls", true, "db.status takes text for tls, not a boolean"},
		{"tls", uint64(1), "db.status takes text for tls, not a number"},
		{"rtls", "true", "db.status takes true or false for rtls, not text"},
		{"rtls", "yes", "db.status takes true or false for rtls, not text"},
		{"out", []any{"a"}, "db.status takes text for out, not a list"},
	} {
		for _, surface := range []Surface{SurfaceCLI, SurfaceMCP, SurfaceTUI} {
			req := ResolveRequest(c, Inputs{Config: map[string]any{tc.key: tc.value}}, false, false).WithSurface(surface)
			_, err := guarded(context.Background(), req)
			verr := view.AsError(err, "test")
			if err == nil || verr.Code != "core.input.type" {
				t.Errorf("%s %v over %s: %v, want core.input.type", tc.key, tc.value, surface, err)
				continue
			}
			if want := tc.want + ", which the config's plugins.db." + tc.key + " sets"; verr.Message != want {
				t.Errorf("message %q, want %q", verr.Message, want)
			}
			if text, ok := tc.value.(string); ok && strings.Contains(verr.Message, strconv.Quote(text)) {
				t.Errorf("the value was echoed: %q", verr.Message)
			}
		}
	}
	if ran {
		t.Error("the handler ran on a value it would have read as the zero")
	}
	// A block under a config key is a section, not a value, and is not
	// read at all; from a profile it is a value, and refused.
	req := ResolveRequest(c, Inputs{Profile: map[string]any{"tags": map[string]any{"a": "b"}}, ProfileName: "p"}, false, false)
	if verr := CheckInputs(c, req); verr == nil ||
		verr.Message != `db.status takes a list for tags, not a block, which the profile "p" sets` {
		t.Errorf("a block from a profile: %v", verr)
	}
	// The hint says what to write in the file, and a caller's value — a
	// tile's with: — how to give it.
	req = ResolveRequest(c, Inputs{Config: map[string]any{"rtls": "true"}}, false, false).WithSurface(SurfaceCLI)
	if verr := CheckInputs(c, req); !strings.HasPrefix(verr.Hint, "write it there unquoted, as `true` or `false` — ") {
		t.Errorf("config hint %q", verr.Hint)
	}
	if verr := CheckInputs(c, NewRequest(map[string]any{"tls": true}, false, false)); verr == nil ||
		!strings.Contains(verr.Hint, "quoted") {
		t.Errorf("caller refusal %v", verr)
	}
	// And what the accessor reads is let through, a bare string in a list
	// slot included.
	for _, values := range []map[string]any{
		{"tls": "true", "rtls": true, "tags": "one", "out": "/tmp/x"},
		{"tags": []any{"a", 1}},
		{"tags": []string{"a"}},
	} {
		if _, err := guarded(context.Background(), NewRequest(values, false, false)); err != nil {
			t.Errorf("%v was refused: %v", values, err)
		}
	}
}

// A Local input is one the MCP bridge strips from an agent's arguments, so
// telling the agent an argument overrides the config sent it to retry with
// an input its schema hides, into the same refusal. And a pinned plugin's
// section is written `pg@<pin>:`, so the key is named under that heading,
// not under a bare namespace the file does not have.
func TestARefusalNamesTheLineAndOnlyAnOverrideThatExists(t *testing.T) {
	c := Capability{
		ID: "pg.status", Summary: "s", Safety: Read,
		Run: func(context.Context, Request) (view.View, error) { return nil, nil },
		Inputs: []Field{
			{Name: "port", Type: Int, Default: 5432, Min: 1, Max: 65535, Config: "port", Local: true},
			{Name: "limit", Type: Int, Default: 10, Min: 1, Max: 100, Config: "limit"},
		},
	}
	pinned := func(key string, v any) Inputs {
		return Inputs{Config: map[string]any{key: v}, ConfigSection: "pg@1a2b3c4d"}
	}
	verr := CheckInputs(c, ResolveRequest(c, pinned("port", "5432"), false, false).WithSurface(SurfaceMCP))
	if verr == nil || !strings.HasSuffix(verr.Message, "which the config's plugins.pg@1a2b3c4d.port sets") {
		t.Fatalf("message: %v", verr)
	}
	if verr.Hint != "only the operator can change it (the config's plugins.pg@1a2b3c4d.port)" {
		t.Errorf("a Local input's hint over MCP: %q", verr.Hint)
	}
	// Not Local: the argument is real, and the hint still offers it.
	verr = CheckInputs(c, ResolveRequest(c, pinned("limit", "5"), false, false).WithSurface(SurfaceMCP))
	if verr == nil || !strings.Contains(verr.Hint, "an argument naming limit overrides it for this call") {
		t.Errorf("a caller-settable input's hint over MCP: %v", verr)
	}
	// On the CLI a flag does override a Local input.
	verr = CheckInputs(c, ResolveRequest(c, pinned("port", "5432"), false, false).WithSurface(SurfaceCLI))
	if verr == nil || !strings.HasSuffix(verr.Hint, "or give port on the call to override it for one run") {
		t.Errorf("a Local input's hint on the CLI: %v", verr)
	}
	// No heading given is the namespace, every built-in's.
	verr = CheckInputs(c, ResolveRequest(c, Inputs{Config: map[string]any{"port": "1"}}, false, false))
	if verr == nil || !strings.Contains(verr.Message, "the config's plugins.pg.port") {
		t.Errorf("no heading: %v", verr)
	}
}

// A declared bound is quoted back to the person in a refusal and beside the
// input in explain, and they type it: an untyped 1e6 arrives as a float64,
// which %v spelled "1e+06", a value no flag parser takes. Whole numbers are
// written as whole numbers, an int64 edge keeps every digit, and a fraction
// stays one.
func TestABoundIsWrittenTheWayItIsTyped(t *testing.T) {
	for _, tc := range []struct {
		f    Field
		want string
	}{
		{Field{Type: Int, Min: 1, Max: 1e6}, "from 1 to 1000000"},
		{Field{Type: Int, Max: int64(9223372036854775807)}, "of at most 9223372036854775807"},
		{Field{Type: Float, Min: 0.5}, "of at least 0.5"},
		{Field{Type: Float, Min: 1e-7, Max: 2.5e9}, "from 0.0000001 to 2500000000"},
	} {
		if got := tc.f.Bounds(); got != tc.want {
			t.Errorf("Bounds(%v, %v) = %q, want %q", tc.f.Min, tc.f.Max, got, tc.want)
		}
	}
}
