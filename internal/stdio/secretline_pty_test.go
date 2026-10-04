package stdio

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// ptyTerminal is a pseudo-terminal a test types into, for the one part of
// this package the fakes elsewhere cannot reach: the line reader's own
// handling of a real terminal, echo and the line discipline switched off and
// back, and the terminal's own timer (VMIN, VTIME) ending the drain of a
// paste. CI's Linux runners compile that path and had never run it; it had
// run on macOS alone, by hand.
type ptyTerminal struct {
	t                *testing.T
	master, terminal int
	// reading is closed once the line reader read last started has returned.
	reading chan struct{}
}

// newTerminal opens one (openPTY), its line-editing keys and IUTF8 set, so
// what a key does is the test's choice rather than this host's default.
//
// **A test that fails while the reader still reads has to be able to stop
// it.** The master is closed first, which hangs the terminal up and ends a
// read the reader is blocked in, and the terminal once the reader has
// returned. Closed the other way round, the terminal's close on macOS waited
// for that read, which nothing was left to end: a reader that never switched
// echo off hung the suite until its timeout, the test's own message never
// printed. And a reader still running when the terminal was closed could
// have read, and set, the next terminal opened on the same descriptor. The
// master is non-blocking so that typing into a terminal nobody reads fails
// (write) rather than waits there instead.
func newTerminal(t *testing.T) *ptyTerminal {
	t.Helper()
	master, terminal, err := openPTY()
	if err != nil {
		t.Skipf("no pseudo-terminal on this host: %v", err)
	}
	p := &ptyTerminal{t: t, master: master, terminal: terminal, reading: make(chan struct{})}
	close(p.reading)
	t.Cleanup(func() {
		_ = unix.Close(master)
		select {
		case <-p.reading:
		case <-time.After(10 * time.Second):
		}
		_ = unix.Close(terminal)
	})
	if err := unix.SetNonblock(master, true); err != nil {
		t.Fatal(err)
	}
	settings, err := unix.IoctlGetTermios(terminal, ioctlGetTermios)
	if err != nil {
		t.Fatal(err)
	}
	settings.Lflag |= unix.ICANON | unix.ECHO
	settings.Iflag |= unix.IUTF8
	settings.Cc[unix.VERASE], settings.Cc[unix.VKILL] = 0x7f, 0x15
	settings.Cc[unix.VWERASE], settings.Cc[unix.VEOF] = 0x17, 0x04
	// And a read that waits for a byte, so the reader's drain (VMIN zero) is
	// a setting the terminal is in only while it drains.
	settings.Cc[unix.VMIN], settings.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(terminal, ioctlSetTermios, settings); err != nil {
		t.Fatal(err)
	}
	return p
}

// read runs the line reader on the terminal and types into it: typed once
// the reader has taken the terminal over, echo and the line discipline off,
// as nothing is typed at a prompt before it shows; then later, once the
// reader has turned to draining what is still arriving, as the rest of a
// paste arrives after its first line. It checks that nothing typed was
// echoed and that the terminal is put back as it was.
//
// The first wait is on the terminal's settings, not on a clock: a byte
// typed before the reader switches the discipline off is edited, and
// echoed, by the discipline, and the test would be of the terminal instead.
// The later pieces are typed from the reader's own goroutine, through
// readTerminal's draining, once the terminal is set to drain and before the
// drain reads it. Typed from here on seeing that setting, as they were,
// they had to arrive within drainWindow of the drain's first read: a test
// held back longer than that by a busy machine typed them into a terminal
// the reader had finished with, and the case read a paste cut short and
// then its second line as the next one typed. Small, so the terminal holds
// them while nothing reads it.
func (p *ptyTerminal) read(typed string, later ...string) (line []byte, more bool, err error) {
	p.t.Helper()
	if len(later) == 0 {
		return p.readWith(readTerminalLine, typed)
	}
	var typing error
	line, more, err = p.readWith(func(fd int) ([]byte, bool, error) {
		return readTerminal(fd, true, func() {
			for _, s := range later {
				if typing = p.typeIn(s); typing != nil {
					return
				}
			}
		})
	}, typed)
	if typing != nil {
		p.t.Errorf("typing the rest of the paste: %v", typing)
	}
	return line, more, err
}

// secret runs the passphrase prompt's reader, ReadSecret's, on the terminal
// and types into it, as read runs the line reader.
func (p *ptyTerminal) secret(typed string) ([]byte, error) {
	p.t.Helper()
	line, _, err := p.readWith(func(fd int) ([]byte, bool, error) {
		line, err := readTerminalSecret(fd)
		return line, false, err
	}, typed)
	return line, err
}

func (p *ptyTerminal) readWith(reader func(fd int) ([]byte, bool, error), typed string) (
	line []byte, more bool, err error,
) {
	p.t.Helper()
	before := p.settings()
	type result struct {
		line []byte
		more bool
		err  error
	}
	done, reading := make(chan result, 1), make(chan struct{})
	p.reading = reading
	go func() {
		defer close(reading)
		line, more, err := reader(p.terminal)
		done <- result{line, more, err}
	}()
	p.await("echo and the line discipline off", func(s *unix.Termios) bool {
		return s.Lflag&(unix.ICANON|unix.ECHO) == 0
	})
	p.write(typed)
	var r result
	select {
	case r = <-done:
	case <-time.After(10 * time.Second):
		p.t.Fatal("the line reader did not return")
	}
	if echoed := p.echoed(); echoed != "" {
		p.t.Errorf("the terminal echoed %q of what was typed", echoed)
	}
	// PENDIN aside, a state the kernel keeps rather than a setting: macOS
	// sets it as the line discipline comes back on, to retype what the
	// terminal holds into the line it edits.
	if after := p.settings(); after.Lflag&^unix.PENDIN != before.Lflag&^unix.PENDIN ||
		after.Iflag != before.Iflag || after.Cc != before.Cc {
		p.t.Errorf("the terminal was left as %+v, not put back as %+v", after, before)
	}
	return r.line, r.more, r.err
}

func (p *ptyTerminal) settings() *unix.Termios {
	p.t.Helper()
	s, err := unix.IoctlGetTermios(p.terminal, ioctlGetTermios)
	if err != nil {
		p.t.Fatal(err)
	}
	return s
}

// await waits for the terminal's settings to be as the reader sets them, or
// for the reader to have returned: one whose line was on the terminal before
// it started, typed ahead of it, can set them and put them back between two
// looks, and what it did to the terminal is then judged by what it read and
// echoed, as it is for any other.
func (p *ptyTerminal) await(what string, ready func(*unix.Termios) bool) {
	p.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !ready(p.settings()); {
		select {
		case <-p.reading:
			return
		default:
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("the line reader never set the terminal to %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// write types s, whole, as a terminal emulator writes a paste, as fast as
// the terminal takes it, and fails where it takes nothing more for as long as
// read waits for the reader: once nothing reads the terminal, or once what it
// echoes has filled the side nobody reads until the reader returns.
func (p *ptyTerminal) write(s string) {
	p.t.Helper()
	if err := p.typeIn(s); err != nil {
		p.t.Fatal(err)
	}
}

// typeIn is write's typing with its failure handed back rather than ended
// on, for the reader's own goroutine: only the goroutine running the test
// may end it.
func (p *ptyTerminal) typeIn(s string) error {
	deadline := time.Now().Add(10 * time.Second)
	for b := []byte(s); len(b) > 0; {
		n, err := unix.Write(p.master, b)
		switch {
		case errors.Is(err, unix.EAGAIN) && time.Now().Before(deadline):
			time.Sleep(time.Millisecond)
			continue
		case err != nil:
			return fmt.Errorf("typing, with %d of %d bytes still to go: %w", len(b), len(s), err)
		}
		b, deadline = b[n:], time.Now().Add(10*time.Second)
	}
	return nil
}

// echoed is what the terminal has written back to the emulator's side and
// the emulator has not read: with echo off, nothing.
func (p *ptyTerminal) echoed() string {
	p.t.Helper()
	buf := make([]byte, 64<<10)
	n, err := unix.Read(p.master, buf)
	if err != nil || n <= 0 {
		return ""
	}
	return string(buf[:n])
}

// A value typed at the prompt is read off a terminal with nothing echoed,
// whatever its length: 1500 bytes is past the 1024 bytes macOS's line
// discipline holds a line to, where a longer one never ends, and it ends
// here at its return. The terminal's keys edit the line as its discipline
// did, erase taking a whole character under IUTF8; ^D after a character ends
// nothing, and on an empty line is nothing typed, the empty answer
// ReadSecretLine returns for it.
func TestALineIsReadOffARealTerminal(t *testing.T) {
	long := strings.Repeat("k", 1500)
	for typed, want := range map[string]string{
		"s3cret\r":                             "s3cret",
		"s3cret\n":                             "s3cret",
		long + "\r":                            long,
		"s3cX\x7fret\r":                        "s3cret",
		"junk\x15s3cret\r":                     "s3cret",
		"one two\x17s3cret\r":                  "one s3cret",
		"caf" + string(rune(0xe9)) + "\x7fe\r": "cafe",
		"s3c\x04ret\r":                         "s3cret",
	} {
		line, more, err := newTerminal(t).read(typed)
		if err != nil || string(line) != want || more {
			t.Errorf("typed %.40q (%d bytes), read %.40q (%d bytes), more %v, %v; want %.40q", typed, len(typed),
				line, len(line), more, err, want)
		}
	}
	line, more, err := newTerminal(t).read("\x04")
	if !errors.Is(err, io.EOF) {
		t.Errorf("^D on an empty line read %q, %v; want the end of input", line, err)
	}
	if line, err = noAnswer(line, err); line != nil || more || err != nil {
		t.Errorf("^D on an empty line answered %q, more %v, %v; want nothing typed", line, more, err)
	}
}

// A paste spanning lines is read as its first line, and the rest of it is
// read off the terminal and reported as more, rather than left for whatever
// reads the terminal next: the prompt after it, or the shell, which would
// run each line. Written whole, with its later lines arriving after the
// first, and larger than a read of the terminal takes at once. A line ending
// typed as a carriage return and a line feed is no second line. And what is
// typed next is read as the next line, not as what was left of the paste.
func TestAPasteIsDrainedOffARealTerminalAndReportedAsMore(t *testing.T) {
	var kubeconfig strings.Builder
	kubeconfig.WriteString("apiVersion: v1\r")
	for i := range 400 {
		kubeconfig.WriteString("    certificate-authority-data: " + strings.Repeat(string(rune('a'+i%26)), 24) + "\r")
	}
	for what, c := range map[string]struct {
		typed, line string
		later       []string
		more        bool
	}{
		"written whole":        {typed: "first\rsecond\rthird\r", line: "first", more: true},
		"arriving after":       {typed: "first\r", later: []string{"second\r", "third\r"}, line: "first", more: true},
		"larger than one read": {typed: kubeconfig.String(), line: "apiVersion: v1", more: true},
		"ended by CR and LF":   {typed: "first\r\n", line: "first"},
	} {
		p := newTerminal(t)
		line, more, err := p.read(c.typed, c.later...)
		if err != nil || string(line) != c.line || more != c.more {
			t.Errorf("%s, read %.40q, more %v, %v; want %q, more %v", what, line, more, err, c.line, c.more)
		}
		if line, more, err := p.read("next\r"); err != nil || string(line) != "next" || more {
			t.Errorf("%s, the next line read %.40q, more %v, %v; want next, and nothing left of the paste",
				what, line, more, err)
		}
	}
}

// The passphrase prompt's reader, ReadSecret's, takes its line off a real
// terminal and nothing after it: a passphrase typed twice ahead of its two
// prompts is still on the terminal for the second, "Once more:", with
// nothing echoed, through the terminal put back to its line discipline and
// taken over again between them. Read as the line reader reads, whatever
// arrived with the first line would go with it, and the second prompt would
// wait for a line already typed.
func TestASecretIsReadOffARealTerminalLeavingWhatFollowsIt(t *testing.T) {
	p := newTerminal(t)
	if line, err := p.secret("pa55word\rpa55word\r"); err != nil || string(line) != "pa55word" {
		t.Fatalf("the first prompt read %q, %v; want pa55word", line, err)
	}
	if line, err := p.secret(""); err != nil || string(line) != "pa55word" {
		t.Errorf("the second prompt read %q, %v; want the pa55word typed ahead of it", line, err)
	}
}
