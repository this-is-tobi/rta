package plugin

import (
	"strings"
	"testing"
)

// One capability and one input, spelled the way each surface's reader finds
// them — and a caller inside the process, or a completion keystroke, reads
// the CLI's spelling rather than none.
func TestACapabilityAndAnInputAreNamedTheWayTheirSurfaceNamesThem(t *testing.T) {
	for _, tc := range []struct {
		s          Surface
		capability string
		input      string
	}{
		{SurfaceCLI, "`rta codec jwk`", "--secret-file"},
		{SurfaceMCP, "the `codec_jwk` tool", `the "secret-file" argument`},
		{SurfaceTUI, "`codec.jwk`", "the secret-file box"},
		{SurfaceUnknown, "`rta codec jwk`", "--secret-file"},
		{SurfaceCompletion, "`rta codec jwk`", "--secret-file"},
	} {
		if got := tc.s.CapabilityName("codec.jwk"); got != tc.capability {
			t.Errorf("CapabilityName over %q = %q, want %q", tc.s, got, tc.capability)
		}
		if got := tc.s.InputName("secret-file"); got != tc.input {
			t.Errorf("InputName over %q = %q, want %q", tc.s, got, tc.input)
		}
	}
}

// The tool name is the one rule the bridge registers tools by, so a message
// naming a tool and the tool list cannot spell it two ways.
func TestToolNameReplacesEveryDot(t *testing.T) {
	if got := ToolName("pg.table.list"); got != "pg_table_list" {
		t.Errorf("ToolName = %q", got)
	}
	if got := SurfaceMCP.CapabilityName("pg.table.list"); !strings.Contains(got, ToolName("pg.table.list")) {
		t.Errorf("the MCP spelling %q does not carry the tool name", got)
	}
}

// The one phrase in which an agent may read a command line, spelled for the
// terminal of the person who types it.
func TestAskOperatorSpellsTheCommandForTheOperatorsTerminal(t *testing.T) {
	if got := AskOperator("grant allow kv.get"); got != "ask the operator to run `rta grant allow kv.get`" {
		t.Errorf("AskOperator = %q", got)
	}
}

// A refusal of a declared bound says where the declaration can be read, and
// over MCP that is the schema the tool came with, never a command an agent
// has no terminal to run.
func TestARefusalOfADeclarationPointsWhereItsReaderCanReadIt(t *testing.T) {
	c := Capability{ID: "sys.ps", Summary: "s", Safety: Read,
		Inputs: []Field{{Name: "limit", Type: Int, Min: 1, Max: 1000}}}
	for s, want := range map[Surface]string{
		SurfaceCLI: "`rta explain sys.ps` names it beside the input",
		SurfaceTUI: "`rta explain sys.ps` names it beside the input",
		SurfaceMCP: "the tool's input schema names it",
	} {
		verr := CheckInputs(c, NewRequest(map[string]any{"limit": 0}, false, false).WithSurface(s))
		if verr == nil || !strings.HasSuffix(verr.Hint, want) {
			t.Errorf("over %q: %v, want a hint ending %q", s, verr, want)
		}
	}
}

// A value from the config is overridden for one run by giving the input on
// the call — here a number written in quotes, which Resolve cannot hold
// inside its range the way it holds one that is merely too large — and the
// hint names it as the call's own surface gives it: a flag, a positional
// argument's usage slot, or a box in a form.
func TestTheOverrideForAConfiguredValueIsNamedForItsSurface(t *testing.T) {
	c := Capability{ID: "pg.status", Summary: "s", Safety: Read, Inputs: []Field{
		{Name: "limit", Type: Int, Min: 1, Max: 100, Config: "limit"},
		{Name: "table", Type: String, Options: []string{"a", "b"}, Positional: true, Config: "table"},
	}}
	for _, tc := range []struct {
		key  string
		v    any
		s    Surface
		want string
	}{
		{"limit", "3", SurfaceCLI, "or give --limit on the call to override it for one run"},
		{"limit", "3", SurfaceTUI, "or fill in the limit box to override it for one run"},
		{"table", "c", SurfaceCLI, "or give <table> on the call to override it for one run"},
	} {
		req := ResolveRequest(c, Inputs{Config: map[string]any{tc.key: tc.v}}, false, false).WithSurface(tc.s)
		verr := CheckInputs(c, req)
		if verr == nil || !strings.HasSuffix(verr.Hint, tc.want) {
			t.Errorf("%s over %q: %v, want a hint ending %q", tc.key, tc.s, verr, tc.want)
		}
	}
}
