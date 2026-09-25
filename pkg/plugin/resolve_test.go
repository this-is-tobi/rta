package plugin

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
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
	got := Resolve(numeric(), Inputs{Caller: map[string]any{"limit": 5}})
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
	Resolve(numeric(), Inputs{Caller: in})
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
		req := NewRequest(Resolve(numeric(), Inputs{Caller: map[string]any{"limit": v}}), false, false)
		if got := req.Int("limit"); got != 5 {
			t.Errorf("%T(%v) resolved to %d, want 5", v, v, got)
		}
	}
	// And a value that is not a number at all is left alone rather than
	// replaced with a confident zero.
	got := Resolve(numeric(), Inputs{Caller: map[string]any{"limit": "not a number"}})
	if got["limit"] != "not a number" {
		t.Errorf("a non-numeric value was rewritten: %v", got["limit"])
	}
}

// An integer that does not fit is refused rather than wrapped. YAML hands a
// literal past 2^63 over as a uint64, JSON hands 1e300 over as a float64,
// and int(n) on either is a large negative number — which a bound check
// then reads as "below the minimum" and a handler as a count that cannot
// be.
func TestResolveRefusesAnIntegerThatDoesNotFit(t *testing.T) {
	for _, v := range []any{
		uint64(math.MaxUint64), uint64(math.MaxInt64) + 1,
		float64(math.MaxUint64), 1e300, -1e300, math.Inf(1), math.NaN(),
	} {
		if got, ok := toInt(v); ok {
			t.Errorf("%T(%v) was accepted as %d", v, v, got)
		}
	}
	if got, ok := toInt(uint64(math.MaxInt64)); !ok || got != math.MaxInt64 {
		t.Errorf("the largest int was refused: %d, %v", got, ok)
	}
}

// `net ping --timeout 0` reached time.NewTicker(0) inside a library goroutine
// and aborted the process. Over MCP that is one schema-valid call from an
// unprivileged agent killing the server for every other tool.
//
// So a handler must never see an out-of-range value — and it used to be
// spared one by clamping, which answered a different question: `net listen
// --port 70000` reported on port 65535. Refused now, with the range named;
// the edges are inside it.
func TestAnOutOfRangeNumberIsRefusedRatherThanMoved(t *testing.T) {
	ran := false
	c := numeric()
	c.Run = func(context.Context, Request) (view.View, error) { ran = true; return nil, nil }
	guarded := GuardInputs(c)
	call := func(values map[string]any) *view.Error {
		t.Helper()
		_, err := guarded(context.Background(), NewRequest(Resolve(c, Inputs{Caller: values}), false, false))
		if err == nil {
			return nil
		}
		return view.AsError(err, "test")
	}
	for _, in := range []any{0, -5, 301, 9000} {
		verr := call(map[string]any{"timeout": in})
		if verr == nil || verr.Code != "core.input.range" || !strings.Contains(verr.Message, "from 1 to 300") {
			t.Errorf("timeout %v: %v, want core.input.range naming the range", in, verr)
		}
	}
	// Floats too, and a bound of 0 is a real bound rather than "unset".
	for _, in := range []float64{-1, 2} {
		if verr := call(map[string]any{"ratio": in}); verr == nil || verr.Code != "core.input.range" {
			t.Errorf("ratio %v was accepted", in)
		}
	}
	if ran {
		t.Error("the handler ran on an out-of-range value")
	}
	for _, values := range []map[string]any{{"timeout": 1}, {"timeout": 300}, {"ratio": 0.0}, {"ratio": 1.0}, {"limit": -3}} {
		if verr := call(values); verr != nil {
			t.Errorf("%v was refused: %v", values, verr)
		}
	}
	// Resolve itself moves nothing: the value the handler would see is the
	// value that was sent, and the guard is what decides.
	if got := Resolve(c, Inputs{Caller: map[string]any{"timeout": 9000}})["timeout"]; got != 9000 {
		t.Errorf("Resolve changed an out-of-range value to %v", got)
	}
}

// A number no accessor can read was let through as "not a number, nothing to
// hold it to", and Request.Int then read it as 0 — below nearly every Min,
// and the one value the bound exists to keep out. `net ping` over MCP with a
// timeout of 2^63 reached time.NewTicker(0) that way and `rta mcp serve`
// exited; so did a config file quoting the timeout. Refused now, before the
// handler: as a range on a bounded field, as a type on an unbounded one.
func TestANumberNoAccessorCanReadIsRefused(t *testing.T) {
	ran := false
	c := numeric()
	c.Run = func(context.Context, Request) (view.View, error) { ran = true; return nil, nil }
	guarded := GuardInputs(c)
	for _, tc := range []struct {
		input string
		v     any
		code  string
	}{
		{"timeout", float64(1 << 63), "core.input.range"},
		{"timeout", uint64(math.MaxUint64), "core.input.range"},
		{"timeout", "0", "core.input.range"},
		{"timeout", true, "core.input.range"},
		{"ratio", "0.5", "core.input.range"},
		{"limit", "15", "core.input.type"},
		{"limit", math.NaN(), "core.input.type"},
	} {
		_, err := guarded(context.Background(), NewRequest(Resolve(c, Inputs{Caller: map[string]any{tc.input: tc.v}}), false, false))
		verr := view.AsError(err, "test")
		if err == nil || verr.Code != tc.code {
			t.Errorf("%s = %T(%v): %v, want %s", tc.input, tc.v, tc.v, err, tc.code)
		}
	}
	if ran {
		t.Error("the handler ran on a number it would have read as 0")
	}
	// Named as text, so "0" is not mistaken for the number it spells — and
	// text holding a number inside the range is not told about the range:
	// `limit: "3"` was refused as "takes a limit from 1 to 1000, not "3"",
	// which sent the operator to change a number that was right.
	for _, v := range []any{"0", "30"} {
		_, err := guarded(context.Background(), NewRequest(map[string]any{"timeout": v}, false, false))
		verr := view.AsError(err, "test")
		if err == nil || verr.Message != "x.y takes a whole number from 1 to 300 for timeout, not text" {
			t.Errorf("%q: err = %v, want the value named as text", v, err)
		}
		if !strings.Contains(verr.Hint, "without quotes") {
			t.Errorf("%q: hint %q", v, verr.Hint)
		}
	}
	// A number is shown: what is wrong with it is its value.
	_, err := guarded(context.Background(), NewRequest(map[string]any{"timeout": uint64(math.MaxUint64)}, false, false))
	if err == nil || !strings.Contains(view.AsError(err, "test").Message, "not 18446744073709551615") {
		t.Errorf("err = %v, want the number shown", err)
	}
	// From the operator's config, the hint says what to write there, which
	// the readdressed hint dropped.
	c.Inputs[2].Config = "limit"
	verr := CheckInputs(c, ResolveRequest(c, Inputs{Config: map[string]any{"limit": "15"}}, false, false).WithSurface(SurfaceCLI))
	if verr == nil {
		t.Fatal("a config number written as text was accepted")
	}
	if !strings.HasPrefix(verr.Hint, "write it there as a bare number, without quotes — ") ||
		!strings.HasSuffix(verr.Message, "not text, which the config's plugins.x.limit sets") {
		t.Errorf("config text: %v / %q", verr, verr.Hint)
	}
	// A present nil says nothing was given, as an absent key does.
	if _, err := guarded(context.Background(), NewRequest(map[string]any{"limit": nil}, false, false)); err != nil {
		t.Errorf("a nil value was refused: %v", err)
	}
}

// A YAML key written with no value decodes as nil — `with: {timeout: ~}` on
// a tile, or `timeout:` left empty in a profile — and the guard reads a
// present nil as nothing given. Resolve wrote it over the default all the
// same, so the handler read the zero: net.ping's timeout of 0 is
// time.NewTicker(0), and the TUI exited on the tile's first refresh.
func TestANilValueLeavesTheLayerUnderItStanding(t *testing.T) {
	c := numeric()
	c.Inputs[0].Config = "timeout" // so a profile may fill it
	got := -1
	c.Run = func(_ context.Context, r Request) (view.View, error) { got = r.Int("timeout"); return nil, nil }
	guarded := GuardInputs(c)
	for _, in := range []Inputs{
		{Caller: map[string]any{"timeout": nil}},
		{Profile: map[string]any{"timeout": nil}, ProfileName: "p"},
	} {
		got = -1
		if _, err := guarded(context.Background(), NewRequest(Resolve(c, in), false, false)); err != nil {
			t.Errorf("%+v: %v", in, err)
		}
		if got != 10 {
			t.Errorf("%+v: the handler read timeout %d, want the default 10", in, got)
		}
	}
	// And under the caller's nil, the operator's value.
	if v := Resolve(c, Inputs{Caller: map[string]any{"timeout": nil}, Config: map[string]any{"timeout": 30}})["timeout"]; v != 30 {
		t.Errorf("a nil from the caller hid the config value: %v", v)
	}
}

// Undeclared values pass through: the MCP bridge and Page both overlay keys
// the capability never declared, and Resolve is not the place to police that.
func TestResolveLeavesUndeclaredValuesAlone(t *testing.T) {
	got := Resolve(numeric(), Inputs{Caller: map[string]any{"detail": true, "surprise": uint64(7)}})
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

// configurable is a capability whose inputs name config keys, including a
// nested one.
func configurable() Capability {
	return Capability{
		ID: "pg.query", Summary: "query", Safety: Read,
		Run: func(context.Context, Request) (view.View, error) { return nil, nil },
		Inputs: []Field{
			{Name: "host", Type: String, Help: "h", Config: "host"},
			{Name: "port", Type: Int, Help: "p", Default: 5432, Config: "port"},
			{Name: "mode", Type: String, Help: "m", Default: "prefer", Config: "tls.mode"},
			{Name: "sql", Type: String, Help: "s"},
		},
	}
}

// Caller, then config, then Default — and a handler cannot tell which of the
// three it got, which is what makes a config-backed input an ordinary input.
func TestConfigBeatsADefaultAndLosesToTheCaller(t *testing.T) {
	cfg := map[string]any{
		"host": "db.internal",
		"port": uint64(6543), // what goccy-yaml hands back for a plain integer
		"tls":  map[string]any{"mode": "require"},
	}
	got := Resolve(configurable(), Inputs{Caller: map[string]any{"host": "typed.example"}, Config: cfg})

	if got["host"] != "typed.example" {
		t.Errorf("host = %v, want the caller's value to win", got["host"])
	}
	if got["port"] != 6543 {
		t.Errorf("port = %#v, want config to beat the declared default and normalise to int", got["port"])
	}
	if got["mode"] != "require" {
		t.Errorf("mode = %v, want the nested config key to beat the default", got["mode"])
	}
	if _, ok := got["sql"]; ok {
		t.Errorf("sql = %v, but no config key names it and it has no default", got["sql"])
	}
}

// Config cannot reach an input whose author did not offer it. That is what
// keeps the reachable set a property of the declaration — checkable before
// the process runs, printable by `rta explain` — rather than a property of
// whatever happens to be in a file.
func TestConfigCannotFillAnInputThatDeclaredNoKey(t *testing.T) {
	got := Resolve(configurable(), Inputs{Config: map[string]any{"sql": "DROP TABLE users"}})
	if v, ok := got["sql"]; ok {
		t.Errorf("sql = %v, but the input declares no config key", v)
	}
}

// A nested block is a namespace, not a value. Handing a map to Request.String
// would stringify a Go map into somebody's connection string.
func TestANestedBlockIsNotItselfAValue(t *testing.T) {
	got := Resolve(configurable(), Inputs{Config: map[string]any{
		"host": map[string]any{"primary": "a", "replica": "b"},
	}})
	if v, ok := got["host"]; ok {
		t.Errorf("host = %#v, want a block to be skipped rather than stringified", v)
	}
}

// Bounds still apply to a value that arrived from config or a profile — an
// operator's file is no more trusted to respect a declared Max than a caller
// is — but by holding it inside each capability's own range rather than by
// refusing it. One key serves every capability that declares it: net's
// `timeout` is read by net.ping up to 300 and by net.port up to 60, and
// refused, `timeout: 90` failed every net.port call while net.ping ran.
func TestAnOperatorNumberIsHeldInsideEachCapabilitysOwnRange(t *testing.T) {
	capWith := func(id string, max int) Capability {
		return Capability{
			ID: id, Summary: "s", Safety: Read,
			Run:    func(context.Context, Request) (view.View, error) { return nil, nil },
			Inputs: []Field{{Name: "timeout", Type: Int, Help: "t", Default: 5, Min: 1, Max: max, Config: "timeout"}},
		}
	}
	ping, port := capWith("net.ping", 300), capWith("net.port", 60)
	for _, tc := range []struct {
		c    Capability
		in   Inputs
		want int
	}{
		{ping, Inputs{Config: map[string]any{"timeout": uint64(90)}}, 90},
		{port, Inputs{Config: map[string]any{"timeout": uint64(90)}}, 60},
		{port, Inputs{Config: map[string]any{"timeout": 0}}, 1},
		{port, Inputs{Profile: map[string]any{"timeout": 90}, ProfileName: "slow"}, 60},
		// The profile beats config, and is held the same way.
		{port, Inputs{Profile: map[string]any{"timeout": 90}, ProfileName: "slow", Config: map[string]any{"timeout": 30}}, 60},
	} {
		req := NewRequest(Resolve(tc.c, tc.in), false, false)
		if verr := CheckInputs(tc.c, req); verr != nil {
			t.Errorf("%s %+v: refused: %v", tc.c.ID, tc.in, verr)
		}
		if got := req.Int("timeout"); got != tc.want {
			t.Errorf("%s %+v: timeout = %d, want %d", tc.c.ID, tc.in, got, tc.want)
		}
	}
	// The caller's own value is still refused, whatever config says: that
	// is a question this capability does not answer.
	req := NewRequest(Resolve(port, Inputs{Caller: map[string]any{"timeout": 90}, Config: map[string]any{"timeout": 30}}), false, false)
	if verr := CheckInputs(port, req); verr == nil || verr.Code != "core.input.range" {
		t.Errorf("a caller's out-of-range value was accepted: %v", verr)
	}
	// A Float the same way.
	ratio := Capability{
		ID: "x.ratio", Summary: "s", Safety: Read,
		Run:    func(context.Context, Request) (view.View, error) { return nil, nil },
		Inputs: []Field{{Name: "ratio", Type: Float, Default: 0.5, Min: 0.0, Max: 1.0, Config: "ratio"}},
	}
	if got := NewRequest(Resolve(ratio, Inputs{Config: map[string]any{"ratio": 2.5}}), false, false).Float("ratio"); got != 1.0 {
		t.Errorf("a config ratio of 2.5 resolved to %v, want 1", got)
	}
	// And a config value that is not a number is not moved: there is no
	// number to move, and the guard refuses it.
	req = NewRequest(Resolve(port, Inputs{Config: map[string]any{"timeout": "90"}}), false, false)
	if verr := CheckInputs(port, req); verr == nil || verr.Code != "core.input.range" {
		t.Errorf("a quoted config number was accepted: %v", verr)
	}
}

// nil config is every surface that has none and the whole world before this
// existed. Nothing changes.
func TestNilConfigResolvesExactlyAsBefore(t *testing.T) {
	got := Resolve(configurable(), Inputs{Caller: map[string]any{"sql": "SELECT 1"}})
	if got["port"] != 5432 || got["mode"] != "prefer" || got["sql"] != "SELECT 1" {
		t.Errorf("resolved %v, want declared defaults plus the caller's value", got)
	}
	if _, ok := got["host"]; ok {
		t.Errorf("host = %v, but it has no default and nothing supplied it", got["host"])
	}
}

// A fraction is not a whole number. toInt truncated it, so `count: 2.5` in
// the config ran net.ping twice, `limit: 0.5` became 0 and then 1, and doctor
// called the file fine — while `--count 2.5` and the same value over MCP were
// refused. Refused from every layer now, naming where it came from.
func TestAFractionIsNotReadAsAWholeNumber(t *testing.T) {
	for _, v := range []any{2.5, float32(0.5), 9.99, json.Number("2.5"), -0.25} {
		if n, ok := toInt(v); ok {
			t.Errorf("%T(%v) was read as %d", v, v, n)
		}
	}
	for _, v := range []any{2.0, float32(3), json.Number("4"), -1.0} {
		if _, ok := toInt(v); !ok {
			t.Errorf("%T(%v), a whole number, was refused", v, v)
		}
	}
	c := numeric()
	c.Inputs[0].Config = "timeout"
	c.Inputs[2].Config = "limit"
	for _, tc := range []struct {
		key, want, code string
	}{
		{"timeout", "x.y takes a whole number from 1 to 300 for timeout, not 2.5, which the config's plugins.x.timeout sets", "core.input.range"},
		{"limit", "x.y takes a whole number for limit, not 2.5, which the config's plugins.x.limit sets", "core.input.type"},
	} {
		verr := CheckInputs(c, ResolveRequest(c, Inputs{Config: map[string]any{tc.key: 2.5}}, false, false).WithSurface(SurfaceCLI))
		if verr == nil || verr.Code != tc.code || verr.Message != tc.want {
			t.Errorf("%s: %v, want %s %q", tc.key, verr, tc.code, tc.want)
			continue
		}
		if !strings.HasPrefix(verr.Hint, "write it there as a whole number — ") {
			t.Errorf("%s: hint %q", tc.key, verr.Hint)
		}
	}
	// And reported before any call, as a fraction rather than as a number
	// too large to hold.
	problem, hint := StatedTypeProblem(Field{Name: "count", Type: Int}, 2.5)
	if !strings.Contains(problem, "fractional") || hint != "write a whole number" {
		t.Errorf("reported as %q / %q", problem, hint)
	}
	if problem, _ := StatedTypeProblem(Field{Name: "ratio", Type: Float}, 2.5); problem != "" {
		t.Errorf("a fraction for a Float was reported: %s", problem)
	}
}

// JSON may spell a whole number `5.0` or `1e2`. Decoded as a float64 each was
// read as the number it is; decoded with UseNumber, which is how a number
// keeps its digits past 2^53, Int64 parsed digits alone and the same value
// was not a number at all. The same reading either way now, the fraction and
// the edge of what int holds included.
func TestAJSONNumberIsReadAsTheNumberItSpells(t *testing.T) {
	for in, want := range map[json.Number]int{"5": 5, "5.0": 5, "1e2": 100, "-3E0": -3} {
		if n, ok := toInt(in); !ok || n != want {
			t.Errorf("%s: read as %d, %v; want %d", in, n, ok, want)
		}
	}
	for _, in := range []json.Number{"2.5", "1e-1", "9223372036854775808", "1e400"} {
		if n, ok := toInt(in); ok {
			t.Errorf("%s was read as %d", in, n)
		}
	}
}
