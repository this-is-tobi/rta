package pluginhost

import (
	"os"
	"strings"
	"syscall"

	"github.com/this-is-tobi/rta/pkg/view"
)

// socketDirPattern names the directory a plugin's socket is made in, and
// socketTail is the most a socket's path takes past TMPDIR: a separator,
// that name with the ten digits os.MkdirTemp puts on it at most, another
// separator, and the "plugin" and ten digits go-plugin's own os.CreateTemp
// names the socket with. Short on purpose — every byte of it is one fewer
// that TMPDIR may have.
const (
	socketDirPattern = "rta-sock-"
	socketTail       = 1 + len(socketDirPattern) + 10 + 1 + len("plugin") + 10
)

// maxSocketPath is the longest path a unix socket can be bound at here: the
// kernel's sun_path, less the NUL Go's syscall package keeps room for. 103
// on macOS and the BSDs, 107 on Linux.
var maxSocketPath = len(syscall.RawSockaddrUnix{}.Path) - 1

// socketDir makes the directory one plugin process creates its socket in,
// which teardown removes once the process has ended.
//
// **go-plugin left every socket behind.** Its server makes one with
// os.CreateTemp in PLUGIN_UNIX_SOCKET_DIR, and with nothing there, straight
// in $TMPDIR as plugin and ten digits, and removes it only when the listener
// it was bound through is closed. With the broker multiplexed
// (GRPCBrokerMultiplex, launch) the listener Serve closes on its way out is
// the multiplexer, whose Close ends its yamux session and not the socket
// beneath it — so an ordinary run, a graceful exit included, left one, and a
// plugin killed once its grace ran out, or ended by a forced exit, left one
// whatever the listener did. Measured on one developer's machine: 74,664 of
// them in TMPDIR.
//
// So the host names the directory, and removing it trusts nothing the plugin
// says: rta made it, and removes it whole. Not the address the handshake
// names — that is the plugin's to choose, and a host that deleted whatever
// path a plugin announced would delete any file a plugin wanted gone. 0700,
// as os.MkdirTemp makes it, so the socket inside is this user's alone, which
// is what the multiplexing was once believed to give.
//
// A forced exit reaches the removal through CloseAll, which the exit runs.
// An rta killed outright (SIGKILL) reaches nothing, and its directory stays,
// holding at most a dead socket. Nothing sweeps those: a directory with this
// name cannot say whether the rta that made it is gone or is another one,
// still running, whose plugin is listening in it, and removing that one would
// cut a live plugin off. Leaving it costs an empty directory.
//
// **A TMPDIR too long for a socket is refused here, before anything starts.**
// A unix socket's path has a fixed limit, and past it the plugin's bind fails
// with EINVAL, it exits before its handshake, and all rta could report was
// go-plugin's "Failed to read any lines from plugin's stdout" and its guesses
// — the wrong architecture, a missing library, a file mode — none of which
// was the cause. Judged on the longest the path can be, so a TMPDIR either
// works or does not, rather than failing on the runs whose random digits
// happened to come out long.
//
// A TMPDIR that cannot hold the directory at all — one that does not exist,
// or that this user cannot write to — is refused by name for the same
// reason. It used to fail in the plugin, with the same guesses; left as
// os.MkdirTemp's own error it reached the operator as "stat …: no such file
// or directory" folded into a plugin that had gone, which named neither
// TMPDIR nor the fix.
func socketDir() (string, error) {
	base := os.TempDir()
	trimmed := strings.TrimRight(base, "/")
	if len(trimmed)+socketTail > maxSocketPath {
		return "", view.Errorf("plugin.tmpdir.toolong",
			"TMPDIR is too long for the socket a plugin listens on: a unix socket's path is at "+
				"most %d bytes here, the socket and its directory take %d of them, and TMPDIR "+
				"(%s) is %d — a TMPDIR of at most %d bytes lets the plugin start",
			maxSocketPath, socketTail, base, len(trimmed), maxSocketPath-socketTail).
			WithHint("point TMPDIR at a shorter directory that only you can write to; rta " +
				"makes a private directory inside it for each plugin's socket")
	}
	dir, err := os.MkdirTemp(base, socketDirPattern)
	if err != nil {
		return "", view.Errorf("plugin.tmpdir.unusable",
			"TMPDIR (%s) cannot hold the directory a plugin's socket is made in: %v", base, err).
			WithHint("point TMPDIR at a directory that exists and that only you can write to")
	}
	return dir, nil
}
