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
// one line, as ReadSecret does, with the line read by rta rather than by the
// terminal's line discipline, its erase and kill keys included.
//
// The discipline holds a line to a fixed length: 1024 bytes on macOS, where
// a longer one never ends — the prompt waits on a newline the terminal will
// not take, and a token or a base64 blob pasted at it hung kv set until ^C.
// And term.ReadPassword reads the end of input on an empty line as nothing
// and waits on, so ^D did not end the prompt either. Read here, a line is as
// long as what was typed, and ^D on an empty one is io.EOF. Where rta cannot
// set the terminal's mode itself (not macOS or Linux) the line is read as
// ReadSecret reads it.
func ReadSecretLine(prompt string) ([]byte, error) {
	defer shutdown.Prompting()()
	fmt.Fprint(os.Stderr, prompt)
	line, err := readSecretLine(int(Real().Fd()))
	fmt.Fprintln(os.Stderr)
	return line, err
}

// readSecretLine is the platform's line reader, a var so a test can answer
// the prompt without a terminal.
var readSecretLine = readTerminalLine

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
// they come. An end of input on an empty line is io.EOF, as the terminal's
// own discipline answers one; typed after a character it ends nothing, as
// there.
//
// One byte at a time, so nothing after the line ending is read: what follows
// it stays where the discipline left it, for whatever reads the terminal
// next.
func (k lineKeys) readLine(r io.Reader) ([]byte, error) {
	var line []byte
	var b [1]byte
	for {
		n, err := r.Read(b[:])
		if n == 1 {
			switch c := b[0]; {
			case c == '\n' || c == '\r':
				return line, nil
			case k.set(k.eof, c):
				if len(line) == 0 {
					return nil, io.EOF
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
			continue
		}
		switch {
		case errors.Is(err, io.EOF) && len(line) > 0:
			return line, nil
		case err != nil:
			return line, err
		default:
			// A read that waits for a byte and returns none: the terminal
			// hung up.
			return line, io.EOF
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
