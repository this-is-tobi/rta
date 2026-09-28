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
// ends at the first line ending.
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
		line, err := keys.readLine(&chunks{parts: split(in, 3)})
		if err != nil || string(line) != want {
			t.Errorf("readLine(%q) = %q, %v; want %q", in, line, err, want)
		}
	}
	// Where the terminal does not read UTF-8, erasing takes a byte, as its
	// discipline's does.
	bytewise := lineKeys{erase: 0x7f}
	if line, _ := bytewise.readLine(&chunks{parts: [][]byte{[]byte("caf\xc3\xa9\x7f\x7fe\n")}}); string(line) != "cafe" {
		t.Errorf("a byte-wise erase read %q, want cafe", line)
	}
	// A key the terminal has not set is a character like any other.
	if line, _ := (lineKeys{}).readLine(&chunks{parts: [][]byte{[]byte("a\x15b\n")}}); string(line) != "a\x15b" {
		t.Errorf("an unset kill key read %q", line)
	}
	// Nothing after the line ending is read.
	c := &chunks{parts: [][]byte{[]byte("first\nsecond")}}
	if line, err := keys.readLine(c); err != nil || string(line) != "first" || len(c.parts) != 1 {
		t.Errorf("a paste read as %q, %v, leaving %q", line, err, c.parts)
	}
}

// A line of any length is read whole: the terminal's discipline held one to
// 1024 bytes on macOS, where a longer one never ended.
func TestALineLongerThanTheTerminalsOwnIsReadWhole(t *testing.T) {
	long := strings.Repeat("A", 70000)
	line, err := lineKeys{}.readLine(&chunks{parts: split(long+"\n", 1000)})
	if err != nil || string(line) != long {
		t.Errorf("a %d-byte line read as %d bytes, %v", len(long), len(line), err)
	}
}

// The end-of-input key on an empty line ends the prompt with nothing, as the
// discipline answered it; so does a terminal that hangs up.
func TestTheEndOfInputOnAnEmptyLineIsNothingTyped(t *testing.T) {
	keys := lineKeys{eof: 0x04}
	if line, err := keys.readLine(&chunks{parts: [][]byte{{0x04}}}); !errors.Is(err, io.EOF) || len(line) != 0 {
		t.Errorf("^D on an empty line = %q, %v; want io.EOF", line, err)
	}
	if _, err := keys.readLine(&chunks{}); !errors.Is(err, io.EOF) {
		t.Errorf("a hung-up terminal = %v, want io.EOF", err)
	}
	if line, err := keys.readLine(strings.NewReader("half")); err != nil || string(line) != "half" {
		t.Errorf("a line the input ended inside = %q, %v", line, err)
	}
}

// The prompt is marked open while it waits, and reads through the
// platform's reader.
func TestASecretLineIsReadWithThePromptOpen(t *testing.T) {
	orig := readSecretLine
	t.Cleanup(func() { readSecretLine = orig })
	var openWhileRead bool
	readSecretLine = func(int) ([]byte, error) {
		openWhileRead = shutdown.PromptOpen()
		return []byte("typed"), nil
	}
	line, err := ReadSecretLine("Value: ")
	if err != nil || !bytes.Equal(line, []byte("typed")) {
		t.Fatalf("ReadSecretLine = %q, %v", line, err)
	}
	if !openWhileRead {
		t.Error("the prompt was not open while it waited")
	}
	if shutdown.PromptOpen() {
		t.Error("the prompt was still open once answered")
	}
}
