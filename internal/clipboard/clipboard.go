// Package clipboard puts a value on this machine's system clipboard by
// shelling out to whichever clipboard program is installed, the value
// arriving on the child's stdin and never in its argv.
//
// Kept separate from any one plugin because two callers need the same
// property for the same reason: builtin/kv's copy hands back a stored
// secret, and the TUI's own per-value copy (internal/render/tui) hands back
// a value a capability just generated, which exists nowhere else to
// re-fetch if a first attempt mangled it. Both need the real OS clipboard,
// not tea.SetClipboard's OSC 52 escape sequence — OSC 52 is base64 text
// written straight into the terminal's output stream, and tmux and several
// terminal emulators cap that stream's length well short of "arbitrary".
// Worse, OSC 52 only guarantees the bytes *it* carries: a renderer that
// wraps the value first — JSON with indentation, say — hands OSC 52
// perfectly good bytes that decode back to a string with newlines in it,
// and a paste target that reads a newline as Enter does not know or care
// that the newline was structure rather than part of the value.
package clipboard

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// timeout bounds each program's attempt. Every entry in Commands() is a
// local subprocess rather than a network round trip, so this is generous
// compared to internal/notify's DBus-derived 3 seconds — what this guards
// against is not a slow answer but a wedged one: a wrapped or shimmed
// binary that never returns, a compositor connection that never completes.
// Without a bound, one wedged program blocks Copy forever instead of moving
// on to the next entry in Commands() the way an outright failure already
// does, silently turning "the installed program is broken" into "the call
// never returns" — a frozen TUI, or a stuck MCP request for kv.copy.
//
// A var, not a const, so a test can shrink it rather than actually wait.
var timeout = 5 * time.Second

// Command is one program that can take bytes on stdin and put them on this
// machine's clipboard.
type Command struct {
	Name string
	args []string
}

// Commands returns what is worth trying here, best first.
//
// Wayland is decided by the environment rather than by which binaries
// happen to be installed. A Wayland desktop running XWayland has both
// wl-copy and xclip on PATH, and xclip there writes into the X11 clipboard
// that no native application reads: the copy reports success and the paste
// comes back with whatever was there before, which is the worst possible
// outcome for a command whose entire job is "the value is now where you
// need it".
//
// clip.exe is in the list for WSL, which has neither an X server nor a
// compositor but does have the Windows clipboard one exec away.
func Commands() []Command {
	switch runtime.GOOS {
	case "darwin":
		return []Command{{Name: "pbcopy"}}
	case "windows":
		return []Command{{Name: "clip"}}
	}
	rest := []Command{
		{Name: "xclip", args: []string{"-selection", "clipboard"}},
		{Name: "xsel", args: []string{"--clipboard", "--input"}},
		{Name: "clip.exe"},
		{Name: "termux-clipboard-set"},
	}
	wayland := Command{Name: "wl-copy"}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return append([]Command{wayland}, rest...)
	}
	return append(rest, wayland)
}

// Copy hands value to the first clipboard program installed, writing only
// to its stdin and never its argv.
//
// Every process on the machine can read every other process's command
// line — `ps auxww`, /proc/*/cmdline — so a value passed as an argument
// would be published to every account on the box for as long as the copy
// took, which is a wider audience than the terminal this exists to keep it
// off.
//
// ok reports whether some program accepted the value. failed names each
// installed program that ran and refused it, paired with its error —
// trying continues down the list rather than stopping at the first,
// because installed and working are different things: xclip is present and
// exits nonzero with no $DISPLAY (common over SSH), and stopping there
// means wl-copy, which would have worked on a Wayland session reached the
// same way, is never reached. tried names every program considered at all,
// installed or not, for a caller that wants to say what to go install.
func Copy(value []byte) (ok bool, failed, tried []string) {
	for _, c := range Commands() {
		tried = append(tried, c.Name)
		path, err := exec.LookPath(c.Name)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmd := exec.CommandContext(ctx, path, c.args...)
		cmd.Stdin = bytes.NewReader(value)
		harden(cmd)
		cmd.Cancel = func() error { reap(cmd); return ctx.Err() }
		err = cmd.Run()
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				failed = append(failed, fmt.Sprintf("%s (timed out)", c.Name))
			} else {
				failed = append(failed, fmt.Sprintf("%s (%v)", c.Name, err))
			}
			continue
		}
		return true, failed, tried
	}
	return false, failed, tried
}
