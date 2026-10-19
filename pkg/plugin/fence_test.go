package plugin

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// A fence compares by the input's declared type, never by how a value prints:
// the next spelling that reads as the same value is `yes` for `true`, and a
// fence that took it would be one a spelling gets through.
func TestFenceAllowsComparesByDeclaredType(t *testing.T) {
	for _, tc := range []struct {
		name    string
		f       Field
		allowed []any
		v       any
		want    bool
	}{
		{"text, exact", Field{Type: String}, []any{"staging"}, "staging", true},
		{"text, another case", Field{Type: String}, []any{"staging"}, "Staging", false},
		{"text outside", Field{Type: String}, []any{"staging"}, "prod", false},
		{"an option in another case", Field{Type: String, Options: []string{"fast", "safe"}}, []any{"safe"}, "SAFE", true},
		{"a number, whatever integer shape arrived", Field{Type: Int}, []any{uint64(5432)}, float64(5432), true},
		{"a number as text is not the number", Field{Type: Int}, []any{5432}, "5432", false},
		{"a fraction is not a whole number", Field{Type: Int}, []any{5432}, 5432.5, false},
		{"a boolean, strictly", Field{Type: Bool}, []any{true}, true, true},
		{"a boolean written as text", Field{Type: Bool}, []any{true}, "true", false},
		{"a duration in another unit", Field{Type: Duration}, []any{"30s"}, "30000ms", true},
		{"a duration as a Go value", Field{Type: Duration}, []any{"30s"}, 30 * time.Second, true},
		{"a duration that differs", Field{Type: Duration}, []any{"30s"}, "31s", false},
		{"a float", Field{Type: Float}, []any{0.5}, 0.5, true},
	} {
		if got := FenceAllows(tc.f, tc.allowed, tc.v); got != tc.want {
			t.Errorf("%s: FenceAllows = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// **A value that is not there is the zero of its type**, so the only way a
// blank gets through is a list that holds the blank.
func TestFenceAllowsReadsNothingAsTheZeroOfTheType(t *testing.T) {
	for _, tc := range []struct {
		name    string
		f       Field
		allowed []any
		v       any
		want    bool
	}{
		{"text left out", Field{Type: String}, []any{"staging"}, nil, false},
		{"the empty string", Field{Type: String}, []any{"staging"}, "", false},
		{"the empty string, listed", Field{Type: String}, []any{"", "staging"}, "", true},
		{"zero, unlisted", Field{Type: Int}, []any{5432}, nil, false},
		{"zero, listed", Field{Type: Int}, []any{0, 5432}, nil, true},
		{"false left out", Field{Type: Bool}, []any{true}, nil, false},
		{"false, listed", Field{Type: Bool}, []any{false}, nil, true},
	} {
		if got := FenceAllows(tc.f, tc.allowed, tc.v); got != tc.want {
			t.Errorf("%s: FenceAllows = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A list-shaped input is held element by element, and an empty list is the
// widest reach there is: most handlers read it as "no filter".
func TestFenceAllowsHoldsAListEveryElement(t *testing.T) {
	f := Field{Type: StringSlice}
	allowed := []any{"a", "b"}
	for name, tc := range map[string]struct {
		v    any
		want bool
	}{
		"every element inside":      {[]string{"a", "b"}, true},
		"one element":               {[]any{"a"}, true},
		"a bare string is one":      {"b", true},
		"one element outside":       {[]string{"a", "z"}, false},
		"an empty list":             {[]string{}, false},
		"nothing at all":            {nil, false},
		"the empty string is empty": {"", false},
		"a blank among them":        {[]string{"a", ""}, false},
	} {
		if got := FenceAllows(f, allowed, tc.v); got != tc.want {
			t.Errorf("%s: FenceAllows(%v) = %v, want %v", name, tc.v, got, tc.want)
		}
	}
}

func TestFenceProblemNamesWhatIsWrongWithoutQuotingIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    Field
		list any
		want string
	}{
		{"a scalar", Field{Type: String}, "secret-value", "where a list of allowed values is declared"},
		{"empty", Field{Type: String}, []any{}, "lists no value"},
		{"the wrong type", Field{Type: Int}, []any{"secret-value"}, "text where an integer is declared"},
		{"nothing", Field{Type: String}, []any{nil}, "written as nothing"},
		{"outside the options", Field{Type: String, Options: []string{"a"}}, []any{"secret-value"}, "does not accept"},
		{"outside the range", Field{Type: Int, Min: 1, Max: 10}, []any{11}, "outside what the input takes"},
		{"a bare duration", Field{Type: Duration}, []any{30}, "a duration is declared"},
	} {
		problem, hint := FenceProblem(tc.f, tc.list)
		if !strings.Contains(problem, tc.want) {
			t.Errorf("%s: problem = %q, want %q", tc.name, problem, tc.want)
		}
		if hint == "" {
			t.Errorf("%s: no hint", tc.name)
		}
		if strings.Contains(problem+hint, "secret-value") {
			t.Errorf("%s: the report quotes the file: %q %q", tc.name, problem, hint)
		}
	}
	for _, ok := range []struct {
		f    Field
		list any
	}{
		{Field{Type: String}, []any{"a", "b"}},
		{Field{Type: StringSlice}, []string{"a"}},
		{Field{Type: Int, Min: 1, Max: 10}, []any{1, uint64(10)}},
		{Field{Type: Bool}, []any{true, false}},
		{Field{Type: Duration}, []any{"30s", "1m"}},
	} {
		if problem, _ := FenceProblem(ok.f, ok.list); problem != "" {
			t.Errorf("%v: a good list was refused: %s", ok.list, problem)
		}
	}
}

func fenceCapability() Capability {
	return Capability{ID: "app.status", Inputs: []Field{
		{Name: "namespace", Type: String, Config: "namespace"},
		{Name: "all-namespaces", Type: Bool, Config: "all-namespaces", Widens: "namespace"},
		{Name: "names", Type: StringSlice, Config: "names"},
		{Name: "limit", Type: Int, Config: "limit", Default: 10},
	}}
}

// An input a fence allows one value of is that value when nobody said
// otherwise — over the plugin's own default, under every layer that states
// something. The value that wins is held to the list afterwards.
func TestAFenceWithOneValueIsTheDefaultOfTheInput(t *testing.T) {
	c := fenceCapability()
	fence := Fence{"namespace": {"staging"}, "names": {"a"}, "limit": {20}}
	got := Resolve(c, Inputs{Fence: fence})
	if got["namespace"] != "staging" {
		t.Errorf("namespace = %v, want the only value the fence allows", got["namespace"])
	}
	if names, _ := got["names"].([]string); len(names) != 1 || names[0] != "a" {
		t.Errorf("names = %#v, want a list of the one allowed value", got["names"])
	}
	if got["limit"] != 20 {
		t.Errorf("limit = %v, want the fence's value over the declared default of 10", got["limit"])
	}

	// Every layer that states something beats it, and is then checked.
	for name, in := range map[string]Inputs{
		"the caller":  {Fence: fence, Caller: map[string]any{"namespace": "prod"}},
		"the profile": {Fence: fence, Profile: map[string]any{"namespace": "prod"}, ProfileName: "p"},
		"the config":  {Fence: fence, Config: map[string]any{"namespace": "prod"}},
	} {
		if got := Resolve(c, in); got["namespace"] != "prod" {
			t.Errorf("%s: namespace = %v, want what it stated — the fence refuses it after, it does not rewrite it", name, got["namespace"])
		}
		if CheckFence(c, in) == nil {
			t.Errorf("%s: a value outside the fence was not refused", name)
		}
	}

	// Two entries is no default for an input that holds one value: somebody has
	// to say which.
	two := Fence{"namespace": {"staging", "staging-jobs"}}
	if got := Resolve(c, Inputs{Fence: two}); got["namespace"] != nil {
		t.Errorf("namespace = %v from a list of two", got["namespace"])
	}
}

// An input that holds a list is every value its fence allows, when nobody
// supplied one: the list is what the connection reaches, and a call that names
// none has asked for what it may have, which is no more than naming every entry
// would. A handler reads the list it is given, so a connection that fences a
// dozen namespaces is read across all of them without a call per namespace.
func TestAFenceOfSeveralValuesIsTheWholeListOfAListInput(t *testing.T) {
	c := fenceCapability()
	fence := Fence{"names": {"a", "b", "c"}}
	got := Resolve(c, Inputs{Fence: fence})
	if names, _ := got["names"].([]string); !slices.Equal(names, []string{"a", "b", "c"}) {
		t.Fatalf("names = %#v, want every value the fence allows", got["names"])
	}
	if breach := CheckFence(c, Inputs{Fence: fence}); breach != nil {
		t.Errorf("the list the fence itself supplied was refused: %+v", breach)
	}

	// What somebody states still wins, and is still held to the list.
	for name, in := range map[string]Inputs{
		"the caller":  {Fence: fence, Caller: map[string]any{"names": []string{"a"}}},
		"the profile": {Fence: fence, Profile: map[string]any{"names": []string{"b"}}, ProfileName: "p"},
		"the config":  {Fence: fence, Config: map[string]any{"names": []string{"c"}}},
	} {
		if names, _ := Resolve(c, in)["names"].([]string); len(names) != 1 {
			t.Errorf("%s: names = %v, want the one value it stated", name, names)
		}
		if breach := CheckFence(c, in); breach != nil {
			t.Errorf("%s: a value inside the fence was refused: %+v", name, breach)
		}
	}
	outside := Inputs{Fence: fence, Caller: map[string]any{"names": []string{"a", "z"}}}
	if CheckFence(c, outside) == nil {
		t.Error("a list with a value outside the fence was not refused")
	}
}

// The list default is for an input a profile may fill. The one a grant names as
// its scope is checked against the records a call names, before anything is
// resolved, and a list filled in afterwards would be records the gate never saw.
func TestAFenceDoesNotFillAListInputTheGrantNamesAsItsScope(t *testing.T) {
	c := Capability{ID: "demo.pods", Scope: "namespace", Inputs: []Field{
		{Name: "namespace", Type: StringSlice, Config: "namespace"},
	}}
	fence := Fence{"namespace": {"a", "b"}}
	if got := Resolve(c, Inputs{Fence: fence}); got["namespace"] != nil {
		t.Errorf("the scope input was filled with %v by a fence the grant gate never saw", got["namespace"])
	}
	if CheckFence(c, Inputs{Fence: fence}) == nil {
		t.Error("a call that named no value for a fenced scope was not refused")
	}
}

func TestCheckFenceHoldsTheResolvedValueNotWhatWasSent(t *testing.T) {
	c := fenceCapability()
	fence := Fence{"namespace": {"staging", "staging-jobs"}}

	for name, tc := range map[string]struct {
		in    Inputs
		blank bool
		want  bool // a breach
	}{
		"the caller names an allowed value": {Inputs{Caller: map[string]any{"namespace": "staging-jobs"}}, false, false},
		"the profile supplies one":          {Inputs{Profile: map[string]any{"namespace": "staging"}, ProfileName: "p"}, false, false},
		"the caller names another":          {Inputs{Caller: map[string]any{"namespace": "prod"}}, false, true},
		"the caller lifts it with a blank": {Inputs{
			Profile: map[string]any{"namespace": "staging"}, ProfileName: "p",
			Caller: map[string]any{"namespace": ""},
		}, true, true},
		"nobody supplies it": {Inputs{}, true, true},
		"a profile supplies another": {Inputs{
			Profile: map[string]any{"namespace": "prod"}, ProfileName: "p",
		}, false, true},
	} {
		tc.in.Fence = fence
		breach := CheckFence(c, tc.in)
		if (breach != nil) != tc.want {
			t.Errorf("%s: breach = %+v, want a breach: %v", name, breach, tc.want)
			continue
		}
		if breach != nil && (breach.Input.Name != "namespace" || breach.Blank != tc.blank || breach.Widened()) {
			t.Errorf("%s: breach = %+v", name, breach)
		}
	}
	if CheckFence(c, Inputs{Caller: map[string]any{"namespace": "anything"}}) != nil {
		t.Error("a connection with no fence was held to one")
	}
}

// The switch the plugin says widens the fenced input is outside the fence
// however it is set to true — by the caller, a profile, the config or its own
// default — and not outside it when it is off or the input it widens is not
// fenced.
func TestCheckFenceRefusesAWideningSwitchWhileItsInputIsFenced(t *testing.T) {
	c := fenceCapability()
	fence := Fence{"namespace": {"staging"}}
	inside := map[string]any{"namespace": "staging"}

	for name, in := range map[string]Inputs{
		"the caller":  {Caller: map[string]any{"namespace": "staging", "all-namespaces": true}},
		"the profile": {Caller: inside, Profile: map[string]any{"all-namespaces": true}, ProfileName: "p"},
		"the config":  {Caller: inside, Config: map[string]any{"all-namespaces": true}},
	} {
		in.Fence = fence
		breach := CheckFence(c, in)
		if breach == nil || !breach.Widened() || breach.Switch.Name != "all-namespaces" || breach.Input.Name != "namespace" {
			t.Errorf("%s: breach = %+v, want the switch refused for the input it widens", name, breach)
		}
	}
	// A declared default of true is a way to set it too.
	on := fenceCapability()
	on.Inputs[1].Default = true
	if breach := CheckFence(on, Inputs{Fence: fence, Caller: inside}); breach == nil || !breach.Widened() {
		t.Errorf("a switch that is on by default was not refused: %+v", breach)
	}
	// Off is not saying it.
	if breach := CheckFence(c, Inputs{Fence: fence, Caller: map[string]any{"namespace": "staging", "all-namespaces": false}}); breach != nil {
		t.Errorf("the switch left off was refused: %+v", breach)
	}
	// And the same switch widens nothing when the input is not fenced.
	other := Fence{"limit": {10}}
	if breach := CheckFence(c, Inputs{Fence: other, Caller: map[string]any{"all-namespaces": true, "limit": 10}}); breach != nil {
		t.Errorf("a switch was refused for an input that is not fenced: %+v", breach)
	}
}

// An input being typed is not tested for its value, and nothing else is spared:
// the switch that widens it and every other fenced input are held as a run holds
// them. The fence on the input itself stays on the connection, which is what keeps
// its switches in the check.
func TestCheckFenceLeavesOnlyTheValueOfTheUnfinishedInputUntested(t *testing.T) {
	c := fenceCapability()
	fence := Fence{"namespace": {"staging"}, "names": {"a", "b"}}
	valid := map[string]any{"names": []string{"a"}}
	with := func(extra map[string]any, unfinished string) Inputs {
		caller := map[string]any{}
		for k, v := range valid {
			caller[k] = v
		}
		for k, v := range extra {
			caller[k] = v
		}
		return Inputs{Fence: fence, Caller: caller, Unfinished: unfinished}
	}

	for name, value := range map[string]any{"another value": "prod", "a blank": ""} {
		if CheckFence(c, with(map[string]any{"namespace": value}, "")) == nil {
			t.Fatalf("%s: the whole check let the value through, so this proves nothing", name)
		}
		if breach := CheckFence(c, with(map[string]any{"namespace": value}, "namespace")); breach != nil {
			t.Errorf("%s: the value of the input being typed was tested: %+v", name, breach)
		}
	}
	if breach := CheckFence(c, with(map[string]any{"namespace": "pr", "all-namespaces": true}, "namespace")); breach == nil ||
		!breach.Widened() || breach.Switch.Name != "all-namespaces" || breach.Input.Name != "namespace" {
		t.Errorf("a switch that widens the input being typed was let through: %+v", breach)
	}
	if breach := CheckFence(c, with(map[string]any{"namespace": "pr", "names": []string{"z"}}, "namespace")); breach == nil ||
		breach.Input.Name != "names" {
		t.Errorf("another fenced input outside the fence was let through: %+v", breach)
	}
	if breach := CheckFence(c, with(map[string]any{"namespace": "pr", "names": []string{"z"}}, "limit")); breach == nil {
		t.Error("naming an input that is not fenced spared one that is")
	}
}

// A list-shaped input is fenced a name at a time.
func TestCheckFenceHoldsEveryElementOfAListInput(t *testing.T) {
	c := fenceCapability()
	fence := Fence{"names": {"a", "b"}}
	if breach := CheckFence(c, Inputs{Fence: fence, Caller: map[string]any{"names": []string{"a", "b"}}}); breach != nil {
		t.Errorf("a list inside the fence was refused: %+v", breach)
	}
	for name, v := range map[string]any{
		"one outside": []string{"a", "z"},
		"empty":       []string{},
	} {
		if CheckFence(c, Inputs{Fence: fence, Caller: map[string]any{"names": v}}) == nil {
			t.Errorf("%s: not refused", name)
		}
	}
}
