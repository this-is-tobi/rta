//go:build unix

package git

import (
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// mayExecute is whether access(2) finds name in dir executable for this
// process, the question git asks of a hook's name before it runs it
// (hookStatus).
//
// Over MCP it is asked of the directory holding name as it is held open
// beneath the roots, and of name itself where it is, without following it: a
// hook that was a file when it was listed and a link out of the roots when it
// is asked about answers for the link, and says nothing of what is at its far
// end. The far end of a hook that was a link is asked about where the gate
// judged it to be (hookMode), which was no link.
func mayExecute(dir boundDir, name string, _ os.FileMode) bool {
	if dir.root == nil {
		return unix.Access(dir.join(name), unix.X_OK) == nil
	}
	parent, err := dir.OpenFile(filepath.Dir(name), os.O_RDONLY|syscall.O_NONBLOCK)
	if err != nil {
		return false
	}
	defer func() { _ = parent.Close() }()
	return unix.Faccessat(int(parent.Fd()), filepath.Base(name), unix.X_OK, unix.AT_SYMLINK_NOFOLLOW) == nil
}
