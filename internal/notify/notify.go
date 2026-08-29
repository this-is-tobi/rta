// Package notify rings the doorbell on this machine's desktop, so a call
// parked for a person's answer is something they find out about rather than
// something they have to go looking for (the "what is not built here"
// list, now built).
//
// A shell-out to whatever notifier the desktop already has, in the same
// doctrine as git, ssh, kubectl and internal/clipboard: rta does not carry a
// DBus client or a Cocoa binding to say four words.
//
// **Text is never interpolated into a script.** AppleScript is a language,
// `display notification "…"` is a string literal in it, and a body assembled
// by concatenation is one quotation mark away from being code — on a machine
// whose whole reason for running rta is that something else on it is not
// trusted. So the AppleScript here is a constant that reads its arguments
// out of `argv`, and the text arrives as argv, past `--`. The property is
// held here rather than at each call site, because a caller who has to
// remember it is a caller who will one day forget.
//
// It is best-effort by construction. Every failure — no notifier installed,
// a headless box, macOS declining to show it, a notification daemon that
// never answers — is a notification the operator does not get, never a call
// that does not run.
package notify

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// timeout bounds the shell-out.
//
// notify-send talks to the desktop over DBus and blocks on the default DBus
// reply timeout — twenty-five seconds — when nothing is listening, which is
// the normal state of a server reached over SSH. A quarter of the operator's
// answering window spent finding out there is no desktop is worse than no
// doorbell at all.
const timeout = 3 * time.Second

// maxLen caps each field, in runes. A notification is a line and a half of
// somebody's screen; past this the desktop truncates it in its own way, and
// what gets cut is unpredictable.
const maxLen = 200

// ErrNoNotifier means this machine has nothing to show a notification with.
var ErrNoNotifier = errors.New("no desktop notifier on this machine")

// Note is one notification.
type Note struct {
	// Title and Body are plain text. Neither may carry markup, control
	// characters or newlines by the time they reach a notifier — clean does
	// that, and it is not the caller's job.
	Title string
	Body  string
	// TTL, when set, asks the desktop to take the notification away by
	// itself. A doorbell for a call that stopped waiting ten minutes ago is
	// a lie on somebody's screen. Honoured on Linux; macOS notifications go
	// to Notification Centre and stay there whatever anybody asks.
	TTL time.Duration
}

// Available reports whether a notifier is installed, without sending
// anything, so a caller can say "you asked for notifications and there are
// none here" once at startup rather than failing silently at each call.
func Available() bool {
	_, err := lookup()
	return err == nil
}

// Send shows the note, or says why it could not.
func Send(ctx context.Context, n Note) error {
	name, err := lookup()
	if err != nil {
		return err
	}
	args, err := argv(n)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	// No stdin, and its output goes nowhere: under `rta mcp serve` stdout is
	// the agent's JSON-RPC channel, and a notifier that decides to print a
	// deprecation warning must not be able to write into it.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return errors.New("the desktop did not answer")
		}
		return err
	}
	return nil
}

// lookup finds this platform's notifier.
//
// One per platform rather than a list to try: unlike the clipboard, where an
// installed-but-broken program is common enough to need a fallback, a
// notifier that is present and fails means there is no desktop session, and
// the next program down the list would fail the same way for the same
// reason.
func lookup() (string, error) {
	var name string
	switch runtime.GOOS {
	case "darwin":
		// Part of macOS since forever; the check is for a stripped image
		// rather than for a missing package.
		name = "osascript"
	case "linux", "freebsd", "openbsd", "netbsd":
		name = "notify-send"
	default:
		// Windows has toasts and no way to raise one that does not involve
		// asking PowerShell to construct XML. Claiming a doorbell that does
		// not ring is worse than saying there is none.
		return "", ErrNoNotifier
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", ErrNoNotifier
	}
	return path, nil
}

// argv builds the command line, with the text always in argument position.
//
// It cleans the text itself rather than taking it cleaned. The promise is
// that a notifier only ever receives one line of printable text, and a
// promise kept by whoever remembers to call a helper first is a promise
// with a hole in it the day somebody adds a second caller.
func argv(n Note) ([]string, error) {
	title, body := clean(n.Title), clean(n.Body)
	if title == "" {
		return nil, errors.New("a notification with no title is a blank box")
	}
	if runtime.GOOS == "darwin" {
		// The script is a constant. `on run argv` is how an osascript
		// invocation reads its arguments, and `--` stops the two that follow
		// from being read as options should a title ever begin with a dash.
		return []string{
			"-e", "on run argv",
			"-e", "display notification (item 1 of argv) with title (item 2 of argv)",
			"-e", "end run",
			"--", body, title,
		}, nil
	}
	args := []string{"--app-name=rta", "--urgency=normal"}
	if n.TTL > 0 {
		args = append(args, "--expire-time="+durationMillis(n.TTL))
	}
	// notify-send takes the summary and the body as positional arguments;
	// `--` keeps them positional whatever they start with.
	return append(args, "--", title, body), nil
}

func durationMillis(d time.Duration) string {
	ms := d.Milliseconds()
	if ms < 1000 {
		ms = 1000
	}
	// A day of milliseconds is past what any notification daemon honours,
	// and past what anybody means by "still relevant".
	if ms > 86_400_000 {
		ms = 86_400_000
	}
	return strconv.FormatInt(ms, 10)
}

// clean is the floor under every caller: one line of printable text, capped.
//
// Newlines and control characters go because a notification is one field on
// somebody's screen and a terminal is not the only thing that renders
// escape sequences. `<` goes because notify-send's body is parsed for a
// small set of HTML-ish tags on most desktops, and text that arrives as
// markup is text that displays as something other than what was written —
// the same reason declared text is refused an escape sequence in
// pkg/plugin/text.go, one channel along.
func clean(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= maxLen {
			b.WriteString("…")
			break
		}
		switch {
		case r == '<':
			b.WriteRune('(')
		case r == '>':
			b.WriteRune(')')
		case r == '&':
			b.WriteString("and")
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		case unicode.IsControl(r) || !unicode.IsGraphic(r):
			continue
		default:
			b.WriteRune(r)
		}
		n++
	}
	return strings.TrimSpace(b.String())
}
