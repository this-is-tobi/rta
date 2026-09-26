package toolcall

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// Direct tests for the MCP argument-validation boundary itself, rather than
// only indirectly through internal/mcp's black-box bridge tests — the gap
// that once let Options on a number go unenforced here: internal/mcp's own
// tests never happened to exercise that shape, so nothing failed either.

func field(name string, typ plugin.FieldType, opts ...string) plugin.Field {
	return plugin.Field{Name: name, Type: typ, Options: opts}
}

func TestValidateAcceptsDeclaredFieldsAndRejectsUnknownOnes(t *testing.T) {
	c := plugin.Capability{ID: "x.y", Inputs: []plugin.Field{
		{Name: "host", Type: plugin.String},
		{Name: "count", Type: plugin.Int},
	}}
	if verr := Validate(c, map[string]any{"host": "h", "count": float64(3)}); verr != nil {
		t.Fatalf("a well-formed call was refused: %v", verr)
	}
	verr := Validate(c, map[string]any{"host": "h", "extra": "surprise"})
	if verr == nil || !strings.Contains(verr.Message, "extra") {
		t.Fatalf("an unknown argument was not refused by name: %v", verr)
	}
}

func TestValidateRejectsAWrongTypeByName(t *testing.T) {
	c := plugin.Capability{ID: "x.y", Inputs: []plugin.Field{{Name: "count", Type: plugin.Int}}}
	verr := Validate(c, map[string]any{"count": "not a number"})
	if verr == nil || !strings.Contains(verr.Message, "count") {
		t.Fatalf("a wrong-typed argument was not refused by name: %v", verr)
	}
}

func TestValidateAcceptsTheHostInjectedDetailAndProfileFields(t *testing.T) {
	// Scope: the field a grant is checked against — never itself
	// profile-fillable (see plugin.ProfileFillable), so a second,
	// Config-keyed field is what actually makes Profilable(c) true here.
	c := plugin.Capability{ID: "x.y", Detailed: true, Scope: "key",
		Inputs: []plugin.Field{
			{Name: "key", Type: plugin.String},
			{Name: "endpoint", Type: plugin.String, Config: "endpoint"},
		}}
	if verr := Validate(c, map[string]any{"detail": true, "profile": "staging"}); verr != nil {
		t.Fatalf("the host's own injected fields were refused: %v", verr)
	}
	if verr := Validate(c, map[string]any{"detail": "not a bool"}); verr == nil {
		t.Fatal("a wrong-typed detail flag was accepted")
	}
}

func TestValidateSkipsLocalFieldsEntirely(t *testing.T) {
	c := plugin.Capability{ID: "x.y", Inputs: []plugin.Field{
		{Name: "identity", Type: plugin.Path, Local: true},
	}}
	// A Local field is never declared to the caller, so a value under its
	// name is a guess, not a typo — Validate must not even type-check it,
	// let alone refuse it as unknown (that would confirm to a model that a
	// hidden input exists).
	if verr := Validate(c, map[string]any{"identity": 12345}); verr != nil {
		t.Fatalf("a Local field's value was type-checked: %v", verr)
	}
}

func TestRequireEnforcesRequiredFieldsAndExemptsLocalOnes(t *testing.T) {
	c := plugin.Capability{ID: "x.y", Inputs: []plugin.Field{
		{Name: "key", Type: plugin.String, Required: true},
		{Name: "identity", Type: plugin.Path, Local: true, Required: true},
	}}
	verr := Require(c, map[string]any{}, false)
	if verr == nil || verr.Code != "core.input.missing" || verr.Message != `x.y needs the argument "key"` {
		t.Fatalf("a missing required field: %v", verr)
	}
	// A Local field declared Required must not make the capability
	// permanently uncallable — it can never arrive from the caller.
	if verr := Require(c, map[string]any{"key": "k"}, false); verr != nil {
		t.Fatalf("a required Local field blocked an otherwise complete call: %v", verr)
	}
	// Sent with nothing in it is not given: the handler would read it
	// exactly as it reads an argument left out.
	if verr := Require(c, map[string]any{"key": ""}, false); verr == nil || verr.Code != "core.input.missing" {
		t.Errorf("an empty required argument: %v", verr)
	}
}

// With a profile named, an input the profile may fill is left to the check in
// front of the handler, which runs once the profile is laid on: the profile is
// resolved after consent, so here it has filled nothing yet. An input no
// profile can fill is still the caller's to send.
func TestRequireLeavesToTheGuardWhatANamedProfileMayFill(t *testing.T) {
	c := plugin.Capability{ID: "db.query", Inputs: []plugin.Field{
		{Name: "database", Type: plugin.String, Required: true, Config: "database"},
		{Name: "sql", Type: plugin.String, Required: true},
	}}
	if verr := Require(c, map[string]any{"sql": "select 1"}, false); verr == nil {
		t.Error("with no profile named, a missing database was accepted")
	}
	if verr := Require(c, map[string]any{"sql": "select 1"}, true); verr != nil {
		t.Errorf("with a profile named, the database it may fill was refused: %v", verr)
	}
	if verr := Require(c, map[string]any{}, true); verr == nil || !strings.Contains(verr.Message, `"sql"`) {
		t.Errorf("with a profile named, the sql no profile fills: %v", verr)
	}
}

// An input the CLI reads from a pipe when it is left out is required here,
// where there is no pipe. codec_jwt {} reached the handler, which could only
// answer that there was no token, while the tool description told the agent
// a missing token was read from standard input; the schema now lists it and
// the call is refused before anything runs.
func TestAPipedInputIsRequiredOverMCP(t *testing.T) {
	c := plugin.Capability{ID: "codec.jwt", Inputs: []plugin.Field{
		{Name: "token", Type: plugin.Secret, Positional: true, Piped: true},
		{Name: "key", Type: plugin.Secret},
	}}
	if got, _ := InputSchema(c, nil)["required"].([]string); !slices.Equal(got, []string{"token"}) {
		t.Errorf("required = %v, want [token]", got)
	}
	verr := Require(c, map[string]any{}, false)
	if verr == nil || verr.Code != "core.input.missing" || !strings.Contains(verr.Message, `needs the argument "token"`) {
		t.Errorf("a call leaving the token out: %v", verr)
	}
	if verr := Require(c, map[string]any{"token": "eyJ"}, false); verr != nil {
		t.Errorf("a call giving it was refused: %v", verr)
	}
}

// 9223372036854775807 in a JSON body decodes to 2^63, one past int64. The
// integer check converted before it compared, and on arm64 int64(2^63)
// saturates to MaxInt64, whose float64 is 2^63 again — so the value passed
// as an integer that nothing downstream could read, and `net_ping` with it
// reached a handler as a timeout of 0. The edges of int64 itself still pass.
// A channel that hands Validate a float64 rather than Decode's exact number
// is still held to that.
func TestValidateRefusesAnIntegerPastWhatInt64Holds(t *testing.T) {
	c := plugin.Capability{ID: "x.y", Inputs: []plugin.Field{{Name: "timeout", Type: plugin.Int}}}
	for _, n := range []float64{1 << 63, -(1 << 64), 1e300, math.Inf(1), math.NaN(), 1.5} {
		if verr := Validate(c, map[string]any{"timeout": n}); verr == nil {
			t.Errorf("%v was accepted as an integer", n)
		}
	}
	for _, n := range []float64{-(1 << 63), 1<<63 - 1024, 0} {
		if verr := Validate(c, map[string]any{"timeout": n}); verr != nil {
			t.Errorf("%v was refused: %v", n, verr)
		}
	}
}

// {"encoding": "HEX"} was a string, missed the enum, and was hinted "encoding
// expects a string" — true, and nothing a model could correct. An enum miss
// names the enum; a wrong type still names the type.
func TestAnEnumMissIsHintedWithTheEnum(t *testing.T) {
	c := plugin.Capability{ID: "gen.token", Inputs: []plugin.Field{
		field("encoding", plugin.String, "hex", "base64"),
		field("tags", plugin.StringSlice, "red", "blue"),
	}}
	for _, args := range []map[string]any{{"encoding": "HEX"}, {"tags": []any{"red", "Blue"}}} {
		verr := Validate(c, args)
		if verr == nil || !strings.Contains(verr.Hint, "one of") || strings.Contains(verr.Hint, "expects") {
			t.Errorf("%v: %v", args, verr)
		}
	}
	verr := Validate(c, map[string]any{"encoding": 3.0})
	if verr == nil || verr.Hint != "encoding expects a string" {
		t.Errorf("a wrong type: %v", verr)
	}
}

// The same mistake answers with the same code on every surface. An option
// miss was core.mcp.badargs over MCP and core.input.option on the CLI, and a
// number outside its range was refused only after the grant gate had spent a
// use on it and the operator had been asked to approve it; both are the
// host's refusal now, in the host's words, before either.
func TestAnOptionOrRangeMissIsTheHostsRefusal(t *testing.T) {
	one := 1
	most := 1024
	c := plugin.Capability{ID: "gen.password", Inputs: []plugin.Field{
		field("encoding", plugin.String, "hex", "base64"),
		{Name: "length", Type: plugin.Int, Min: one, Max: most},
		{Name: "count", Type: plugin.Int},
	}}
	cases := []struct {
		raw, code, says string
	}{
		{`{"encoding": "nope"}`, "core.input.option", `gen.password takes one of hex, base64 for encoding, not "nope"`},
		{`{"length": 0}`, "core.input.range", "gen.password takes a length from 1 to 1024, not 0"},
		// Exactly the number sent. A float64 held neither end: int64's
		// largest was refused as "past what an integer holds", one below its
		// smallest was quoted back as a number nobody sent, and a large one
		// in range of int64 came back rounded.
		{`{"length": 9223372036854775807}`, "core.input.range", "not 9223372036854775807"},
		{`{"length": -9223372036854775809}`, "core.input.range", "not -9223372036854775809"},
		{`{"length": 9223372036854775000}`, "core.input.range", "not 9223372036854775000"},
		{`{"length": 1e400}`, "core.input.range", "not 1e400"},
		// Unbounded, an integer past what the host reads is the host's
		// type refusal, quoting it the same way.
		{`{"count": 18446744073709551616}`, "core.input.type", "not 18446744073709551616"},
	}
	for _, tc := range cases {
		values, err := Decode([]byte(tc.raw))
		if err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		verr := Validate(c, values)
		if verr == nil || verr.Code != tc.code || !strings.Contains(verr.Message, tc.says) {
			t.Errorf("%s: %v, want %s saying %q", tc.raw, verr, tc.code, tc.says)
		}
	}
}

// A whole number is an integer however it is written, as JSON Schema's
// "integer" says: a client that serialises every number as a double sends
// 3.0, and one that shortens sends 1e2. Both reach the handler as what they
// are, and a fraction is still refused as not one.
func TestAWholeNumberIsAnIntegerHoweverItIsWritten(t *testing.T) {
	c := plugin.Capability{ID: "x.y", Inputs: []plugin.Field{{Name: "limit", Type: plugin.Int}}}
	for raw, want := range map[string]int{`{"limit": 3.0}`: 3, `{"limit": 1e2}`: 100, `{"limit": -0}`: 0} {
		values, err := Decode([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if verr := Validate(c, values); verr != nil {
			t.Errorf("%s was refused: %v", raw, verr)
			continue
		}
		req := plugin.ResolveRequest(c, plugin.Inputs{Caller: values}, false, false)
		if got := req.Int("limit"); got != want {
			t.Errorf("%s reached the handler as %d, want %d", raw, got, want)
		}
	}
	values, _ := Decode([]byte(`{"limit": 2.5}`))
	if verr := Validate(c, values); verr == nil || verr.Code != "core.mcp.badargs" {
		t.Errorf("a fraction for an integer: %v", verr)
	}
}

// Decode reads what json.Unmarshal read, numbers aside: an object, null, and
// nothing after it.
func TestDecodeReadsAnObjectAndNothingElse(t *testing.T) {
	if values, err := Decode([]byte(`null`)); err != nil || values != nil {
		t.Errorf("null: %v, %v", values, err)
	}
	for _, raw := range []string{`[1]`, `"x"`, `{"a": 1} {"b": 2}`, `{"a": 1`} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Errorf("%s was decoded", raw)
		}
	}
}

func TestCheckStringSliceAcceptsAScalarAndAnArrayRejectsOthers(t *testing.T) {
	if err := checkStringSlice("bare"); err != nil {
		t.Errorf("a bare string was refused: %v", err)
	}
	if err := checkStringSlice([]any{"a", "b"}); err != nil {
		t.Errorf("an array of strings was refused: %v", err)
	}
	if err := checkStringSlice([]any{"a", float64(1)}); err == nil {
		t.Error("an array with a non-string element was accepted")
	}
	if err := checkStringSlice(float64(1)); err == nil {
		t.Error("a bare number was accepted as a string slice")
	}
}

func TestJSONKindNamesValuesTheWayAReaderThinksOfThem(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{nil, "null"},
		{"x", "a string"},
		{float64(1), "a number"},
		{int64(1), "a number"},
		{json.Number("1e400"), "a number"},
		{true, "a boolean"},
		{[]any{1}, "an array"},
		{map[string]any{}, "an object"},
	}
	for _, tc := range cases {
		if got := JSONKind(tc.v); got != tc.want {
			t.Errorf("JSONKind(%#v) = %q, want %q", tc.v, got, tc.want)
		}
	}
}
