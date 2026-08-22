package clipboard

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeClipboard puts a stand-in for every clipboard program this package
// knows about on PATH, and nothing else.
//
// A stub for Copy would prove only that the stub was called. What has to be
// true is a property of the process actually started — the value arrives on
// its stdin and appears nowhere in its argv — so the test runs a real
// program and reads back both.
func fakeClipboard(t *testing.T) (stdin, argv string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in is a shell script")
	}
	dir := t.TempDir()
	stdin = filepath.Join(dir, "stdin")
	argv = filepath.Join(dir, "argv")
	script := "#!/bin/sh\ncat > " + stdin + "\nprintf '%s\\n' \"$@\" > " + argv + "\n"
	for _, name := range []string{"pbcopy", "xclip", "xsel", "wl-copy", "clip", "clip.exe", "termux-clipboard-set"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Prepended, not substituted: the stand-in is a shell script, and a PATH
	// holding nothing but the stand-ins leaves it unable to find `cat` — at
	// which point the redirection still creates an empty file and the test
	// blames the code under it.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return stdin, argv
}

// The whole point of the package: the value ends up somewhere you can paste
// it from, and nowhere anybody can read it off the process table.
func TestCopyPutsTheValueOnTheClipboardAndNowhereElse(t *testing.T) {
	stdin, argv := fakeClipboard(t)

	ok, failed, _ := Copy([]byte("s3cr3t"))

	if !ok {
		t.Fatalf("Copy did not report success, failed = %v", failed)
	}
	got, err := os.ReadFile(stdin)
	if err != nil {
		t.Fatalf("nothing reached the clipboard: %v", err)
	}
	if string(got) != "s3cr3t" {
		t.Errorf("clipboard got %q, want the value byte for byte", got)
	}
	line, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(line), "s3cr3t") {
		t.Errorf("the value was passed as an argument, readable in ps: %q", line)
	}
}

// A value is copied whole. The package has no idea what the value is a
// value *of*, so it cannot assume where it is safe to stop — a private key
// truncated at its first newline is not a smaller secret but a broken one.
func TestCopyDoesNotStopAtTheFirstLine(t *testing.T) {
	stdin, _ := fakeClipboard(t)
	pem := "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"

	ok, _, _ := Copy([]byte(pem))

	if !ok {
		t.Fatal("Copy did not report success")
	}
	got, _ := os.ReadFile(stdin)
	if string(got) != pem {
		t.Errorf("clipboard got %q, want the whole value", got)
	}
}

// "Not installed" and "installed, broken" are different outcomes, and
// stopping at the first program that merely failed would mean a working one
// further down the list — wl-copy on a Wayland session reached over SSH,
// where xclip is present but has no $DISPLAY — is never tried.
func TestCopyTriesTheNextProgramWhenTheFirstInstalledOneFails(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("only one clipboard program is ever considered")
	}
	t.Setenv("WAYLAND_DISPLAY", "") // pin Commands()'s order for this test
	dir := t.TempDir()
	first, second := Commands()[0].Name, Commands()[1].Name
	stdin := filepath.Join(dir, "stdin")
	if err := os.WriteFile(filepath.Join(dir, first), []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, second), []byte("#!/bin/sh\ncat > "+stdin+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Prepended, not substituted — see fakeClipboard's own comment on this:
	// a PATH holding nothing but the stand-ins cannot find `sh` or `cat`
	// either, and the resulting exit status 127 looks like the code under
	// test failing when it is the test's own setup that is broken.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ok, failed, _ := Copy([]byte("s3cr3t"))

	if !ok {
		t.Fatalf("Copy did not fall through to the working program, failed = %v", failed)
	}
	if len(failed) != 1 {
		t.Errorf("failed = %v, want exactly the first program's failure recorded", failed)
	}
	got, err := os.ReadFile(stdin)
	if err != nil {
		t.Fatalf("the second program never ran: %v", err)
	}
	if string(got) != "s3cr3t" {
		t.Errorf("clipboard got %q, want the value", got)
	}
}

// A machine with no clipboard program at all: ok is false and failed is
// empty, which is what tells a caller to suggest installing one rather than
// report a program failure that never happened.
func TestCopyWithNoProgramInstalledReportsNoFailuresOnlyNothingTried(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	ok, failed, tried := Copy([]byte("x"))

	if ok {
		t.Fatal("Copy reported success with nothing on PATH")
	}
	if len(failed) != 0 {
		t.Errorf("failed = %v, want none — nothing installed ran at all", failed)
	}
	if len(tried) == 0 {
		t.Error("tried is empty — a caller has nothing left to suggest installing")
	}
}

// XWayland puts xclip on PATH inside a Wayland session, where it writes to
// an X11 clipboard nothing native reads: the copy reports success and the
// paste comes back with whatever was there before — the worst outcome
// available to a command whose whole job is "it is now where you need it".
func TestWaylandIsPreferredByEnvironmentNotByWhatIsInstalled(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("one clipboard, no ambiguity")
	}
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	if first := Commands()[0].Name; first != "wl-copy" {
		t.Errorf("under Wayland the first choice is %q", first)
	}
	t.Setenv("WAYLAND_DISPLAY", "")
	if first := Commands()[0].Name; first == "wl-copy" {
		t.Error("wl-copy is first without a compositor to talk to")
	}
}
