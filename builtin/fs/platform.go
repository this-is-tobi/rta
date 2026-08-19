package fs

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// Crossing a filesystem boundary turns "what is using space here" into a
// different question, and sometimes into a hang: descending into a network
// mount, or into /proc, counts space that is not on this device and may not
// answer at all.
//
// The device number is the portable-enough way to tell. syscall.Stat_t is
// available on every unix Go builds for; where it is not (Windows), the check
// degrades to "always the same device", which is the pre-existing behaviour
// rather than a new failure.

func deviceOf(path string) (uint64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return deviceOfInfo(info)
}

func deviceOfInfo(info os.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
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
