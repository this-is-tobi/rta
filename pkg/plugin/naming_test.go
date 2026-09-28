package plugin

import (
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/pflag"
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

// A positional input is a slot on the CLI's command line, never a flag, and
// an input like any other everywhere else.
func TestAPositionalInputIsNamedByItsSlotOnTheCLI(t *testing.T) {
	for s, want := range map[Surface]string{
		SurfaceCLI:     "<hostname>",
		SurfaceUnknown: "<hostname>",
		SurfaceMCP:     `the "hostname" argument`,
		SurfaceTUI:     "the hostname box",
	} {
		if got := s.ArgumentName("hostname"); got != want {
			t.Errorf("ArgumentName over %q = %q, want %q", s, got, want)
		}
	}
}

// A call is one command line on the CLI, and a capability with its inputs
// named everywhere else.
func TestACallIsNamedTheWayItsSurfaceMakesIt(t *testing.T) {
	for s, want := range map[Surface]string{
		SurfaceCLI:     "`rta note edit --title --body`",
		SurfaceUnknown: "`rta note edit --title --body`",
		SurfaceMCP:     `the ` + "`note_edit`" + ` tool with the "title" argument and the "body" argument`,
		SurfaceTUI:     "`note.edit` with the title box and the body box",
	} {
		if got := s.CapabilityWith("note.edit", "title", "body"); got != want {
			t.Errorf("CapabilityWith over %q = %q, want %q", s, got, want)
		}
	}
	if got := SurfaceMCP.CapabilityWith("note.list"); got != SurfaceMCP.CapabilityName("note.list") {
		t.Errorf("with no inputs, CapabilityWith = %q, want the capability's name", got)
	}
}

// A whole call, values and all, is a command line at a terminal, the tool
// and its arguments to an agent, and the capability with its boxes filled in
// the TUI — with a value a shell would split quoted on the command line.
func TestAWholeCallIsSpelledTheWayItsSurfaceMakesIt(t *testing.T) {
	args := []Arg{
		{Name: "key", Value: "db password", Positional: true},
		{Name: "revision", Value: 2},
		{Name: "force", Value: true},
	}
	for s, want := range map[Surface]string{
		SurfaceCLI:     `rta kv restore 'db password' --revision 2 --force`,
		SurfaceUnknown: `rta kv restore 'db password' --revision 2 --force`,
		SurfaceMCP:     `kv_restore {"force":true,"key":"db password","revision":2}`,
		SurfaceTUI:     `kv.restore key="db password" revision=2 force`,
	} {
		if got := s.Call("kv.restore", args...); got != want {
			t.Errorf("Call over %q = %s, want %s", s, got, want)
		}
	}
	if got := SurfaceMCP.Call("kv.list"); got != "kv_list {}" {
		t.Errorf("a call with no arguments over MCP = %s", got)
	}
	// A value is whatever somebody stored — an agent's kv key among them —
	// so the command line a person pastes runs none of it, and the tool's
	// arguments carry it as it is.
	stored := Arg{Name: "key", Value: "a$(touch pwned)`id`<b>&c", Positional: true}
	if got, want := SurfaceCLI.Call("kv.get", stored), `rta kv get 'a$(touch pwned)`+"`id`"+`<b>&c'`; got != want {
		t.Errorf("a stored value on the CLI = %s, want %s", got, want)
	}
	if got, want := SurfaceMCP.Call("kv.get", stored), `kv_get {"key":"a$(touch pwned)`+"`id`"+`<b>&c"}`; got != want {
		t.Errorf("a stored value over MCP = %s, want %s", got, want)
	}
	// A TUI box is not a shell: a value goes in as it is typed.
	if got, want := SurfaceTUI.Call("kv.init", Arg{Name: "identity", Value: "~/.ssh/id_$USER"}),
		"kv.init identity=~/.ssh/id_$USER"; got != want {
		t.Errorf("a value in the TUI = %s, want %s", got, want)
	}
	if got, want := SurfaceCLI.Call("note.add", Arg{Name: "title", Value: "", Positional: true}), `rta note add ''`; got != want {
		t.Errorf("an empty value on the CLI = %s, want %s", got, want)
	}
	// A placeholder stands for what the reader types, bare as a usage line
	// spells it; anything else in angle brackets is a value like any other.
	got := SurfaceCLI.Call("kv.get", Arg{Name: "key", Value: "<key>", Positional: true},
		Arg{Name: "out", Value: "<file>"}, Arg{Name: "note", Value: "<a b>"})
	if want := `rta kv get <key> --out <file> --note '<a b>'`; got != want {
		t.Errorf("placeholders on the CLI = %s, want %s", got, want)
	}
	if got, want := SurfaceMCP.Call("kv.get", Arg{Name: "key", Value: "<key>"}), `kv_get {"key":"<key>"}`; got != want {
		t.Errorf("a placeholder over MCP = %s, want %s", got, want)
	}
}

// A value in a TUI box holding a character a reader does not see as itself
// is quoted with the character named by its code point, as textclean.Record
// shows the same record, by the rule the shell's spelling reads (glyph.Seen).
// Asked of unicode.IsPrint, a Hangul filler, a Braille blank and a
// variation selector each counted printable, and strconv.Quote left them
// raw: `kv.get key=prod/db` and a filler read as the call on the bare record.
func TestABoxValueSpellsACharacterAReaderDoesNotSeeByItsCodePoint(t *testing.T) {
	for _, r := range []rune{0x3164, 0x115f, 0xffa0, 0x2800, 0x1d159, 0x16fe4, 0xfe0f, 0xe0100, 0x034f, 0xad, 0xa0, 0x202e} {
		got := SurfaceTUI.Call("kv.get", Arg{Name: "key", Value: "prod/db" + string(r)})
		want := `kv.get key="prod/db` + strings.Trim(strconv.QuoteRuneToASCII(r), "'") + `"`
		if got != want {
			t.Errorf("a value ending in U+%04X in the TUI = %s, want %s", r, got, want)
		}
	}
	// A byte that is not UTF-8 is named as a byte, and a quote or a backslash
	// in a quoted value is escaped, so the spelling reads back as the value.
	bad := SurfaceTUI.Call("kv.get", Arg{Name: "key", Value: "a b\xff\"\\"})
	if want := `kv.get key="a b\xff\"\\"`; bad != want {
		t.Errorf("a value with a byte that is not UTF-8 in the TUI = %s, want %s", bad, want)
	}
	if v, err := strconv.Unquote(strings.TrimPrefix(bad, "kv.get key=")); err != nil || v != "a b\xff\"\\" {
		t.Errorf("the quoted value reads back as %q (%v)", v, err)
	}
	// A character a reader sees as itself stays as it is, however far from
	// ASCII: an accented letter, an ideograph, an emoji.
	for _, s := range []string{"café", "東京", string(rune(0x1f600))} {
		if got, want := SurfaceTUI.Call("kv.get", Arg{Name: "key", Value: s}), "kv.get key="+s; got != want {
			t.Errorf("a plain value in the TUI = %s, want %s", got, want)
		}
	}
}

// A switch turned off is joined to its flag on the command line. pflag gives
// a Bool flag its value only after an equals sign, and reads the next word as
// an argument of the command's own: `--tls false` turned the switch on and
// handed the command a stray "false" — the opposite call to the one spelled.
func TestASwitchTurnedOffIsSpelledAsTheCommandLineReadsIt(t *testing.T) {
	args := []Arg{{Name: "key", Value: "k", Positional: true}, {Name: "tls", Value: false}, {Name: "force", Value: true}}
	for s, want := range map[Surface]string{
		SurfaceCLI:     `rta kv restore k --tls=false --force`,
		SurfaceUnknown: `rta kv restore k --tls=false --force`,
		SurfaceMCP:     `kv_restore {"force":true,"key":"k","tls":false}`,
		SurfaceTUI:     `kv.restore key=k tls=false force`,
	} {
		if got := s.Call("kv.restore", args...); got != want {
			t.Errorf("Call over %q = %s, want %s", s, got, want)
		}
	}

	flags := pflag.NewFlagSet("restore", pflag.ContinueOnError)
	tls := flags.Bool("tls", true, "")
	force := flags.Bool("force", false, "")
	words := strings.Fields(SurfaceCLI.Call("kv.restore", args...))
	if err := flags.Parse(words[3:]); err != nil {
		t.Fatal(err)
	}
	if *tls || !*force || flags.NArg() != 1 || flags.Arg(0) != "k" {
		t.Errorf("the spelled call reads back as tls=%v force=%v arguments %q, want false, true and [k]",
			*tls, *force, flags.Args())
	}
}

// Leaving inputs out is said the way the caller does it: a TUI form keeps its
// boxes, so there they are left empty rather than left off.
func TestInputsLeftOutAreSaidTheWayTheSurfaceLeavesThemOut(t *testing.T) {
	for s, want := range map[Surface]string{
		SurfaceCLI: "without --key and --secret-file",
		SurfaceMCP: `without the "key" argument and the "secret-file" argument`,
		SurfaceTUI: "with the key box and the secret-file box left empty",
	} {
		if got := s.WithoutInputs("key", "secret-file"); got != want {
			t.Errorf("WithoutInputs over %q = %q, want %q", s, got, want)
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
