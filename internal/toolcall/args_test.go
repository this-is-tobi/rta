package toolcall

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// Direct tests for the MCP argument-validation boundary itself, rather than
// only indirectly through internal/mcp's black-box bridge tests — the gap
// that let checkEnum's type-switch omission (Int/Float/Bool Options
// silently unenforced) go uncaught: internal/mcp's own tests never happened
// to exercise Options on a non-string field, so nothing here failed either.

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
	if verr := Require(c, map[string]any{}); verr == nil {
		t.Fatal("a missing required field was accepted")
	}
	// A Local field declared Required must not make the capability
	// permanently uncallable — it can never arrive from the caller.
	if verr := Require(c, map[string]any{"key": "k"}); verr != nil {
		t.Fatalf("a required Local field blocked an otherwise complete call: %v", verr)
	}
}

// The fix: Options on an Int, Float or Bool field used to be silently
// unenforced — checkEnum's type switch only ever populated anything to
// check for string and []any, so a numeric or boolean value always found
// nothing to compare against and passed regardless of what Options said.
func TestCheckEnumEnforcesOptionsOnNumericAndBooleanFields(t *testing.T) {
	t.Run("int", func(t *testing.T) {
		f := field("risk", plugin.Int, "1", "2", "3")
		if err := checkEnum(f, float64(2)); err != nil {
			t.Errorf("an allowed int value was refused: %v", err)
		}
		if err := checkEnum(f, float64(999)); err == nil {
			t.Error("an out-of-set int value was accepted")
		}
	})
	t.Run("float", func(t *testing.T) {
		f := field("ratio", plugin.Float, "0.5", "1.5")
		if err := checkEnum(f, 1.5); err != nil {
			t.Errorf("an allowed float value was refused: %v", err)
		}
		if err := checkEnum(f, 3.14); err == nil {
			t.Error("an out-of-set float value was accepted")
		}
	})
	t.Run("bool", func(t *testing.T) {
		// Contrived (a bool has only two values anyway) but the type gap is
		// the same one Int/Float have, and this is the third case
		// checkFieldType's switch feeds into checkEnum.
		f := field("flag", plugin.Bool, "true")
		if err := checkEnum(f, true); err != nil {
			t.Errorf("an allowed bool value was refused: %v", err)
		}
		if err := checkEnum(f, false); err == nil {
			t.Error("an out-of-set bool value was accepted")
		}
	})
	t.Run("string, unaffected", func(t *testing.T) {
		f := field("color", plugin.String, "red", "green")
		if err := checkEnum(f, "red"); err != nil {
			t.Errorf("an allowed string value was refused: %v", err)
		}
		if err := checkEnum(f, "blue"); err == nil {
			t.Error("an out-of-set string value was accepted")
		}
	})
}

// The same enforcement reached through the full Validate path a real MCP
// call goes through, not just the unit-level checkEnum call above.
func TestValidateEnforcesEnumOnAnIntField(t *testing.T) {
	c := plugin.Capability{ID: "x.y", Inputs: []plugin.Field{
		field("risk", plugin.Int, "1", "2", "3"),
	}}
	if verr := Validate(c, map[string]any{"risk": float64(2)}); verr != nil {
		t.Fatalf("an allowed value was refused: %v", verr)
	}
	if verr := Validate(c, map[string]any{"risk": float64(999)}); verr == nil {
		t.Fatal("an out-of-set int value passed Validate despite a declared Options list")
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
