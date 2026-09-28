//go:build !windows

package pluginhost

import "os"

// socketDirPattern names the directory a plugin's socket is made in.
const socketDirPattern = "rta-sock-"

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
func socketDir() (string, error) {
	return os.MkdirTemp("", socketDirPattern)
}
