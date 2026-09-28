package stdio

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/shutdown"
)

// chunks is a terminal as a read sees it: each read returns the next chunk,
// and once they are gone a read returns nothing, as a terminal set to wait a
// moment for a byte does when nothing comes.
type chunks struct {
	parts [][]byte
	reads int
}

func (c *chunks) Read(b []byte) (int, error) {
	c.reads++
	if len(c.parts) == 0 {
		return 0, nil
	}
	n := copy(b, c.parts[0])
	if c.parts[0] = c.parts[0][n:]; len(c.parts[0]) == 0 {
		c.parts = c.parts[1:]
	}
	return n, nil
}

func split(s string, size int) [][]byte {
	var out [][]byte
	for len(s) > size {
		out = append(out, []byte(s[:size]))
		s = s[size:]
	}
	return append(out, []byte(s))
}

// The keys a terminal's settings name edit the line as its own discipline
// did — the last character, the whole line, the last word — and the line
// ends at the first line ending, whatever arrived after it in the same read
// handed back rather than taken for the line.
func TestALineIsEditedAsTheTerminalWouldHaveEditedIt(t *testing.T) {
	keys := lineKeys{erase: 0x7f, kill: 0x15, wordErase: 0x17, eof: 0x04, utf8: true}
	for in, want := range map[string]string{
		"s3cret\n":                  "s3cret",
		"s3cX\x7fret\n":             "s3cret",
		"s3cX\bret\n":               "s3cret",
		"junk\x15s3cret\n":          "s3cret",
		"one two\x17s3cret\n":       "one s3cret",
		"one two  \x17\x17s3cret\n": "s3cret",
		"s3c\x04ret\n":              "s3cret",
		"\x7f\x7fs3cret\r":          "s3cret",
		"caf\xc3\xa9\x7fe\n":        "cafe",
		"keeps\x01control\n":        "keeps\x01control",
	} {
		line, rest, err := keys.readLine(&chunks{parts: split(in, 3)})
		if err != nil || string(line) != want || len(rest) != 0 {
			t.Errorf("readLine(%q) = %q, rest %q, %v; want %q", in, line, rest, err, want)
		}
	}
	// Where the terminal does not read UTF-8, erasing takes a byte, as its
	// discipline's does.
	bytewise := lineKeys{erase: 0x7f}
	if line, _, _ := bytewise.readLine(&chunks{parts: [][]byte{[]byte("caf\xc3\xa9\x7f\x7fe\n")}}); string(line) != "cafe" {
		t.Errorf("a byte-wise erase read %q, want cafe", line)
	}
	// A key the terminal has not set is a character like any other.
	if line, _, _ := (lineKeys{}).readLine(&chunks{parts: [][]byte{[]byte("a\x15b\n")}}); string(line) != "a\x15b" {
		t.Errorf("an unset kill key read %q", line)
	}
	line, rest, err := keys.readLine(&chunks{parts: [][]byte{[]byte("first\nsecond\nthird")}})
	if err != nil || string(line) != "first" || string(rest) != "second\nthird" {
		t.Errorf("a paste read as %q, rest %q, %v", line, rest, err)
	}
}

// A line of any length is read whole: the terminal's discipline held one to
// 1024 bytes on macOS, where a longer one never ended.
func TestALineLongerThanTheTerminalsOwnIsReadWhole(t *testing.T) {
	long := strings.Repeat("A", 70000)
	line, _, err := lineKeys{}.readLine(&chunks{parts: split(long+"\n", 1000)})
	if err != nil || string(line) != long {
		t.Errorf("a %d-byte line read as %d bytes, %v", len(long), len(line), err)
	}
}

// The end-of-input key on an empty line ends the prompt with nothing, as the
// discipline answered it; so does a terminal that hangs up.
func TestTheEndOfInputOnAnEmptyLineIsNothingTyped(t *testing.T) {
	keys := lineKeys{eof: 0x04}
	if line, _, err := keys.readLine(&chunks{parts: [][]byte{{0x04}}}); !errors.Is(err, io.EOF) || len(line) != 0 {
		t.Errorf("^D on an empty line = %q, %v; want io.EOF", line, err)
	}
	if _, _, err := keys.readLine(&chunks{}); !errors.Is(err, io.EOF) {
		t.Errorf("a hung-up terminal = %v, want io.EOF", err)
	}
	if line, _, err := keys.readLine(strings.NewReader("half")); err != nil || string(line) != "half" {
		t.Errorf("a line the input ended inside = %q, %v", line, err)
	}
}

// What is still arriving after the line is read until nothing does, and it
// is more than the line when any of it is more than blank: the rest of a
// paste is, a copied line's own newline or a carriage return and line feed
// is not.
func TestDrainReadsTheRestOfAPasteAndSaysWhetherItHeldAnything(t *testing.T) {
	paste := strings.Repeat("certificate-authority-data: QUJD\n", 2000)
	c := &chunks{parts: split(paste, 1024)}
	if !drain(c) {
		t.Error("the rest of a paste was not counted as more")
	}
	if len(c.parts) != 0 {
		t.Errorf("%d chunks were left for the shell", len(c.parts))
	}
	for _, rest := range []string{"", "\n", "\r\n", "  \n\t"} {
		if drain(&chunks{parts: [][]byte{[]byte(rest)}}) {
			t.Errorf("drain(%q) counted a blank rest as more", rest)
		}
	}
}

// The prompt is marked open while it waits, reads through the platform's
// reader, and says what that reader found after the line.
func TestASecretLineIsReadWithThePromptOpen(t *testing.T) {
	orig := readSecretLine
	t.Cleanup(func() { readSecretLine = orig })
	var openWhileRead bool
	readSecretLine = func(int) ([]byte, bool, error) {
		openWhileRead = shutdown.PromptOpen()
		return []byte("typed"), true, nil
	}
	line, more, err := ReadSecretLine("Value: ")
	if err != nil || !bytes.Equal(line, []byte("typed")) || !more {
		t.Fatalf("ReadSecretLine = %q, %v, %v", line, more, err)
	}
	if !openWhileRead {
		t.Error("the prompt was not open while it waited")
	}
	if shutdown.PromptOpen() {
		t.Error("the prompt was still open once answered")
	}
}

// A secret's line is read one byte at a time, so nothing after its line
// ending is taken off the terminal: what was typed ahead of the prompt after
// it stays there for that prompt.
func TestASecretsLineLeavesWhatFollowsIt(t *testing.T) {
	c := &chunks{parts: [][]byte{[]byte("first\nsecond\n")}}
	line, rest, err := lineKeys{}.readLine(byteReader{c})
	if err != nil || string(line) != "first" || len(rest) != 0 {
		t.Fatalf("readLine = %q, rest %q, %v", line, rest, err)
	}
	if len(c.parts) != 1 || string(c.parts[0]) != "second\n" {
		t.Errorf("the line took what followed it off the terminal, leaving %q", c.parts)
	}
}

// ^D on an empty line, or a terminal that hung up, is nothing typed to both
// prompts rather than an io.EOF each caller would pass on in the reader's
// words; any other failure, and a line the input ended inside, is as it was.
func TestTheEndOfInputOnAnEmptyLineIsNoAnswer(t *testing.T) {
	origLine, origSecret := readSecretLine, readPassword
	t.Cleanup(func() { readSecretLine, readPassword = origLine, origSecret })
	failed := errors.New("input/output error")
	for _, c := range []struct {
		line     string
		err      error
		wantLine string
		wantErr  error
	}{
		{"", io.EOF, "", nil},
		{"half", io.EOF, "half", io.EOF},
		{"", failed, "", failed},
		{"typed", nil, "typed", nil},
	} {
		readSecretLine = func(int) ([]byte, bool, error) { return []byte(c.line), false, c.err }
		readPassword = func(int) ([]byte, error) { return []byte(c.line), c.err }
		line, _, err := ReadSecretLine("Value: ")
		if string(line) != c.wantLine || !errors.Is(err, c.wantErr) || (c.wantErr == nil) != (err == nil) {
			t.Errorf("ReadSecretLine over %q, %v = %q, %v", c.line, c.err, line, err)
		}
		secret, err := ReadSecret("Passphrase: ")
		if string(secret) != c.wantLine || !errors.Is(err, c.wantErr) || (c.wantErr == nil) != (err == nil) {
			t.Errorf("ReadSecret over %q, %v = %q, %v", c.line, c.err, secret, err)
		}
	}
}
