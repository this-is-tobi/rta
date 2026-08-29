package fs

import (
	"errors"
	"io/fs"
	"os"
)

// Crossing a filesystem boundary turns "what is using space here" into a
// different question, and sometimes into a hang: descending into a network
// mount, or into /proc, counts space that is not on this device and may not
// answer at all.
//
// The device number is the portable-enough way to tell, and where it is not
// available the check degrades to "always the same device" — see
// platform_unix.go and platform_windows.go. That split is a build tag rather
// than a type assertion because syscall.Stat_t does not merely fail to match
// on Windows, it does not exist: written as one file with a comma-ok
// assertion, this package did not compile for windows/amd64 at all, and the
// degradation the comment promised could never happen.

func deviceOf(path string) (uint64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return deviceOfInfo(info)
}

// sameDevice reports whether an entry lives on the filesystem the scan
// started on. A scanner that could not determine its own device does not
// exclude anything — refusing to descend on a platform where the answer is
// unavailable would silently report zero.
func (s *scanner) sameDevice(info os.FileInfo) bool {
	if s.device == 0 {
		return true
	}
	dev, ok := deviceOfInfo(info)
	if !ok {
		return true
	}
	return dev == s.device
}

func asPathError(err error, target **fs.PathError) bool {
	return errors.As(err, target)
}
