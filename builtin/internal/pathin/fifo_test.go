package pathin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A named pipe nothing writes to held open(2) for good, where no context
// reaches, and each call pinned an OS thread. Off the CLI it is refused, and
// promptly: the deadline is the assertion, because the failure is a call
// that never returns rather than one that returns wrong.
func TestAFIFOIsRefusedAtOnceOffTheCLI(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, sf := range []plugin.Surface{plugin.SurfaceMCP, plugin.SurfaceTUI, plugin.SurfaceCompletion} {
		done := make(chan error, 1)
		go func() {
			_, err := Read(on(sf), fifo, 64)
			done <- err
		}()
		select {
		case err := <-done:
			var notAFile *NotAFileError
			if !errors.As(err, &notAFile) || !strings.Contains(err.Error(), "a named pipe") {
				t.Errorf("%s: err = %v, want a NotAFileError saying it is a named pipe", sf, err)
			}
		case <-time.After(5 * time.Second):
			// Unblock the open this test leaked, so the process can exit.
			if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
				_ = w.Close()
			}
			t.Fatalf("%s: reading a FIFO with no writer did not return", sf)
		}
	}
}

// The CLI reads a pipe, as its guide says every Path input does: a person
// naming one at a terminal started its writer.
func TestTheCLIReadsAPipeItsWriterFeeds(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		_, _ = w.WriteString("piped")
		_ = w.Close()
	}()
	got, err := Read(on(plugin.SurfaceCLI), fifo, 64)
	if err != nil || string(got) != "piped" {
		t.Errorf("got %q, %v", got, err)
	}
}

// A file the caller did not name is read as a file on every surface: at a
// terminal, too, nobody started a writer for a pipe a repository left under
// the name of its lockfile.
func TestReadFileRefusesAFIFOOnTheCLITooAtOnce(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadFile(fifo, 64)
		done <- err
	}()
	select {
	case err := <-done:
		var notAFile *NotAFileError
		if !errors.As(err, &notAFile) || !strings.Contains(err.Error(), "a named pipe") {
			t.Errorf("err = %v, want a NotAFileError saying it is a named pipe", err)
		}
	case <-time.After(5 * time.Second):
		if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		t.Fatal("reading a FIFO nobody named did not return")
	}
}

// A named pipe where a directory is opened held the open as surely as one
// where a file is: os.OpenRoot, and os.Root.OpenRoot for the last name in
// its path, open without O_DIRECTORY, and a FIFO put in a directory's place
// between a walk looking at it and opening it held the walk, and its thread,
// for good. Refused at once instead, as a pipe opened to read is.
func TestAFIFOWhereADirectoryIsOpenedIsRefusedAtOnce(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for name, open := range map[string]func() error{
		"a path": func() error {
			d, err := OpenDir(on(plugin.SurfaceTUI), fifo)
			if err == nil {
				_ = d.Close()
			}
			return err
		},
		"a name in a directory": func() error {
			sub, err := openSub(root, "pipe")
			if err == nil {
				_ = sub.Close()
			}
			return err
		},
	} {
		done := make(chan error, 1)
		go func() { done <- open() }()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s: a FIFO was opened as a directory", name)
			}
		case <-time.After(5 * time.Second):
			if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
				_ = w.Close()
			}
			t.Fatalf("%s: opening a FIFO as a directory did not return", name)
		}
	}
}
