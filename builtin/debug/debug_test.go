package debug

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/builtin/internal/pipein"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func req(values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, false)
}

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatal(err)
	}
}

// --- runAnsi: argument, stdin, and the "neither" error ---

func TestRunAnsiUsesTheArgumentWhenGiven(t *testing.T) {
	v, err := runAnsi(context.Background(), req(map[string]any{"input": "hi"}))
	if err != nil {
		t.Fatal(err)
	}
	table := v.(view.Table)
	if table.Total != 1 || table.Rows[0][0] != "hi" {
		t.Errorf("got %+v", table)
	}
}

// A non-CLI request must never block on stdin — pipein.Read has to
// short-circuit before it ever touches stdio.Real()/term.IsTerminal, which
// this proves indirectly: an MCP request with no argument reaches the
// "nothing to explain" error rather than hanging.
func TestRunAnsiOnANonCliSurfaceWithNoArgumentErrorsRatherThanReadingStdin(t *testing.T) {
	r := req(map[string]any{}).WithSurface(plugin.SurfaceMCP)
	_, err := runAnsi(context.Background(), r)
	verr := view.AsError(err, "debug.test")
	if verr.Code != "debug.ansi.noinput" {
		t.Errorf("code = %q, want debug.ansi.noinput", verr.Code)
	}
}

// A read failure and an oversized pipe are both this capability's to word:
// pipein reports what happened, and only debug.ansi knows what the pipe was
// supposed to hold.
func TestStdinErrorWordsBothFailures(t *testing.T) {
	if verr := stdinError(errors.New("boom")); verr == nil || verr.Code != "debug.ansi.stdin" ||
		!strings.Contains(verr.Message, "boom") {
		t.Errorf("read failure = %v, want code debug.ansi.stdin naming the cause", verr)
	}
	verr := stdinError(pipein.ErrTooLarge)
	if verr == nil || verr.Code != "debug.ansi.stdin" || !strings.Contains(verr.Message, "1.0 MiB") {
		t.Errorf("oversized pipe = %v, want the bound named", verr)
	}
	if stdinError(nil) != nil {
		t.Error("no error became one")
	}
}

// --- explainAnsi: the walk, text accumulation, and row shape ---

func TestExplainAnsiOnPlainTextIsOneRow(t *testing.T) {
	table := explainAnsi("hello, world")
	if table.Total != 1 {
		t.Fatalf("got %d rows, want 1 (table=%+v)", table.Total, table)
	}
	if table.Rows[0][0] != "hello, world" || table.Rows[0][1] != "text" {
		t.Errorf("got %v", table.Rows[0])
	}
}

func TestExplainAnsiOnEmptyInputIsZeroRows(t *testing.T) {
	if table := explainAnsi(""); table.Total != 0 {
		t.Errorf("got %d rows, want 0", table.Total)
	}
}

// Text interrupted by one sequence has to stay three rows — text, sequence,
// text — not one row per character before and after it.
func TestExplainAnsiSplitsTextAroundASequence(t *testing.T) {
	table := explainAnsi("before\x1b[31mafter")
	if table.Total != 3 {
		t.Fatalf("got %d rows, want 3: %+v", table.Total, table.Rows)
	}
	if table.Rows[0][0] != "before" || table.Rows[0][1] != "text" {
		t.Errorf("row 0 = %v", table.Rows[0])
	}
	if table.Rows[1][1] != "CSI" {
		t.Errorf("row 1 kind = %q, want CSI", table.Rows[1][1])
	}
	if table.Rows[2][0] != "after" || table.Rows[2][1] != "text" {
		t.Errorf("row 2 = %v", table.Rows[2])
	}
}

// --- SGR ---

func sgrMeaning(t *testing.T, seq string) string {
	t.Helper()
	table := explainAnsi(seq)
	if table.Total != 1 {
		t.Fatalf("%q produced %d rows, want 1: %+v", seq, table.Total, table.Rows)
	}
	if kind := table.Rows[0][1]; kind != "CSI" {
		t.Fatalf("%q classified as %q, want CSI", seq, kind)
	}
	return table.Rows[0][2]
}

func TestSgrNoParamsMeansReset(t *testing.T) {
	if got := sgrMeaning(t, "\x1b[m"); got != "reset" {
		t.Errorf("got %q", got)
	}
}

func TestSgrSingleColor(t *testing.T) {
	if got := sgrMeaning(t, "\x1b[31m"); got != "red foreground" {
		t.Errorf("got %q", got)
	}
}

func TestSgrCombinesEveryParamInOrder(t *testing.T) {
	got := sgrMeaning(t, "\x1b[1;31;4m")
	want := "bold, red foreground, underline"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSgrUnknownCodeFallsBackRatherThanDroppingIt(t *testing.T) {
	if got := sgrMeaning(t, "\x1b[99m"); got != "SGR 99" {
		t.Errorf("got %q", got)
	}
}

func TestSgr256ColorForeground(t *testing.T) {
	got := sgrMeaning(t, "\x1b[38;5;196m")
	if !strings.Contains(got, "foreground") || !strings.Contains(got, "256-color 196") {
		t.Errorf("got %q", got)
	}
}

func TestSgrTruecolorBackground(t *testing.T) {
	got := sgrMeaning(t, "\x1b[48;2;10;20;30m")
	if !strings.Contains(got, "background") || !strings.Contains(got, "rgb 10,20,30") {
		t.Errorf("got %q", got)
	}
}

// A 38 with nothing usable after it (truncated, or a bare "38" on its own)
// must not panic indexing params[i+1]/params[i+2] and must not be silently
// swallowed — it falls through to the plain "SGR 38" line instead.
func TestSgrExtendedColorWithNoSelectorDoesNotPanic(t *testing.T) {
	if got := sgrMeaning(t, "\x1b[38m"); got != "SGR 38" {
		t.Errorf("got %q", got)
	}
}

// "38;2;10" is truecolor's own introducer with only one of the three RGB
// components present — not enough to consume, so it falls through to three
// independent codes. The middle one reads as "faint" rather than "SGR 2"
// because 2 genuinely means that on its own (AttrFaint) — the same digit is
// both a plain SGR code and the truecolor selector, and which one applies
// depends entirely on how much follows it.
func TestSgrExtendedColorMissingItsComponentsDoesNotPanic(t *testing.T) {
	if got := sgrMeaning(t, "\x1b[38;2;10m"); got != "SGR 38, faint, SGR 10" {
		t.Errorf("got %q", got)
	}
}

// The pooled parser holds 32 parameters and indexes past them when a 33rd
// arrives: a CSI or DCS carrying more took the CLI down, answered MCP with a
// panic, and crashed the TUI on every launch once a tile held one. Captured
// terminal output is the input this capability exists to read, and nothing
// stops it carrying forty parameters, in either introducer, with colons.
func TestASequenceWithManyParametersIsExplained(t *testing.T) {
	forty := strings.Repeat("9;", 40) + "9"
	for _, input := range []string{
		"\x1b[" + forty + "m",
		"\x9b" + forty + "m",
		"\x1b[" + strings.Repeat("9:", 40) + "9m",
		"\x1bP" + forty + "q\x1b\\",
		"\x1b [" + forty + "m", // an intermediate first, and the decoder still reads a CSI
		"\x1b[" + forty,        // and cut off, still in its parameters
	} {
		if table := explainAnsi(input); table.Total == 0 {
			t.Errorf("%q: no rows", input)
		}
	}
	// Every parameter is read, the 32nd included, which the pooled parser's
	// last slot dropped even where it did not panic.
	got := sgrMeaning(t, "\x1b["+strings.Repeat("9;", 31)+"9m")
	if n := strings.Count(got, "strikethrough"); n != 32 {
		t.Errorf("32 parameters gave %d meanings: %q", n, got)
	}
	got = sgrMeaning(t, "\x1b["+forty+"m")
	if n := strings.Count(got, "strikethrough"); n != 41 || !strings.Contains(got, "41 parameters") {
		t.Errorf("41 parameters = %q, want each named and the count said", got)
	}
}

// --- cursor movement, position, erase ---

func TestCursorMovementDefaultsToOne(t *testing.T) {
	table := explainAnsi("\x1b[A")
	if got := table.Rows[0][2]; got != "cursor up 1" {
		t.Errorf("got %q", got)
	}
}

func TestCursorMovementUsesTheGivenCount(t *testing.T) {
	table := explainAnsi("\x1b[5B")
	if got := table.Rows[0][2]; got != "cursor down 5" {
		t.Errorf("got %q", got)
	}
}

func TestCursorPositionNamesRowAndColumn(t *testing.T) {
	table := explainAnsi("\x1b[3;7H")
	if got := table.Rows[0][2]; got != "cursor position: row 3, column 7" {
		t.Errorf("got %q", got)
	}
}

func TestEraseEntireScreen(t *testing.T) {
	table := explainAnsi("\x1b[2J")
	if got := table.Rows[0][2]; got != "erase entire screen" {
		t.Errorf("got %q", got)
	}
}

// --- OSC: title, hyperlink, clipboard ---

func TestOscTitle(t *testing.T) {
	table := explainAnsi("\x1b]0;my title\x07")
	if got := table.Rows[0][2]; got != "set window/icon title: my title" {
		t.Errorf("got %q", got)
	}
}

func TestOscHyperlinkNamesTheUri(t *testing.T) {
	table := explainAnsi("\x1b]8;;https://example.com\x07")
	if got := table.Rows[0][2]; got != "hyperlink: https://example.com" {
		t.Errorf("got %q", got)
	}
}

// The one the hazard is about: decode it, don't just
// say "clipboard write" and leave the payload as base64 nobody reads twice.
func TestOscClipboardWriteIsDecoded(t *testing.T) {
	// base64("hello") == "aGVsbG8="
	table := explainAnsi("\x1b]52;c;aGVsbG8=\x07")
	got := table.Rows[0][2]
	if !strings.Contains(got, `"hello"`) || !strings.Contains(got, "WRITE") {
		t.Errorf("got %q", got)
	}
}

func TestOscClipboardReadRequestIsNotReportedAsAWrite(t *testing.T) {
	table := explainAnsi("\x1b]52;c;?\x07")
	got := table.Rows[0][2]
	if !strings.Contains(got, "READ") || strings.Contains(got, "WRITE") {
		t.Errorf("got %q", got)
	}
}

func TestOscClipboardInvalidBase64DoesNotCrash(t *testing.T) {
	table := explainAnsi("\x1b]52;c;not-base64!!\x07")
	if !strings.Contains(table.Rows[0][2], "not valid base64") {
		t.Errorf("got %q", table.Rows[0][2])
	}
}

// --- control characters ---

func TestBareBel(t *testing.T) {
	table := explainAnsi("\x07")
	if table.Rows[0][1] != "control" || table.Rows[0][2] != "bell" {
		t.Errorf("got %v", table.Rows[0])
	}
}

// --- visualize: the safety property ---

// Every dangerous byte class this package names — ESC, BEL, bare CR — must
// never appear literally in a row's Sequence column. This is the one test
// in the package that is really about correctness of a *security* property
// rather than a parsing one: the Sequence column is rendered straight into
// the CLI/TUI, and a raw ESC there is not a display bug.
func TestVisualizeNeverEmitsARawControlByte(t *testing.T) {
	inputs := []string{
		"\x1b[31mred\x1b[0m",
		"\x1b]52;c;aGVsbG8=\x07",
		"\x1b]0;title\x07",
		"a\rb",
		"\x1b",
	}
	for _, in := range inputs {
		table := explainAnsi(in)
		for _, row := range table.Rows {
			seq := row[0]
			if row[1] == "text" {
				continue // plain printable text is allowed to be itself
			}
			for i := 0; i < len(seq); i++ {
				if c := seq[i]; c < 0x20 || c == 0x7f {
					t.Errorf("input %q row %v: raw control byte 0x%02x in Sequence column", in, row, c)
				}
			}
		}
	}
}

func TestVisualizeRendersEscAndBelAsMnemonics(t *testing.T) {
	got := visualize("\x1b[31m")
	if !strings.HasPrefix(got, "ESC") {
		t.Errorf("got %q, want it to start with ESC", got)
	}
	got = visualize("\x07")
	if got != "BEL" {
		t.Errorf("got %q, want BEL", got)
	}
}
