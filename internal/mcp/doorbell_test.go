package mcp

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/consent"
)

// recordingNotifier puts a notifier on PATH that writes what it was asked to
// show, one argument per line, and returns the file it writes to.
func recordingNotifier(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "shown")
	name := "notify-send"
	if runtime.GOOS == "darwin" {
		name = "osascript"
	}
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> '" + out + "'; done\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	bell.Lock()
	bell.off = false
	bell.Unlock()
	return out
}

// The id is eight hex digits on a banner that is gone in seconds; the command
// finds the waiting call itself, so the banner carries none.
func TestTheDoorbellNamesTheCommandAndNoRequestId(t *testing.T) {
	out := recordingNotifier(t)
	req := consent.Request{ID: "0a1b2c3d", Deadline: time.Now().Add(time.Minute)}

	// The notifier has three seconds to start, and a machine under load can
	// take longer to run a script it has just been written than that. A doorbell
	// that gave up is turned off for the rest of the server's life, so the test
	// switches it back on and rings again rather than failing on the host's
	// weather.
	var raw []byte
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		bell.Lock()
		bell.off = false
		bell.Unlock()
		ringDoorbell(context.Background(), "note.rm", req)
		if raw, err = os.ReadFile(out); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("nothing was shown: %v", err)
	}
	shown := string(raw)
	if !strings.Contains(shown, "note.rm needs your answer · rta agent allow\n") {
		t.Errorf("the doorbell does not read as the command that answers it: %q", shown)
	}
	if strings.Contains(shown, req.ID) || regexp.MustCompile(`\b[0-9a-f]{8}\b`).MatchString(shown) {
		t.Errorf("the doorbell still carries a request id: %q", shown)
	}
}
