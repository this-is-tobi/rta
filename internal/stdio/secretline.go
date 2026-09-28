package stdio

import (
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/shutdown"
)

// ReadSecretLine asks the person at the terminal for a secret that fits on
// one line, as ReadSecret does, and reports whether more arrived with it: the
// rest of a paste that spanned lines, read off the terminal and dropped.
//
// A prompt that reads one line and leaves the rest where it was hands a
// pasted kubeconfig's second line onwards to whatever reads the terminal
// next — the passphrase prompt after it, or the shell, which runs each line
// and keeps it in its history — and once echo is back on, the terminal
// shows whatever of the paste had not yet arrived. So after the line,
// whatever is still arriving is read until the terminal has been quiet for
// a moment, echo still off, and more is true when any of it is more than
// blank: a line ending typed as a carriage return and a line feed, or a
// copied line's own newline, is no second line.
//
// The line is read by rta rather than by the terminal's line discipline,
// erase and kill keys included, since the discipline holds a line to a
// fixed length — 1024 bytes on macOS, where a longer one never ends — and
// ^D on an empty line is nothing typed, as it is to ReadSecret. Where rta
// cannot set the terminal's mode itself (not macOS or Linux) the line is
// read as ReadSecret reads it and more is always false.
func ReadSecretLine(prompt string) (line []byte, more bool, err error) {
	defer shutdown.Prompting()()
	fmt.Fprint(os.Stderr, prompt)
	line, more, err = readSecretLine(int(Real().Fd()))
	fmt.Fprintln(os.Stderr)
	line, err = noAnswer(line, err)
	return line, more, err
}

// readSecretLine is the platform's line reader, a var so a test can answer
// the prompt without a terminal.
var readSecretLine = readTerminalLine

// noAnswer is what a prompt returns for what its reader read: an end of
// input on an empty line — ^D, or a terminal that hung up — is nothing
// typed, the empty answer every caller already refuses in its own words,
// rather than an io.EOF a caller would pass on as "reading the passphrase:
// EOF".
func noAnswer(line []byte, err error) ([]byte, error) {
	if len(line) == 0 && errors.Is(err, io.EOF) {
		return nil, nil
	}
	return line, err
}

// byteReader reads at most one byte a call, so a line read through it ends
// with nothing after its line ending taken off the terminal.
type byteReader struct{ io.Reader }

func (r byteReader) Read(b []byte) (int, error) { return r.Reader.Read(b[:min(len(b), 1)]) }

// lineKeys are the characters that edit a line being typed, read off the
// terminal's own settings so each does what the terminal's discipline did
// with it: erase the last character, kill the line, erase the last word, and
// end the input. Zero is a key that is not set. utf8 is the terminal's IUTF8,
// under which erasing takes a whole character rather than its last byte.
type lineKeys struct {
	erase, kill, wordErase, eof byte
	utf8                        bool
}

// readLine reads from r up to the first line ending, applying the keys as
// they come, and returns the line and whatever came after the ending in the
// same read. An end of input on an empty line is io.EOF, as the terminal's
// own discipline answers one; typed after a character it ends nothing, as
// there.
func (k lineKeys) readLine(r io.Reader) (line, rest []byte, err error) {
	buf := make([]byte, 4096)
	defer clear(buf)
	for {
		n, err := r.Read(buf)
		for i := 0; i < n; i++ {
			switch c := buf[i]; {
			case c == '\n' || c == '\r':
				return line, append([]byte(nil), buf[i+1:n]...), nil
			case k.set(k.eof, c):
				if len(line) == 0 {
					return nil, nil, io.EOF
				}
			case k.set(k.erase, c) || c == '\b':
				line = k.eraseChar(line)
			case k.set(k.kill, c):
				line = line[:0]
			case k.set(k.wordErase, c):
				line = eraseWord(line)
			default:
				line = append(line, c)
			}
		}
		switch {
		case errors.Is(err, io.EOF) && len(line) > 0:
			return line, nil, nil
		case err != nil:
			return line, nil, err
		case n == 0:
			// A read that waits for a byte and returns none: the terminal
			// hung up.
			return line, nil, io.EOF
		}
	}
}

// eraseChar drops the last character of line, or its last byte where the
// terminal does not read UTF-8.
func (k lineKeys) eraseChar(line []byte) []byte {
	if len(line) == 0 {
		return line
	}
	size := 1
	if k.utf8 {
		_, size = utf8.DecodeLastRune(line)
	}
	return line[:len(line)-size]
}

// set reports whether c is key, and key is one the terminal has set.
func (lineKeys) set(key, c byte) bool { return key != 0 && c == key }

// eraseWord drops the last word of line and the blanks after it, as the
// terminal's word-erase key does.
func eraseWord(line []byte) []byte {
	i := len(line)
	for i > 0 && (line[i-1] == ' ' || line[i-1] == '\t') {
		i--
	}
	for i > 0 && line[i-1] != ' ' && line[i-1] != '\t' {
		i--
	}
	return line[:i]
}

// drain reads r until a read returns nothing — r is a terminal set to wait a
// moment for a byte and give up — and reports whether any of what it read
// is more than blank. Nothing read is kept.
func drain(r io.Reader) bool {
	buf := make([]byte, 32<<10)
	defer clear(buf)
	more := false
	for {
		n, err := r.Read(buf)
		more = more || !blank(buf[:max(n, 0)])
		if n <= 0 || err != nil {
			return more
		}
	}
}

// blank reports whether b holds nothing but line endings, spaces and tabs.
func blank(b []byte) bool {
	for _, c := range b {
		if c != '\n' && c != '\r' && c != ' ' && c != '\t' {
			return false
		}
	}
	return true
}
