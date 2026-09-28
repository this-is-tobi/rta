//go:build unix

package git

import (
	"os"

	"golang.org/x/sys/unix"
)

// mayExecute is whether access(2) finds path executable for this process,
// the question git asks of a hook's name before it runs it (hookStatus).
func mayExecute(path string, _ os.FileMode) bool { return unix.Access(path, unix.X_OK) == nil }
